package statsync

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
)

func countRows(t *testing.T, db *sqlx.DB, table string) int {
	t.Helper()
	var count int
	if err := db.Get(&count, "SELECT COUNT(*) FROM "+table); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func TestApplyDeltaIsIdempotent(t *testing.T) {
	db := testDB(t)
	repo := newTestRepository(t, db)
	ctx := context.Background()

	env := validEnvelope()
	for i := range env.Tasks {
		env.Tasks[i].ResolvedWorkspaceID = "ws-local"
	}
	first, err := repo.ApplyDelta(ctx, env)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	second, err := repo.ApplyDelta(ctx, env)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}

	wantCounts := map[string]int{
		"statsync_repositories":      len(env.Repositories),
		"statsync_tasks":             len(env.Tasks),
		"statsync_task_repositories": len(env.TaskRepositories),
		"statsync_sessions":          len(env.Sessions),
		"statsync_turns":             len(env.Turns),
		"statsync_messages":          len(env.Messages),
		"statsync_commits":           len(env.Commits),
	}
	for table, want := range wantCounts {
		if got := countRows(t, db, table); got != want {
			t.Fatalf("%s has %d rows after duplicate push, want %d", table, got, want)
		}
	}
	if first.Upserts != second.Upserts {
		t.Fatalf("upsert counts differ: first=%d second=%d", first.Upserts, second.Upserts)
	}

	// A re-push with changed column values updates the stored row in place.
	updatedTitle := "Renamed remote task"
	env.Tasks[0].Title = updatedTitle
	if _, err := repo.ApplyDelta(ctx, env); err != nil {
		t.Fatalf("update apply: %v", err)
	}
	var title string
	if err := db.Get(&title, `SELECT title FROM statsync_tasks WHERE uid = 'remote-1:task-1'`); err != nil {
		t.Fatalf("read updated title: %v", err)
	}
	if title != updatedTitle {
		t.Fatalf("title = %q, want %q", title, updatedTitle)
	}
}

func TestApplyReconcileDeletesOutsideInventoryWithCascade(t *testing.T) {
	db := testDB(t)
	repo := newTestRepository(t, db)
	ctx := context.Background()

	env := validEnvelope()
	for i := range env.Tasks {
		env.Tasks[i].ResolvedWorkspaceID = "ws-local"
	}
	if _, err := repo.ApplyDelta(ctx, env); err != nil {
		t.Fatalf("apply base payload: %v", err)
	}
	// A second task with its own session, which the reconcile inventory will
	// omit to model a remote deletion.
	orphanEnv := validEnvelope()
	orphanEnv.Tasks = []TaskRow{{
		UID:                 "remote-1:task-2",
		OrigWorkspaceID:     "ws-remote",
		Title:               "Doomed remote task",
		CreatedAt:           "2026-10-11T09:00:00Z",
		UpdatedAt:           "2026-10-11T09:00:00Z",
		ResolvedWorkspaceID: "ws-local",
	}}
	orphanEnv.Sessions = []SessionRow{{
		UID:       "remote-1:sess-2",
		TaskUID:   "remote-1:task-2",
		StartedAt: "2026-10-11T09:05:00Z",
	}}
	orphanEnv.Turns = nil
	orphanEnv.Messages = nil
	orphanEnv.Commits = nil
	orphanEnv.TaskRepositories = nil
	if _, err := repo.ApplyDelta(ctx, orphanEnv); err != nil {
		t.Fatalf("apply orphan payload: %v", err)
	}
	if got := countRows(t, db, "statsync_tasks"); got != 2 {
		t.Fatalf("tasks before reconcile = %d, want 2", got)
	}

	inventory := &Inventory{
		Repositories:     []string{"remote-1:repo-1"},
		Tasks:            []string{"remote-1:task-1"},
		TaskRepositories: []string{"remote-1:tr-1"},
		Sessions:         []string{"remote-1:sess-1"},
		Turns:            []string{"remote-1:turn-1"},
		Messages:         []string{"remote-1:msg-1"},
		Commits:          []string{"remote-1:commit-1"},
	}
	reconcileEnv := validEnvelope()
	reconcileEnv.Mode = ModeReconcile
	reconcileEnv.Inventory = inventory
	result, err := repo.ApplyReconcile(ctx, reconcileEnv)
	if err != nil {
		t.Fatalf("apply reconcile: %v", err)
	}
	if result.Deleted == 0 {
		t.Fatal("reconcile deleted nothing, want the orphan cascade")
	}

	if got := countRows(t, db, "statsync_tasks"); got != 1 {
		t.Fatalf("tasks after reconcile = %d, want 1 (inventory task only)", got)
	}
	// The orphan task's session is gone through the mirror FK cascade even
	// though the sessions inventory listed nothing about it.
	if got := countRows(t, db, "statsync_sessions"); got != 1 {
		t.Fatalf("sessions after reconcile = %d, want 1 (cascade removed the orphan's session)", got)
	}
	var remaining string
	if err := db.Get(&remaining, `SELECT uid FROM statsync_tasks`); err != nil {
		t.Fatalf("read remaining task: %v", err)
	}
	if remaining != "remote-1:task-1" {
		t.Fatalf("remaining task = %q, want remote-1:task-1", remaining)
	}
}

func TestApplyReconcileEmptyInventoryClearsSource(t *testing.T) {
	db := testDB(t)
	repo := newTestRepository(t, db)
	ctx := context.Background()

	env := validEnvelope()
	for i := range env.Tasks {
		env.Tasks[i].ResolvedWorkspaceID = "ws-local"
	}
	if _, err := repo.ApplyDelta(ctx, env); err != nil {
		t.Fatalf("apply delta: %v", err)
	}

	env.Mode = ModeReconcile
	env.Inventory = &Inventory{}
	if _, err := repo.ApplyReconcile(ctx, env); err != nil {
		t.Fatalf("apply reconcile: %v", err)
	}
	for _, table := range []string{
		"statsync_repositories", "statsync_tasks", "statsync_task_repositories",
		"statsync_sessions", "statsync_turns", "statsync_messages", "statsync_commits",
	} {
		if got := countRows(t, db, table); got != 0 {
			t.Fatalf("%s has %d rows after empty-inventory reconcile, want 0", table, got)
		}
	}
}
