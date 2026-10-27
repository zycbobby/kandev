package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"go.uber.org/zap"

	"github.com/kandev/kandev/internal/auth/authn"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/notifications/controller"
	"github.com/kandev/kandev/internal/notifications/dto"
	"github.com/kandev/kandev/internal/notifications/service"
	notificationstore "github.com/kandev/kandev/internal/notifications/store"
	userstore "github.com/kandev/kandev/internal/user/store"
)

// newOccurrenceAPI builds the full notification stack over a real SQLite
// store. The middleware below stands in for auth/httpmw, mirroring
// ownership_test.go, so these tests exercise the real handler, controller,
// service and SQLite store together.
func newOccurrenceAPI(t *testing.T) (*gin.Engine, *sqlx.DB) {
	t.Helper()
	// Keep the auto-provisioned system provider out so default-provider
	// provisioning never writes behind the test's back.
	t.Setenv("KANDEV_DESKTOP_NATIVE_NOTIFICATIONS", "true")
	gin.SetMode(gin.TestMode)

	conn, err := db.OpenSQLite(filepath.Join(t.TempDir(), "notifications.db"))
	if err != nil {
		t.Fatalf("open SQLite database: %v", err)
	}
	database := sqlx.NewDb(conn, "sqlite3")
	t.Cleanup(func() { _ = database.Close() })
	repo, cleanup, err := notificationstore.Provide(context.Background(), database, database)
	if err != nil {
		t.Fatalf("create notification store: %v", err)
	}
	t.Cleanup(func() { _ = cleanup() })

	log, err := logger.NewFromZap(zap.NewNop())
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if userID := c.GetHeader(testUserHeader); userID != "" {
			authn.SetOnGin(c, authn.Identity{UserID: userID, Role: authn.RoleMember})
		}
		c.Next()
	})
	RegisterRoutes(router, controller.NewController(service.NewService(repo, nil, nil, log, nil)), log)
	return router, database
}

// seedOccurrence inserts one occurrence row with a controlled creation time,
// bypassing the service so tests can pin the since cursor precisely.
func seedOccurrence(t *testing.T, database *sqlx.DB, userID, occurrenceID string, createdAt time.Time) {
	t.Helper()
	_, err := database.Exec(`
		INSERT INTO notification_occurrences (id, user_id, event_type, occurrence_id, task_id, session_id, title, body, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, fmt.Sprintf("row-%s-%s", userID, occurrenceID), userID, "session.turn_finished", occurrenceID,
		"task-"+occurrenceID, "session-"+occurrenceID, "Title "+occurrenceID, "Body "+occurrenceID, createdAt.UTC())
	if err != nil {
		t.Fatalf("seed occurrence %s: %v", occurrenceID, err)
	}
}

func listOccurrencesAs(t *testing.T, router *gin.Engine, userID, query string) ([]map[string]any, int) {
	t.Helper()
	response := callAs(t, router, userID, http.MethodGet, "/api/v1/notifications/occurrences"+query, "")
	var rows []map[string]any
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &rows); err != nil {
			t.Fatalf("decode occurrences: %v (body: %s)", err, response.Body.String())
		}
	}
	return rows, response.Code
}

func TestListOccurrencesSinceAndLimit(t *testing.T) {
	t.Run("since is exclusive and rows return oldest first", func(t *testing.T) {
		router, database := newOccurrenceAPI(t)
		base := time.Now().UTC().Truncate(time.Second)
		seedOccurrence(t, database, "user-a", "before-1", base.Add(-3*time.Hour))
		seedOccurrence(t, database, "user-a", "before-2", base.Add(-2*time.Hour))
		seedOccurrence(t, database, "user-a", "after-1", base.Add(-1*time.Hour))
		seedOccurrence(t, database, "user-a", "after-2", base)

		since := url.QueryEscape(base.Add(-2 * time.Hour).Format(time.RFC3339))
		rows, code := listOccurrencesAs(t, router, "user-a", "?since="+since)
		if code != http.StatusOK {
			t.Fatalf("list occurrences = %d, want 200", code)
		}
		if len(rows) != 2 {
			t.Fatalf("rows = %d, want the two occurrences after the cursor", len(rows))
		}
		wantKeys := map[string]bool{
			"event": true, "occurrence_id": true, "task_id": true, "session_id": true,
			"title": true, "body": true, "created_at": true,
		}
		gotKeys := make(map[string]bool, len(rows[0]))
		for key := range rows[0] {
			gotKeys[key] = true
		}
		for key := range wantKeys {
			if !gotKeys[key] {
				t.Fatalf("row keys = %v, missing %q", gotKeys, key)
			}
		}
		for key := range gotKeys {
			if !wantKeys[key] {
				t.Fatalf("row keys = %v, unexpected %q", gotKeys, key)
			}
		}

		var typed []dto.NotificationOccurrenceDTO
		response := callAs(t, router, "user-a", http.MethodGet, "/api/v1/notifications/occurrences?since="+since, "")
		if err := json.Unmarshal(response.Body.Bytes(), &typed); err != nil {
			t.Fatalf("decode typed occurrences: %v", err)
		}
		first, second := typed[0], typed[1]
		if first.OccurrenceID != "after-1" || second.OccurrenceID != "after-2" {
			t.Fatalf("typed rows = %#v, want after-1 then after-2 (oldest first)", typed)
		}
		if first.Event != "session.turn_finished" || first.TaskID != "task-after-1" ||
			first.SessionID != "session-after-1" || first.Title != "Title after-1" || first.Body != "Body after-1" {
			t.Fatalf("typed first row = %#v", first)
		}
		if first.CreatedAt != base.Add(-1*time.Hour).Format(time.RFC3339) {
			t.Fatalf("typed created_at = %q, want the RFC3339 timestamp", first.CreatedAt)
		}

		// An explicit limit truncates the same window, oldest first.
		rows, code = listOccurrencesAs(t, router, "user-a", "?since="+since+"&limit=1")
		if code != http.StatusOK || len(rows) != 1 || rows[0]["occurrence_id"] != "after-1" {
			t.Fatalf("limited rows = %#v (code %d), want only after-1", rows, code)
		}
	})

	t.Run("limit defaults to 100 and caps at 200", func(t *testing.T) {
		router, database := newOccurrenceAPI(t)
		base := time.Now().UTC().Truncate(time.Second)
		for i := range 205 {
			seedOccurrence(t, database, "user-a", fmt.Sprintf("occ-%03d", i), base.Add(time.Duration(i)*time.Minute))
		}

		rows, code := listOccurrencesAs(t, router, "user-a", "")
		if code != http.StatusOK {
			t.Fatalf("default list = %d, want 200", code)
		}
		if len(rows) != 100 {
			t.Fatalf("default limit returned %d rows, want 100", len(rows))
		}
		if got := rows[0]["occurrence_id"]; got != "occ-000" {
			t.Fatalf("first default row = %#v, want occ-000 (oldest first)", rows[0])
		}

		rows, code = listOccurrencesAs(t, router, "user-a", "?limit=1000")
		if code != http.StatusOK {
			t.Fatalf("capped list = %d, want 200", code)
		}
		if len(rows) != 200 {
			t.Fatalf("limit=1000 returned %d rows, want the 200 cap", len(rows))
		}
	})

	t.Run("invalid parameters are rejected", func(t *testing.T) {
		router, _ := newOccurrenceAPI(t)
		for _, query := range []string{
			"?since=not-a-timestamp",
			"?limit=abc",
			"?limit=-1",
		} {
			if _, code := listOccurrencesAs(t, router, "user-a", query); code != http.StatusBadRequest {
				t.Fatalf("query %q = %d, want 400", query, code)
			}
		}
	})
}

func TestListOccurrencesScopedToUser(t *testing.T) {
	router, database := newOccurrenceAPI(t)
	base := time.Now().UTC().Truncate(time.Second)
	seedOccurrence(t, database, "user-a", "a-1", base.Add(-3*time.Minute))
	seedOccurrence(t, database, "user-a", "a-2", base.Add(-2*time.Minute))
	seedOccurrence(t, database, "user-b", "b-1", base.Add(-time.Minute))
	seedOccurrence(t, database, userstore.DefaultUserID, "d-1", base)

	assertOccurrenceIDs := func(t *testing.T, rows []map[string]any, want ...string) {
		t.Helper()
		if len(rows) != len(want) {
			t.Fatalf("rows = %#v, want %v", rows, want)
		}
		for index, expected := range want {
			if got := rows[index]["occurrence_id"]; got != expected {
				t.Fatalf("row %d = %#v, want %s", index, rows[index], expected)
			}
		}
	}

	rows, code := listOccurrencesAs(t, router, "user-a", "")
	if code != http.StatusOK {
		t.Fatalf("list as user-a = %d", code)
	}
	assertOccurrenceIDs(t, rows, "a-1", "a-2")

	rows, code = listOccurrencesAs(t, router, "user-b", "")
	if code != http.StatusOK {
		t.Fatalf("list as user-b = %d", code)
	}
	assertOccurrenceIDs(t, rows, "b-1")

	// With authentication disabled the request carries no identity and
	// resolves to the pre-auth default user, never to another user's rows.
	rows, code = listOccurrencesAs(t, router, "", "")
	if code != http.StatusOK {
		t.Fatalf("list without identity = %d", code)
	}
	assertOccurrenceIDs(t, rows, "d-1")
}
