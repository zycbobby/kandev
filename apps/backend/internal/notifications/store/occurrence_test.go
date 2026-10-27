package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/notifications/models"
)

// seedOccurrenceSQL inserts one occurrence row with a controlled creation
// time, bypassing the repository so retention tests can pin exact ages.
func seedOccurrenceSQL(t *testing.T, database *sqlx.DB, userID, occurrenceID string, createdAt time.Time) {
	t.Helper()
	_, err := database.Exec(`
		INSERT INTO notification_occurrences (id, user_id, event_type, occurrence_id, task_id, session_id, title, body, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, fmt.Sprintf("row-%s-%s", userID, occurrenceID), userID, "session.turn_finished", occurrenceID,
		"task-"+occurrenceID, "session-"+occurrenceID, "Title "+occurrenceID, "Body "+occurrenceID, createdAt)
	if err != nil {
		t.Fatalf("seed occurrence %s: %v", occurrenceID, err)
	}
}

func TestSQLiteOccurrenceInsertDedupAndList(t *testing.T) {
	database := openNotificationTestDB(t)
	repo, err := newSQLiteRepositoryWithDB(context.Background(), database, database)
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}
	ctx := context.Background()

	inserted, err := repo.InsertOccurrence(ctx, &models.Occurrence{
		UserID: "user-1", EventType: "session.turn_finished", OccurrenceID: "turn-1",
		TaskID: "task-1", SessionID: "session-1", Title: "Agent turn finished", Body: "The agent finished a turn.",
	})
	if err != nil || !inserted {
		t.Fatalf("insert turn-1 = %v (inserted %t)", err, inserted)
	}
	replayed, err := repo.InsertOccurrence(ctx, &models.Occurrence{
		UserID: "user-1", EventType: "session.turn_finished", OccurrenceID: "turn-1",
	})
	if err != nil || replayed {
		t.Fatalf("replay turn-1 = %v (inserted %t), want dedup", err, replayed)
	}
	if _, err := repo.InsertOccurrence(ctx, &models.Occurrence{UserID: "user-1", EventType: "session.turn_finished"}); err == nil {
		t.Fatal("insert without an occurrence ID must fail")
	}
	if _, err := repo.InsertOccurrence(ctx, &models.Occurrence{
		UserID: "user-2", EventType: "session.turn_finished", OccurrenceID: "turn-1",
	}); err != nil {
		t.Fatalf("same occurrence for another user = %v, want a separate row", err)
	}
	if _, err := repo.InsertOccurrence(ctx, &models.Occurrence{
		UserID: "user-1", EventType: "session.clarification_requested", OccurrenceID: "turn-1",
	}); err != nil {
		t.Fatalf("same occurrence ID for another event = %v, want a separate row", err)
	}

	// The store applies the limit literally; the service owns the default
	// and ceiling clamps, so callers here pass explicit limits.
	occurrences, err := repo.ListOccurrences(ctx, "user-1", time.Time{}, 10)
	if err != nil {
		t.Fatalf("list occurrences: %v", err)
	}
	if len(occurrences) != 2 {
		t.Fatalf("occurrences = %#v, want one per (event, occurrence)", occurrences)
	}
	first := occurrences[0]
	if first.EventType != "session.turn_finished" || first.OccurrenceID != "turn-1" ||
		first.TaskID != "task-1" || first.SessionID != "session-1" ||
		first.Title != "Agent turn finished" || first.Body != "The agent finished a turn." {
		t.Fatalf("first occurrence = %#v", first)
	}
	if first.CreatedAt.IsZero() {
		t.Fatal("occurrence CreatedAt must be assigned at insert")
	}

	limited, err := repo.ListOccurrences(ctx, "user-1", time.Time{}, 1)
	if err != nil {
		t.Fatalf("list limited occurrences: %v", err)
	}
	if len(limited) != 1 || limited[0].OccurrenceID != "turn-1" {
		t.Fatalf("limited occurrences = %#v, want only the oldest", limited)
	}

	scoped, err := repo.ListOccurrences(ctx, "user-2", time.Time{}, 10)
	if err != nil {
		t.Fatalf("list scoped occurrences: %v", err)
	}
	if len(scoped) != 1 || scoped[0].UserID != "user-2" {
		t.Fatalf("scoped occurrences = %#v, want only user-2's row", scoped)
	}
}

func TestSQLiteOccurrenceSweepDeletesExpiredRows(t *testing.T) {
	database := openNotificationTestDB(t)
	repo, err := newSQLiteRepositoryWithDB(context.Background(), database, database)
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	seedOccurrenceSQL(t, database, "user-1", "stale", now.Add(-8*24*time.Hour))
	seedOccurrenceSQL(t, database, "user-1", "fresh", now.Add(-time.Hour))

	deleted, err := repo.SweepOccurrences(context.Background(), now.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatalf("sweep occurrences: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("swept %d rows, want only the expired one", deleted)
	}
	var remaining int
	if err := database.Get(&remaining, `SELECT COUNT(*) FROM notification_occurrences`); err != nil {
		t.Fatalf("count occurrences: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("remaining occurrences = %d, want the fresh row only", remaining)
	}
}

func TestSQLiteOccurrenceStartupSweepOnConstruction(t *testing.T) {
	database := openNotificationTestDB(t)
	if _, err := newSQLiteRepositoryWithDB(context.Background(), database, database); err != nil {
		t.Fatalf("create repository: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	seedOccurrenceSQL(t, database, "user-1", "stale", now.Add(-8*24*time.Hour))
	seedOccurrenceSQL(t, database, "user-1", "fresh", now.Add(-time.Hour))

	// Constructing a repository over the existing database sweeps at startup.
	if _, err := newSQLiteRepositoryWithDB(context.Background(), database, database); err != nil {
		t.Fatalf("recreate repository: %v", err)
	}
	var occurrenceID string
	if err := database.Get(&occurrenceID, `SELECT occurrence_id FROM notification_occurrences`); err != nil {
		t.Fatalf("read surviving occurrence: %v", err)
	}
	if occurrenceID != "fresh" {
		t.Fatalf("surviving occurrence = %q, want fresh", occurrenceID)
	}
}
