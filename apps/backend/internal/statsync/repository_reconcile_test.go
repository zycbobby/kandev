package statsync

import (
	"context"
	"fmt"
	"testing"
)

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
