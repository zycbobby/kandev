package statsync

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"

	"github.com/kandev/kandev/internal/testutil"
)

// testDB opens a foreign-key-enforced in-memory SQLite handle, the same
// connection shape production uses. Each test gets its own named in-memory
// database so parallel or sequential tests cannot see each other's rows.
func testDB(t *testing.T) *sqlx.DB {
	t.Helper()
	uri := fmt.Sprintf("file:statsync-%s?mode=memory&cache=shared&_foreign_keys=on", strings.NewReplacer("/", "-", " ", "_").Replace(t.Name()))
	raw, err := sql.Open("sqlite3", uri)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	db := sqlx.NewDb(raw, "sqlite3")
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seedCoreAnalyticsFixture creates the minimal core rows the v_stats_* views
// union against, so the views can be queried without the full task schema.
func seedCoreAnalyticsFixture(t *testing.T, db *sqlx.DB) {
	t.Helper()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS workflow_steps (
			id TEXT PRIMARY KEY,
			workflow_id TEXT NOT NULL,
			position INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS repositories (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			name TEXT NOT NULL,
			deleted_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL DEFAULT '',
			workflow_id TEXT NOT NULL DEFAULT '',
			workflow_step_id TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL,
			state TEXT DEFAULT 'TODO',
			is_ephemeral INTEGER NOT NULL DEFAULT 1,
			origin TEXT DEFAULT 'manual',
			archived_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS task_repositories (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			repository_id TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS task_sessions (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			started_at TIMESTAMP NOT NULL,
			completed_at TIMESTAMP,
			repository_id TEXT DEFAULT '',
			agent_profile_snapshot TEXT DEFAULT '{}'
		)`,
		`CREATE TABLE IF NOT EXISTS task_session_turns (
			id TEXT PRIMARY KEY,
			task_session_id TEXT NOT NULL,
			started_at TIMESTAMP NOT NULL,
			completed_at TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS task_session_messages (
			id TEXT PRIMARY KEY,
			task_session_id TEXT NOT NULL,
			turn_id TEXT NOT NULL,
			author_type TEXT NOT NULL DEFAULT 'user',
			type TEXT NOT NULL DEFAULT 'message',
			created_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS task_session_commits (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			commit_sha TEXT NOT NULL,
			committed_at TIMESTAMP NOT NULL,
			files_changed INTEGER DEFAULT 0,
			insertions INTEGER DEFAULT 0,
			deletions INTEGER DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS task_session_git_snapshots (
			id TEXT PRIMARY KEY,
			session_id TEXT,
			snapshot_type TEXT NOT NULL,
			files TEXT DEFAULT '{}',
			created_at TIMESTAMP NOT NULL
		)`,
		`INSERT INTO workflow_steps (id, workflow_id, position) VALUES ('ws-step-1', 'wf-1', 0), ('ws-step-2', 'wf-1', 1)`,
		`INSERT INTO repositories (id, workspace_id, name) VALUES ('local-repo-1', 'ws-local', 'kandev')`,
		`INSERT INTO tasks (id, workspace_id, workflow_id, workflow_step_id, title, state, is_ephemeral, origin, created_at, updated_at)
			VALUES ('local-task-1', 'ws-local', 'wf-1', 'ws-step-2', 'Local task', 'DONE', 0, 'manual', '2026-10-01 08:00:00+00:00', '2026-10-01 09:00:00+00:00')`,
		`INSERT INTO task_repositories (id, task_id, repository_id) VALUES ('local-tr-1', 'local-task-1', 'local-repo-1')`,
		`INSERT INTO task_sessions (id, task_id, started_at, repository_id, agent_profile_snapshot)
			VALUES ('local-sess-1', 'local-task-1', '2026-10-01 08:05:00+00:00', 'local-repo-1', '{"model":"m1","model_name":"M1","llm":"claude"}')`,
		`INSERT INTO task_session_turns (id, task_session_id, started_at) VALUES ('local-turn-1', 'local-sess-1', '2026-10-01 08:05:00+00:00')`,
		`INSERT INTO task_session_messages (id, task_session_id, turn_id, author_type, type, created_at)
			VALUES ('local-msg-1', 'local-sess-1', 'local-turn-1', 'user', 'message', '2026-10-01 08:05:01+00:00')`,
		`INSERT INTO task_session_commits (id, session_id, commit_sha, committed_at, files_changed, insertions, deletions)
			VALUES ('local-commit-1', 'local-sess-1', 'sha-local', '2026-10-01 09:00:00+00:00', 2, 10, 5)`,
		`INSERT INTO task_session_git_snapshots (id, session_id, snapshot_type, files, created_at)
			VALUES ('local-snap-1', 'local-sess-1', 'pending', '{"a.go":{"additions":3,"deletions":1}}', '2026-10-01 08:30:00+00:00')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(db.Rebind(statement)); err != nil {
			t.Fatalf("seed core fixture: %v", err)
		}
	}
}

func newTestRepository(t *testing.T, db *sqlx.DB) *Repository {
	t.Helper()
	repo, err := NewWithDB(db, db)
	if err != nil {
		t.Fatalf("new statsync repository: %v", err)
	}
	// Mirror the boot order: views only exist after every store schema has
	// settled, which in these fixtures is "already true".
	if err := EnsureViews(db); err != nil {
		t.Fatalf("ensure statsync views: %v", err)
	}
	return repo
}

func TestSchemaFreshAndReplay(t *testing.T) {
	db := testDB(t)
	seedCoreAnalyticsFixture(t, db)

	newTestRepository(t, db)
	// Replay on the same DB must be a no-op, including the view definitions.
	newTestRepository(t, db)

	for _, view := range mirrorViews {
		var count int
		if err := db.Get(&count, "SELECT COUNT(*) FROM "+view.name); err != nil {
			t.Fatalf("query %s after replay: %v", view.name, err)
		}
		if count != 1 {
			t.Fatalf("%s exposes %d core rows, want 1 (fixture core row)", view.name, count)
		}
	}
}

func TestViewsUnionMirrorRowsWithInclusionConstants(t *testing.T) {
	db := testDB(t)
	seedCoreAnalyticsFixture(t, db)
	repo := newTestRepository(t, db)

	env := validEnvelope()
	for i := range env.Tasks {
		env.Tasks[i].ResolvedWorkspaceID = "ws-local"
	}
	if _, err := repo.ApplyDelta(context.Background(), env); err != nil {
		t.Fatalf("apply delta: %v", err)
	}

	var completed int
	if err := db.Get(&completed, `SELECT completed FROM v_stats_tasks WHERE id = 'remote-1:task-1'`); err != nil {
		t.Fatalf("read mirror task through view: %v", err)
	}
	if completed != 1 {
		t.Fatalf("mirror task completed = %d, want 1 (remote completed=true)", completed)
	}
	var localCompleted int
	if err := db.Get(&localCompleted, `SELECT completed FROM v_stats_tasks WHERE id = 'local-task-1'`); err != nil {
		t.Fatalf("read local task through view: %v", err)
	}
	// Local task sits on the workflow's last step (position 1 of 2), so the
	// view's core-arm last-step rule must also report it completed.
	if localCompleted != 1 {
		t.Fatalf("local task completed = %d, want 1 (last workflow step)", localCompleted)
	}

	var origin string
	var isEphemeral int
	if err := db.QueryRowx(`SELECT origin, is_ephemeral FROM v_stats_tasks WHERE id = 'remote-1:task-1'`).
		Scan(&origin, &isEphemeral); err != nil {
		t.Fatalf("read mirror constants: %v", err)
	}
	if origin != "manual" || isEphemeral != 0 {
		t.Fatalf("mirror constants origin=%q is_ephemeral=%d, want manual/0", origin, isEphemeral)
	}
}

func TestSchemaPostgresFreshAndReplay(t *testing.T) {
	dsn := testutil.PostgresDSNFromEnv(t)
	db := testutil.OpenIsolatedPostgres(t, dsn)
	seedCoreAnalyticsFixture(t, db)

	newTestRepository(t, db)
	newTestRepository(t, db)

	for _, view := range mirrorViews {
		var count int
		if err := db.Get(&count, "SELECT COUNT(*) FROM "+view.name); err != nil {
			t.Fatalf("query %s on postgres after replay: %v", view.name, err)
		}
	}
}
