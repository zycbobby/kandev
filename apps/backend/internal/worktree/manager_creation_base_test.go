package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func advanceMainForCreationBase(t *testing.T, repoPath, label string) string {
	t.Helper()
	second := filepath.Join(repoPath, "SECOND.md")
	if err := os.WriteFile(second, []byte("second "+label+"\n"), 0o644); err != nil {
		t.Fatalf("failed to write SECOND.md: %v", err)
	}
	runGit(t, repoPath, "add", "SECOND.md")
	runGit(t, repoPath, "commit", "-m", "second commit "+label)
	return strings.TrimSpace(runGit(t, repoPath, "rev-parse", "main"))
}

// TestCreateWorktree_RecordsCreationBaseOnNewBranch covers the fork-point
// capture: a newly cut worktree branch carries the ref it was cut from and
// that ref's tip at cut time.
func TestCreateWorktree_RecordsCreationBaseOnNewBranch(t *testing.T) {
	cfg := newTestConfig(t)
	mgr, err := NewManager(cfg, newMockStore(), newTestLogger())
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	repoPath := initGitRepoForWorktreeTest(t)
	mainTip := advanceMainForCreationBase(t, repoPath, "first")

	wt, err := mgr.Create(context.Background(), CreateRequest{
		TaskID:         "task-creation-base",
		SessionID:      "session-creation-base",
		RepositoryID:   "repo-1",
		RepositoryPath: repoPath,
		BaseBranch:     "main",
		TaskDirName:    "task-creation-base",
		RepoName:       "repo-1",
	})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if wt.CreationBaseRef != "main" {
		t.Fatalf("CreationBaseRef = %q, want %q", wt.CreationBaseRef, "main")
	}
	if wt.CreationForkSHA != mainTip {
		t.Fatalf("CreationForkSHA = %q, want main tip %q", wt.CreationForkSHA, mainTip)
	}
}

// TestCreateWorktree_ReuseDoesNotReRecordCreationBase pins the reuse
// contract: reuse never re-observes the fork point. Advancing the mother
// branch between the two Create calls must not move the recorded anchor —
// the reused record keeps the original values (same-process cache) or is
// empty (store reload), never the new tip.
func TestCreateWorktree_ReuseDoesNotReRecordCreationBase(t *testing.T) {
	cfg := newTestConfig(t)
	mgr, err := NewManager(cfg, newMockStore(), newTestLogger())
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	repoPath := initGitRepoForWorktreeTest(t)
	recordedTip := advanceMainForCreationBase(t, repoPath, "first")

	req := CreateRequest{
		TaskID:         "task-creation-base-reuse",
		SessionID:      "session-creation-base-reuse",
		RepositoryID:   "repo-1",
		RepositoryPath: repoPath,
		BaseBranch:     "main",
		TaskDirName:    "task-creation-base-reuse",
		RepoName:       "repo-1",
	}
	wt, err := mgr.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if wt.CreationBaseRef != "main" || wt.CreationForkSHA != recordedTip {
		t.Fatalf("first Create() creation base = %q/%q, want main/%q", wt.CreationBaseRef, wt.CreationForkSHA, recordedTip)
	}

	advancedTip := advanceMainForCreationBase(t, repoPath, "advanced")
	if advancedTip == recordedTip {
		t.Fatal("advancing main did not move the tip; test cannot detect re-recording")
	}

	reused, err := mgr.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("second Create() unexpected error: %v", err)
	}
	if reused.ID != wt.ID {
		t.Fatalf("second Create() returned worktree %q, want reused %q", reused.ID, wt.ID)
	}
	if reused.CreationBaseRef != "" && reused.CreationBaseRef != "main" {
		t.Fatalf("reuse path changed creation base ref: %q", reused.CreationBaseRef)
	}
	if reused.CreationForkSHA != "" && reused.CreationForkSHA != recordedTip {
		t.Fatalf("reuse path re-recorded fork point %q, want the recorded %q or empty", reused.CreationForkSHA, recordedTip)
	}
}
