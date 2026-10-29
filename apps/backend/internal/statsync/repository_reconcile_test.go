package statsync

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

// withInventoryChunkSize injects a small page/delete-batch size so a handful of
// rows exercises the multi-page and multi-batch paths. Tests using it must not
// run in parallel: inventoryChunkSize is package state.
func withInventoryChunkSize(t *testing.T, size int) {
	t.Helper()
	original := inventoryChunkSize
	inventoryChunkSize = size
	t.Cleanup(func() { inventoryChunkSize = original })
}

// inventoryFromEnvelope lists every uid the envelope carries, the shape a
// reconcile push sends while it still holds all of them.
func inventoryFromEnvelope(env *Envelope) *Inventory {
	inv := &Inventory{}
	for _, row := range env.Repositories {
		inv.Repositories = append(inv.Repositories, row.UID)
	}
	for _, row := range env.Tasks {
		inv.Tasks = append(inv.Tasks, row.UID)
	}
	for _, row := range env.TaskRepositories {
		inv.TaskRepositories = append(inv.TaskRepositories, row.UID)
	}
	for _, row := range env.Sessions {
		inv.Sessions = append(inv.Sessions, row.UID)
	}
	for _, row := range env.Turns {
		inv.Turns = append(inv.Turns, row.UID)
	}
	for _, row := range env.Messages {
		inv.Messages = append(inv.Messages, row.UID)
	}
	for _, row := range env.Commits {
		inv.Commits = append(inv.Commits, row.UID)
	}
	for _, row := range env.GitSnapshots {
		inv.GitSnapshots = append(inv.GitSnapshots, row.UID)
	}
	return inv
}

func TestApplyReconcileLargeInventoryExceedsStatementBudget(t *testing.T) {
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
	// One row the inventory will omit, modelling a remote deletion.
	orphan := &Envelope{SchemaVersion: SchemaVersion, SourceInstance: "remote-1", Mode: ModeDelta}
	orphan.Messages = []MessageRow{{
		UID: "remote-1:msg-orphan", SessionUID: "remote-1:sess-1",
		AuthorType: "user", Type: "text", CreatedAt: "2026-10-11T09:05:02Z",
	}}
	if _, err := repo.ApplyDelta(ctx, orphan); err != nil {
		t.Fatalf("apply orphan payload: %v", err)
	}

	inv := inventoryFromEnvelope(env)
	for i := 0; i < 32766; i++ {
		inv.Messages = append(inv.Messages, fmt.Sprintf("remote-1:bulk-%d", i))
	}
	reconcile := &Envelope{SchemaVersion: SchemaVersion, SourceInstance: "remote-1",
		Mode: ModeReconcile, Inventory: inv}
	result, err := repo.ApplyReconcile(ctx, reconcile)
	if err != nil {
		t.Fatalf("ApplyReconcile with %d message uids: %v", len(inv.Messages), err)
	}
	if got := countRows(t, db, "statsync_messages"); got != 1 {
		t.Fatalf("messages after reconcile = %d, want 1 (inventory row kept, orphan deleted)", got)
	}
	if result.Deleted != 1 {
		t.Fatalf("Deleted = %d, want 1 (only the orphan)", result.Deleted)
	}
}

func TestApplyReconcileKeepsInventoryAcrossPages(t *testing.T) {
	withInventoryChunkSize(t, 2)
	db := testDB(t)
	repo := newTestRepository(t, db)
	ctx := context.Background()

	env := validEnvelope()
	for i := range env.Tasks {
		env.Tasks[i].ResolvedWorkspaceID = "ws-local"
	}
	for i := 2; i <= 8; i++ {
		env.Messages = append(env.Messages, MessageRow{
			UID: fmt.Sprintf("remote-1:msg-%d", i), SessionUID: "remote-1:sess-1",
			TurnUID: "remote-1:turn-1", AuthorType: "user", Type: "text",
			CreatedAt: "2026-10-11T09:05:01Z",
		})
	}
	if _, err := repo.ApplyDelta(ctx, env); err != nil {
		t.Fatalf("apply base payload: %v", err)
	}

	inv := inventoryFromEnvelope(env)
	// msg-8 is the only remote deletion; it sits on a page boundary, so a
	// cursor that failed to advance would miss it or revisit earlier pages.
	inv.Messages = inv.Messages[:len(inv.Messages)-1]
	reconcile := &Envelope{SchemaVersion: SchemaVersion, SourceInstance: "remote-1",
		Mode: ModeReconcile, Inventory: inv}
	if _, err := repo.ApplyReconcile(ctx, reconcile); err != nil {
		t.Fatalf("apply reconcile: %v", err)
	}

	var remaining []string
	if err := db.Select(&remaining, "SELECT uid FROM statsync_messages ORDER BY uid"); err != nil {
		t.Fatalf("read remaining messages: %v", err)
	}
	want := make([]string, 0, 7)
	for i := 1; i <= 7; i++ {
		want = append(want, fmt.Sprintf("remote-1:msg-%d", i))
	}
	if !reflect.DeepEqual(remaining, want) {
		t.Fatalf("remaining messages = %v, want %v", remaining, want)
	}
}

func TestApplyReconcileEmptyCategoryDeletesWholeCategory(t *testing.T) {
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

	inv := inventoryFromEnvelope(env)
	inv.Messages = nil
	reconcile := &Envelope{SchemaVersion: SchemaVersion, SourceInstance: "remote-1",
		Mode: ModeReconcile, Inventory: inv}
	if _, err := repo.ApplyReconcile(ctx, reconcile); err != nil {
		t.Fatalf("apply reconcile: %v", err)
	}

	if got := countRows(t, db, "statsync_messages"); got != 0 {
		t.Fatalf("messages after empty-category reconcile = %d, want 0", got)
	}
	for _, table := range []string{
		"statsync_tasks", "statsync_sessions", "statsync_turns", "statsync_commits",
	} {
		if got := countRows(t, db, table); got != 1 {
			t.Fatalf("%s has %d rows, want 1 (an empty messages inventory must not touch other categories)", table, got)
		}
	}
}

func TestApplyReconcileDeletesOutsideInventoryPerCategory(t *testing.T) {
	db := testDB(t)
	repo := newTestRepository(t, db)
	ctx := context.Background()

	env := validEnvelope()
	for i := range env.Tasks {
		env.Tasks[i].ResolvedWorkspaceID = "ws-local"
	}
	env.Turns = append(env.Turns, TurnRow{
		UID: "remote-1:turn-2", SessionUID: "remote-1:sess-1", StartedAt: "2026-10-11T09:06:00Z",
	})
	env.Messages = append(env.Messages, MessageRow{
		UID: "remote-1:msg-2", SessionUID: "remote-1:sess-1", TurnUID: "remote-1:turn-2",
		AuthorType: "assistant", Type: "text", CreatedAt: "2026-10-11T09:06:01Z",
	})
	env.Commits = append(env.Commits, CommitRow{
		UID: "remote-1:commit-2", SessionUID: "remote-1:sess-1", CommitSHA: "def456",
		CommittedAt: "2026-10-11T10:05:00Z",
	})
	if _, err := repo.ApplyDelta(ctx, env); err != nil {
		t.Fatalf("apply base payload: %v", err)
	}

	// The inventory still lists the original rows only; one uid is listed
	// twice to pin duplicate tolerance.
	inv := inventoryFromEnvelope(validEnvelope())
	inv.Messages = append(inv.Messages, "remote-1:msg-1")
	reconcile := &Envelope{SchemaVersion: SchemaVersion, SourceInstance: "remote-1",
		Mode: ModeReconcile, Inventory: inv}
	result, err := repo.ApplyReconcile(ctx, reconcile)
	if err != nil {
		t.Fatalf("apply reconcile: %v", err)
	}

	for _, table := range []string{"statsync_turns", "statsync_messages", "statsync_commits"} {
		if got := countRows(t, db, table); got != 1 {
			t.Fatalf("%s has %d rows after reconcile, want 1 (the second row was outside the inventory)", table, got)
		}
	}
	for _, table := range []string{"statsync_tasks", "statsync_sessions"} {
		if got := countRows(t, db, table); got != 1 {
			t.Fatalf("%s has %d rows, want 1 (parent inventory kept)", table, got)
		}
	}
	if result.Deleted != 3 {
		t.Fatalf("Deleted = %d, want 3 (one row per child category, cascades excluded)", result.Deleted)
	}
}

func TestApplyReconcileLeavesOtherSourcesIntact(t *testing.T) {
	db := testDB(t)
	repo := newTestRepository(t, db)
	ctx := context.Background()

	env := validEnvelope()
	for i := range env.Tasks {
		env.Tasks[i].ResolvedWorkspaceID = "ws-local"
	}
	if _, err := repo.ApplyDelta(ctx, env); err != nil {
		t.Fatalf("apply remote-1 payload: %v", err)
	}
	other := &Envelope{
		SchemaVersion: SchemaVersion, SourceInstance: "remote-2", Mode: ModeDelta,
		Tasks: []TaskRow{{
			UID: "remote-2:task-1", OrigWorkspaceID: "ws-remote", Title: "Other source",
			CreatedAt: "2026-10-11T09:00:00Z", UpdatedAt: "2026-10-11T09:00:00Z",
			ResolvedWorkspaceID: "ws-local",
		}},
		Sessions: []SessionRow{{
			UID: "remote-2:sess-1", TaskUID: "remote-2:task-1", StartedAt: "2026-10-11T09:05:00Z",
		}},
		Messages: []MessageRow{{
			UID: "remote-2:msg-1", SessionUID: "remote-2:sess-1",
			AuthorType: "user", Type: "text", CreatedAt: "2026-10-11T09:05:01Z",
		}},
	}
	if _, err := repo.ApplyDelta(ctx, other); err != nil {
		t.Fatalf("apply remote-2 payload: %v", err)
	}

	inv := inventoryFromEnvelope(env)
	inv.Messages = nil // the reconcile drops remote-1's only message
	reconcile := &Envelope{SchemaVersion: SchemaVersion, SourceInstance: "remote-1",
		Mode: ModeReconcile, Inventory: inv}
	if _, err := repo.ApplyReconcile(ctx, reconcile); err != nil {
		t.Fatalf("apply reconcile: %v", err)
	}

	var remaining []string
	if err := db.Select(&remaining, "SELECT uid FROM statsync_messages ORDER BY uid"); err != nil {
		t.Fatalf("read remaining messages: %v", err)
	}
	if want := []string{"remote-2:msg-1"}; !reflect.DeepEqual(remaining, want) {
		t.Fatalf("remaining messages = %v, want %v", remaining, want)
	}
	for _, table := range []string{"statsync_tasks", "statsync_sessions"} {
		if got := countRows(t, db, table); got != 2 {
			t.Fatalf("%s has %d rows, want 2 (remote-1 and remote-2 rows)", table, got)
		}
	}
}
