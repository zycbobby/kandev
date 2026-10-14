package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"expvar"

	"github.com/kandev/kandev/internal/task/models"
	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/kandev/kandev/internal/worktree"
)

// graceReclaimWorktreeFake extends the shared archive-reclaim fake with the
// discard-capable reclamation surface and an inspection failure mode, without
// editing the upstream fake definition.
type graceReclaimWorktreeFake struct {
	archiveReclaimWorktreeFake
	inspectErr error
	discards   int
}

func (f *graceReclaimWorktreeFake) InspectDirtyWorktrees(
	ctx context.Context, worktrees []*worktree.Worktree,
) ([]worktree.DirtyWorktree, error) {
	if f.inspectErr != nil {
		return nil, f.inspectErr
	}
	return f.archiveReclaimWorktreeFake.InspectDirtyWorktrees(ctx, worktrees)
}

func (f *graceReclaimWorktreeFake) CleanupArchivedWorktreeDiscardingChanges(
	_ context.Context, wt *worktree.Worktree, taskID, _, _ string,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.discards++
	worktrees := f.worktrees[taskID]
	for i, current := range worktrees {
		if current != nil && current.ID == wt.ID {
			f.worktrees[taskID] = append(worktrees[:i], worktrees[i+1:]...)
			break
		}
	}
	return nil
}

func (f *graceReclaimWorktreeFake) discardCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.discards
}

func (f *graceReclaimWorktreeFake) worktreesFor(taskID string) []*worktree.Worktree {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*worktree.Worktree(nil), f.worktrees[taskID]...)
}

func TestArchiveReclaimForcesDirtyCheckoutAfterGracePeriod(t *testing.T) {
	ctx := context.Background()
	svc, _, repo, manager, sourcePath, wt := createArchiveReclaimFixture(t)
	svc.StopTaskResourceCleanupWorker()
	svc.SetArchiveReclaimDirtyGrace(72 * time.Hour)
	sentinel := filepath.Join(wt.Path, "untracked-grace.txt")
	if err := os.WriteFile(sentinel, []byte("discard after grace\n"), 0o600); err != nil {
		t.Fatalf("write dirty worktree marker: %v", err)
	}

	// Archive through the repository so the fixture skips the archive-time
	// source-manifest capture; the grace behavior under test lives entirely
	// in the reclaim pass.
	if err := repo.ArchiveTask(ctx, wt.TaskID); err != nil {
		t.Fatalf("archive task: %v", err)
	}
	reclaimJob := backdatedReclaimJob(t, repo, wt, 73*time.Hour)
	forceDiscardsBefore := archiveReclaimCounter(t, "force_discarded")
	if err := svc.processTaskResourceCleanupJob(ctx, reclaimJob.ID); err != nil {
		t.Fatalf("process expired-grace reclaim: %v", err)
	}
	got, err := repo.GetTaskResourceCleanupJob(ctx, reclaimJob.ID)
	if err != nil {
		t.Fatalf("reload reclaim job: %v", err)
	}
	if got.State != models.TaskResourceCleanupStateSucceeded {
		t.Fatalf("expired-grace reclaim state = %q (last error %q), want succeeded (forced)", got.State, got.LastError)
	}
	if _, err := os.Stat(wt.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dirty archived worktree survived the grace period: %v", err)
	}
	if _, err := os.Stat(sentinel); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uncommitted marker survived the forced reclaim: %v", err)
	}
	if persisted, getErr := manager.GetByID(ctx, wt.ID); getErr == nil && persisted != nil && persisted.Status == worktree.StatusActive {
		t.Fatalf("worktree row after forced reclaim = %+v, want not active", persisted)
	}
	if branches := strings.TrimSpace(string(runGitTestCmd(t, sourcePath, "branch", "--list", wt.Branch))); branches == "" {
		t.Fatal("forced reclaim removed the local branch; want branch-preserving policy")
	}
	if archiveReclaimCounter(t, "force_discarded") != forceDiscardsBefore+1 {
		t.Fatalf("archive_reclaim_total[force_discarded] did not advance past %d", forceDiscardsBefore)
	}
}

// backdatedReclaimJob archives-time backdating helper: it builds an
// archive_reclaim job for the worktree whose snapshot archive time is the
// task's archived time shifted back by age, rewriting the task row to match.
// The reclaim pass refuses a snapshot whose archive time no longer matches
// the task, so both sides move together.
func backdatedReclaimJob(
	t *testing.T, repo *sqliterepo.Repository, wt *worktree.Worktree, age time.Duration,
) *models.TaskResourceCleanupJob {
	t.Helper()
	ctx := context.Background()
	task, err := repo.GetTask(ctx, wt.TaskID)
	if err != nil || task == nil || task.ArchivedAt == nil {
		t.Fatalf("load archived task: task=%+v err=%v", task, err)
	}
	backdated := task.ArchivedAt.Add(-age).UTC().Format(time.RFC3339Nano)
	if _, err := repo.DB().ExecContext(ctx, `UPDATE tasks SET archived_at = ? WHERE id = ?`, backdated, wt.TaskID); err != nil {
		t.Fatalf("backdate archived task: %v", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, backdated)
	if err != nil {
		t.Fatalf("parse backdated archive time: %v", err)
	}
	snapshot, err := json.Marshal(archiveReclaimSnapshot{
		WorktreeID: wt.ID, WorktreePath: wt.Path, RepositoryPath: wt.RepositoryPath,
		ArchivedAt: parsed,
	})
	if err != nil {
		t.Fatalf("encode reclaim snapshot: %v", err)
	}
	job := &models.TaskResourceCleanupJob{
		ID: "job-grace-" + wt.ID, OperationID: "archive_reclaim:" + wt.TaskID + ":" + wt.ID,
		TaskID: wt.TaskID, Trigger: models.TaskResourceCleanupTriggerArchiveReclaim,
		State: models.TaskResourceCleanupStatePending, ResourceSnapshot: string(snapshot),
	}
	if err := repo.CreateTaskResourceCleanupJob(ctx, job); err != nil {
		t.Fatalf("create reclaim job: %v", err)
	}
	return job
}

// @covers AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.2
func TestArchiveReclaimZeroGraceKeepsRetainUntilClean(t *testing.T) {
	ctx := context.Background()
	svc, _, repo, _, _, wt := createArchiveReclaimFixture(t)
	svc.StopTaskResourceCleanupWorker()
	svc.SetArchiveReclaimDirtyGrace(0)
	sentinel := filepath.Join(wt.Path, "untracked-zero-grace.txt")
	if err := os.WriteFile(sentinel, []byte("keep until clean\n"), 0o600); err != nil {
		t.Fatalf("write dirty worktree marker: %v", err)
	}

	if err := repo.ArchiveTask(ctx, wt.TaskID); err != nil {
		t.Fatalf("archive task: %v", err)
	}
	reclaimJob := backdatedReclaimJob(t, repo, wt, 30*24*time.Hour)
	if err := svc.processTaskResourceCleanupJob(ctx, reclaimJob.ID); err != nil {
		t.Fatalf("process zero-grace reclaim: %v", err)
	}
	got, err := repo.GetTaskResourceCleanupJob(ctx, reclaimJob.ID)
	if err != nil {
		t.Fatalf("reload reclaim job: %v", err)
	}
	if got.State != models.TaskResourceCleanupStateWaitingForClean {
		t.Fatalf("zero-grace reclaim state = %q, want waiting_for_clean", got.State)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("uncommitted marker removed despite disabled grace: %v", err)
	}
}

// @covers AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.3
func TestArchiveReclaimInUseVetoOutranksGracePeriod(t *testing.T) {
	ctx := context.Background()
	svc, _, repo := createTestService(t)
	svc.StopTaskResourceCleanupWorker()
	svc.SetArchiveReclaimDirtyGrace(72 * time.Hour)
	const taskID = "task-grace-in-use-veto"
	seedCleanupTaskAndSession(t, repo, taskID, "session-grace-in-use-veto")
	if err := repo.ArchiveTask(ctx, taskID); err != nil {
		t.Fatalf("archive task: %v", err)
	}
	wt := &worktree.Worktree{
		ID: "wt-grace-in-use-veto", TaskID: taskID, RepositoryID: "repo-grace-in-use-veto",
		RepositoryPath: "/tmp/grace-veto-repo", Path: "/tmp/grace-veto-repo/worktree", Status: worktree.StatusActive,
	}
	if err := repo.CreateRepository(ctx, &models.Repository{
		ID: wt.RepositoryID, WorkspaceID: "ws-" + taskID, Name: wt.RepositoryID, LocalPath: wt.RepositoryPath,
	}); err != nil {
		t.Fatalf("create repository: %v", err)
	}
	if err := repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{
		ID: "env-" + taskID, TaskID: taskID, ExecutorType: "worktree", WorkspacePath: "/tmp/grace-veto-repo",
		Status: models.TaskEnvironmentStatusReady, Repos: []*models.TaskEnvironmentRepo{{
			ID: "env-repo-" + wt.ID, RepositoryID: wt.RepositoryID, WorktreeID: wt.ID,
			WorktreePath: wt.Path, Status: worktree.StatusActive,
		}},
	}); err != nil {
		t.Fatalf("create task environment: %v", err)
	}
	reclaimJob := backdatedReclaimJob(t, repo, wt, 73*time.Hour)

	fake := &graceReclaimWorktreeFake{
		archiveReclaimWorktreeFake: archiveReclaimWorktreeFake{
			worktrees: map[string][]*worktree.Worktree{taskID: {wt}},
			dirty:     true,
			refs:      1,
		},
	}
	svc.SetWorktreeCleanup(fake)
	if err := svc.processTaskResourceCleanupJob(ctx, reclaimJob.ID); err != nil {
		t.Fatalf("process in-use expired-grace reclaim: %v", err)
	}
	got, err := repo.GetTaskResourceCleanupJob(ctx, reclaimJob.ID)
	if err != nil {
		t.Fatalf("reload reclaim job: %v", err)
	}
	if got.State != models.TaskResourceCleanupStateWaitingForClean {
		t.Fatalf("in-use reclaim state = %q, want waiting_for_clean", got.State)
	}
	if fake.discardCount() != 0 {
		t.Fatalf("forced discard ran on a referenced worktree (%d times), want none", fake.discardCount())
	}
	if remain := fake.worktreesFor(taskID); len(remain) != 1 {
		t.Fatalf("referenced worktree removed despite in-use veto: %v", remain)
	}
}

// @covers AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.4
func TestArchiveReclaimInspectionFailureStaysFailClosedAfterGrace(t *testing.T) {
	ctx := context.Background()
	svc, _, repo := createTestService(t)
	svc.StopTaskResourceCleanupWorker()
	svc.SetArchiveReclaimDirtyGrace(72 * time.Hour)
	const taskID = "task-grace-inspect-failure"
	seedCleanupTaskAndSession(t, repo, taskID, "session-grace-inspect-failure")
	if err := repo.ArchiveTask(ctx, taskID); err != nil {
		t.Fatalf("archive task: %v", err)
	}
	wt := &worktree.Worktree{
		ID: "wt-grace-inspect-failure", TaskID: taskID, RepositoryID: "repo-grace-inspect-failure",
		RepositoryPath: "/tmp/grace-inspect-repo", Path: "/tmp/grace-inspect-repo/worktree", Status: worktree.StatusActive,
	}
	if err := repo.CreateRepository(ctx, &models.Repository{
		ID: wt.RepositoryID, WorkspaceID: "ws-" + taskID, Name: wt.RepositoryID, LocalPath: wt.RepositoryPath,
	}); err != nil {
		t.Fatalf("create repository: %v", err)
	}
	if err := repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{
		ID: "env-" + taskID, TaskID: taskID, ExecutorType: "worktree", WorkspacePath: "/tmp/grace-inspect-repo",
		Status: models.TaskEnvironmentStatusReady, Repos: []*models.TaskEnvironmentRepo{{
			ID: "env-repo-" + wt.ID, RepositoryID: wt.RepositoryID, WorktreeID: wt.ID,
			WorktreePath: wt.Path, Status: worktree.StatusActive,
		}},
	}); err != nil {
		t.Fatalf("create task environment: %v", err)
	}
	reclaimJob := backdatedReclaimJob(t, repo, wt, 73*time.Hour)

	fake := &graceReclaimWorktreeFake{
		archiveReclaimWorktreeFake: archiveReclaimWorktreeFake{
			worktrees: map[string][]*worktree.Worktree{taskID: {wt}},
		},
		inspectErr: errors.New("git status exploded"),
	}
	svc.SetWorktreeCleanup(fake)
	// The processor surfaces the inspection error after scheduling the retry;
	// the fail-closed contract is about what it must NOT do (discard).
	_ = svc.processTaskResourceCleanupJob(ctx, reclaimJob.ID)
	got, err := repo.GetTaskResourceCleanupJob(ctx, reclaimJob.ID)
	if err != nil {
		t.Fatalf("reload reclaim job: %v", err)
	}
	if got.State == models.TaskResourceCleanupStateSucceeded {
		t.Fatal("failed inspection produced a successful reclaim; want fail-closed retry")
	}
	if fake.discardCount() != 0 {
		t.Fatalf("forced discard ran on an uninspectable worktree (%d times), want none", fake.discardCount())
	}
	if remain := fake.worktreesFor(taskID); len(remain) != 1 {
		t.Fatalf("uninspectable worktree removed despite fail-closed guard: %v", remain)
	}
}

// @covers AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.5
func TestArchiveReclaimFailureAfterRetryBudgetRecordsFailedOutcome(t *testing.T) {
	ctx := context.Background()
	svc, _, repo := createTestService(t)
	svc.StopTaskResourceCleanupWorker()
	svc.SetArchiveReclaimDirtyGrace(72 * time.Hour)
	const taskID = "task-grace-failed-outcome"
	seedCleanupTaskAndSession(t, repo, taskID, "session-grace-failed-outcome")
	if err := repo.ArchiveTask(ctx, taskID); err != nil {
		t.Fatalf("archive task: %v", err)
	}
	wt := &worktree.Worktree{
		ID: "wt-grace-failed-outcome", TaskID: taskID, RepositoryID: "repo-grace-failed-outcome",
		RepositoryPath: "/tmp/grace-failed-repo", Path: "/tmp/grace-failed-repo/worktree", Status: worktree.StatusActive,
	}
	if err := repo.CreateRepository(ctx, &models.Repository{
		ID: wt.RepositoryID, WorkspaceID: "ws-" + taskID, Name: wt.RepositoryID, LocalPath: wt.RepositoryPath,
	}); err != nil {
		t.Fatalf("create repository: %v", err)
	}
	if err := repo.CreateTaskEnvironment(ctx, &models.TaskEnvironment{
		ID: "env-" + taskID, TaskID: taskID, ExecutorType: "worktree", WorkspacePath: "/tmp/grace-failed-repo",
		Status: models.TaskEnvironmentStatusReady, Repos: []*models.TaskEnvironmentRepo{{
			ID: "env-repo-" + wt.ID, RepositoryID: wt.RepositoryID, WorktreeID: wt.ID,
			WorktreePath: wt.Path, Status: worktree.StatusActive,
		}},
	}); err != nil {
		t.Fatalf("create task environment: %v", err)
	}
	reclaimJob := backdatedReclaimJob(t, repo, wt, 73*time.Hour)
	// The claim increments attempts, so 7 reaches the eight-attempt budget.
	reclaimJob.Attempts = 7
	failedBefore := archiveReclaimCounter(t, "failed")
	if _, err := repo.DB().ExecContext(ctx,
		`UPDATE task_resource_cleanup_jobs SET attempts = 7 WHERE id = ?`, reclaimJob.ID); err != nil {
		t.Fatalf("seed retry budget: %v", err)
	}

	fake := &graceReclaimWorktreeFake{
		archiveReclaimWorktreeFake: archiveReclaimWorktreeFake{
			worktrees: map[string][]*worktree.Worktree{taskID: {wt}},
		},
		inspectErr: errors.New("git status exploded"),
	}
	svc.SetWorktreeCleanup(fake)
	_ = svc.processTaskResourceCleanupJob(ctx, reclaimJob.ID)
	got, err := repo.GetTaskResourceCleanupJob(ctx, reclaimJob.ID)
	if err != nil {
		t.Fatalf("reload reclaim job: %v", err)
	}
	if got.State != models.TaskResourceCleanupStateFailed {
		t.Fatalf("exhausted reclaim state = %q, want failed", got.State)
	}
	if fake.discardCount() != 0 {
		t.Fatalf("forced discard ran despite failed inspection (%d times), want none", fake.discardCount())
	}
	if archiveReclaimCounter(t, "failed") != failedBefore+1 {
		t.Fatalf("archive_reclaim_total[failed] did not advance past %d", failedBefore)
	}
}

func archiveReclaimCounter(t *testing.T, outcome string) int64 {
	t.Helper()
	value := archiveReclaimCounters.Get(outcome)
	if value == nil {
		return 0
	}
	intValue, ok := value.(*expvar.Int)
	if !ok {
		t.Fatalf("archive_reclaim_total[%s] is %T, want *expvar.Int", outcome, value)
	}
	return intValue.Value()
}
