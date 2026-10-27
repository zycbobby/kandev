package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/notifications/models"
	"github.com/kandev/kandev/internal/testutil"
)

// newOccurrenceRepositoryPostgres opens an isolated PostgreSQL schema shared
// by the writer and reader, matching production's single pool for PostgreSQL,
// and skips the test when KANDEV_TEST_POSTGRES_DSN is unset.
func newOccurrenceRepositoryPostgres(t *testing.T) (*sqliteRepository, *sqlx.DB) {
	t.Helper()
	dsn := testutil.PostgresDSNFromEnv(t)
	pg := testutil.OpenIsolatedPostgres(t, dsn)
	repo, err := newSQLiteRepositoryWithDB(context.Background(), pg, pg)
	if err != nil {
		t.Fatalf("create notification repository: %v", err)
	}
	return repo, pg
}

// seedPostgresOccurrence inserts one occurrence row with a controlled
// creation time so the cursor and sweep windows are deterministic.
func seedPostgresOccurrence(t *testing.T, database *sqlx.DB, userID, occurrenceID string, createdAt time.Time) {
	t.Helper()
	_, err := database.Exec(database.Rebind(`
		INSERT INTO notification_occurrences (id, user_id, event_type, occurrence_id, task_id, session_id, title, body, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`), fmt.Sprintf("row-%s-%s", userID, occurrenceID), userID, "session.turn_finished", occurrenceID,
		"task-"+occurrenceID, "session-"+occurrenceID, "Title "+occurrenceID, "Body "+occurrenceID, createdAt.UTC())
	if err != nil {
		t.Fatalf("seed occurrence %s: %v", occurrenceID, err)
	}
}

func assertOccurrenceIDs(t *testing.T, occurrences []*models.Occurrence, want ...string) {
	t.Helper()
	if len(occurrences) != len(want) {
		t.Fatalf("occurrences = %#v, want %v", occurrences, want)
	}
	ids := make([]string, 0, len(occurrences))
	for _, occurrence := range occurrences {
		ids = append(ids, occurrence.OccurrenceID)
	}
	for index, expected := range want {
		if ids[index] != expected {
			t.Fatalf("occurrence ids = %v, want %v", ids, want)
		}
	}
}

// TestOccurrenceLifecycle_Postgres is the environment-gated PostgreSQL twin
// of the SQLite occurrence tests. ON CONFLICT dedup, the strictly exclusive
// since cursor with LIMIT, per-user scoping, and the retention sweep are all
// dialect-sensitive and must behave identically on PostgreSQL.
func TestOccurrenceLifecycle_Postgres(t *testing.T) {
	repo, database := newOccurrenceRepositoryPostgres(t)
	ctx := context.Background()

	// Insert assigns the creation time and dedups a replayed dispatch.
	inserted, err := repo.InsertOccurrence(ctx, &models.Occurrence{
		UserID: "user-1", EventType: "session.turn_finished", OccurrenceID: "turn-1",
		TaskID: "task-1", SessionID: "session-1", Title: "Agent turn finished", Body: "The agent finished a turn.",
	})
	if err != nil || !inserted {
		t.Fatalf("insert turn-1 = %v (inserted %t)", err, inserted)
	}
	stored, err := repo.ListOccurrences(ctx, "user-1", time.Time{}, 10)
	if err != nil {
		t.Fatalf("list occurrences: %v", err)
	}
	assertOccurrenceIDs(t, stored, "turn-1")
	if stored[0].CreatedAt.IsZero() || stored[0].CreatedAt.Location() != time.UTC {
		t.Fatalf("turn-1 created_at = %v, want a non-zero UTC timestamp", stored[0].CreatedAt)
	}
	base := stored[0].CreatedAt
	replayed, err := repo.InsertOccurrence(ctx, &models.Occurrence{
		UserID: "user-1", EventType: "session.turn_finished", OccurrenceID: "turn-1",
	})
	if err != nil || replayed {
		t.Fatalf("replay turn-1 = %v (inserted %t), want dedup", err, replayed)
	}

	// Deterministic cursor, scoping, and sweep windows around the stored row.
	seedPostgresOccurrence(t, database, "user-1", "later", base.Add(10*time.Minute))
	seedPostgresOccurrence(t, database, "user-2", "other-user", base.Add(20*time.Minute))
	seedPostgresOccurrence(t, database, "user-1", "expired", base.Add(-8*24*time.Hour))

	// The since cursor is exclusive and rows return oldest first.
	rows, err := repo.ListOccurrences(ctx, "user-1", base.Add(5*time.Minute), 10)
	if err != nil {
		t.Fatalf("list occurrences after cursor: %v", err)
	}
	assertOccurrenceIDs(t, rows, "later")

	rows, err = repo.ListOccurrences(ctx, "user-1", time.Time{}, 10)
	if err != nil {
		t.Fatalf("list occurrences: %v", err)
	}
	assertOccurrenceIDs(t, rows, "expired", "turn-1", "later")

	// An explicit limit truncates the window, oldest first.
	rows, err = repo.ListOccurrences(ctx, "user-1", time.Time{}, 1)
	if err != nil {
		t.Fatalf("list limited occurrences: %v", err)
	}
	assertOccurrenceIDs(t, rows, "expired")

	// Rows stay scoped to one user.
	rows, err = repo.ListOccurrences(ctx, "user-2", time.Time{}, 10)
	if err != nil {
		t.Fatalf("list scoped occurrences: %v", err)
	}
	assertOccurrenceIDs(t, rows, "other-user")

	// The sweep removes only rows older than the cutoff.
	deleted, err := repo.SweepOccurrences(ctx, base.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatalf("sweep occurrences: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("swept %d rows, want only the expired one", deleted)
	}
	rows, err = repo.ListOccurrences(ctx, "user-1", time.Time{}, 10)
	if err != nil {
		t.Fatalf("list occurrences after sweep: %v", err)
	}
	assertOccurrenceIDs(t, rows, "turn-1", "later")
}
