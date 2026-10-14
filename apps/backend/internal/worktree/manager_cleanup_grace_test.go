package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
)

// The discard variant keeps the archived-cleanup identity gate: a stale
// owner must never authorize removing a checkout, whatever its cleanliness.
// The happy path (a dirty checkout actually removed) is covered at the
// service boundary in internal/task/service, where the store-backed worktree
// records carry repository paths.
//
// @covers AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.1
func TestCleanupArchivedWorktreeDiscardingChangesStillValidatesOwner(t *testing.T) {
	ctx := context.Background()
	mgr, store := newReferenceCleanupTestManager(t)
	seedReferenceCleanupSession(t, store, "task-archive-discard-owner", "session-archive-discard-owner", models.TaskSessionStateCompleted)
	wt := createReferenceCleanupWorktree(t, mgr, "task-archive-discard-owner", "session-archive-discard-owner")

	if err := os.WriteFile(filepath.Join(wt.Path, "untracked-owner.txt"), []byte("keep\n"), 0o600); err != nil {
		t.Fatalf("write untracked dirty marker: %v", err)
	}

	err := mgr.CleanupArchivedWorktreeDiscardingChanges(ctx, wt, "task-transferred", wt.Path, wt.RepositoryPath)
	if !errors.Is(err, ErrArchivedWorktreeIdentityChanged) {
		t.Fatalf("CleanupArchivedWorktreeDiscardingChanges error = %v, want identity-changed", err)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("worktree directory was removed after owner mismatch: %v", err)
	}
	assertWorktreeReferenceStatus(t, store, wt.ID, StatusActive)
}
