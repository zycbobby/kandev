package api

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/agentctl/server/process"
	"github.com/kandev/kandev/internal/task/models"
)

// creationBaseAPIFixture extends the git API fixture with the "pushed 39"
// shape: three unpushed commits on local main, a work branch cut from that
// tip with one commit of its own.
type creationBaseAPIFixture struct {
	*gitAPIFixture
	forkSHA string
}

func newCreationBaseAPIFixture(t *testing.T) *creationBaseAPIFixture {
	t.Helper()
	fixture := newGitAPIFixture(t)
	// The base fixture leaves the work repo on feature/work; move back to
	// main, add the mother's own unpushed commits, then cut the work branch.
	runGitAPI(t, fixture.repo, "checkout", "main")
	for i := 1; i <= 3; i++ {
		writeFileAPI(t, fixture.repo, fmt.Sprintf("mother-%d.txt", i), "mother\n")
		runGitAPI(t, fixture.repo, "add", ".")
		runGitAPI(t, fixture.repo, "commit", "-m", fmt.Sprintf("mother %d", i))
	}
	forkSHA := strings.TrimSpace(runGitAPI(t, fixture.repo, "rev-parse", "main"))
	runGitAPI(t, fixture.repo, "checkout", "-b", "feature/creation-base")
	writeFileAPI(t, fixture.repo, "own-work.txt", "own\n")
	runGitAPI(t, fixture.repo, "add", ".")
	runGitAPI(t, fixture.repo, "commit", "-m", "own work")
	return &creationBaseAPIFixture{gitAPIFixture: fixture, forkSHA: forkSHA}
}

func (f *creationBaseAPIFixture) installCreationBase(t *testing.T, forkSHA string) {
	t.Helper()
	base := models.CreationBase{Version: models.CreationBaseVersion, Ref: "main", ForkSHA: forkSHA}
	f.server.procMgr.GetWorkspaceTracker().SetCreationBase(&base)
}

func (f *creationBaseAPIFixture) cumulativeDiff(ctx context.Context) (*process.CumulativeDiffResult, error) {
	gitOp, err := f.server.procMgr.GitOperatorFor("")
	if err != nil {
		return nil, err
	}
	comparison, err := f.server.comparisonResolutionFor("")
	if err != nil {
		return nil, err
	}
	result, _, err := f.server.runGitCumulativeDiffForRepoResolved(ctx, gitOp, "", "main", "", comparison)
	return result, err
}

// TestGitLogCreationBaseShowsOnlyWorktreeCommits pins the commits-panel half
// of the reversal: with a creation base the list contains only this
// worktree's commit; without one the origin-first default mixes in the
// mother branch's own unpushed commits.
func TestGitLogCreationBaseShowsOnlyWorktreeCommits(t *testing.T) {
	f := newCreationBaseAPIFixture(t)
	ctx := context.Background()

	legacy, err := f.server.runGitLogForRepo(ctx, GitLogRequest{TargetBranch: "main"}, 100, "")
	if err != nil {
		t.Fatalf("legacy log: %v", err)
	}
	if len(legacy.Commits) != 4 {
		t.Fatalf("legacy commits = %d, want 4 (3 mother + 1 own)", len(legacy.Commits))
	}

	f.installCreationBase(t, f.forkSHA)
	withBase, err := f.server.runGitLogForRepo(ctx, GitLogRequest{TargetBranch: "main"}, 100, "")
	if err != nil {
		t.Fatalf("creation-base log: %v", err)
	}
	if len(withBase.Commits) != 1 || withBase.Commits[0].CommitMessage != "own work" {
		t.Fatalf("creation-base commits = %#v, want only the worktree's own commit", withBase.Commits)
	}
}

// TestGitCumulativeDiffCreationBaseLimitsFilesToWorktreeWork pins the
// cumulative-diff half: the diff covers only the worktree's own file once
// the creation base is installed.
func TestGitCumulativeDiffCreationBaseLimitsFilesToWorktreeWork(t *testing.T) {
	f := newCreationBaseAPIFixture(t)
	ctx := context.Background()

	legacy, err := f.cumulativeDiff(ctx)
	if err != nil {
		t.Fatalf("legacy cumulative diff: %v", err)
	}
	if _, ok := legacy.Files["own-work.txt"]; !ok {
		t.Fatalf("legacy diff files = %v, want the own work file present", legacy.Files)
	}
	if _, ok := legacy.Files["mother-1.txt"]; !ok {
		t.Fatalf("legacy diff files = %v, want mother files present (origin-first default)", legacy.Files)
	}

	f.installCreationBase(t, f.forkSHA)
	withBase, err := f.cumulativeDiff(ctx)
	if err != nil {
		t.Fatalf("creation-base cumulative diff: %v", err)
	}
	if len(withBase.Files) != 1 {
		t.Fatalf("creation-base diff files = %v, want only the own work file", withBase.Files)
	}
	if _, ok := withBase.Files["own-work.txt"]; !ok {
		t.Fatalf("creation-base diff files = %v, want the own work file", withBase.Files)
	}
}

// TestGitCreationBaseUnavailableFailsClosed covers the bounded error code on
// both comparison entry points when the recorded fork point cannot resolve.
func TestGitCreationBaseUnavailableFailsClosed(t *testing.T) {
	f := newCreationBaseAPIFixture(t)
	runGitAPI(t, f.repo, "branch", "-D", "main")
	f.installCreationBase(t, strings.Repeat("d", 40))
	ctx := context.Background()

	log, err := f.server.runGitLogForRepo(ctx, GitLogRequest{TargetBranch: "main"}, 100, "")
	if err != nil {
		t.Fatalf("log with unavailable base: %v", err)
	}
	if log.Success || log.ErrorCode != "creation_base_ref_unavailable" {
		t.Fatalf("log result = (success=%v, code=%q), want unavailable creation base", log.Success, log.ErrorCode)
	}

	diff, err := f.cumulativeDiff(ctx)
	if err != nil {
		t.Fatalf("diff with unavailable base: %v", err)
	}
	if diff.Success || diff.ErrorCode != "creation_base_ref_unavailable" {
		t.Fatalf("diff result = (success=%v, code=%q), want unavailable creation base", diff.Success, diff.ErrorCode)
	}
}
