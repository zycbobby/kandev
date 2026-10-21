package service

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
)

func creationBaseForServiceTest(ref string) models.CreationBase {
	return models.CreationBase{Version: models.CreationBaseVersion, Ref: ref, ForkSHA: strings.Repeat("a", 40)}
}

func createTaskWithComparisonWorkspace(t *testing.T, svc *Service, title string) string {
	t.Helper()
	taskResult, err := svc.CreateTask(context.Background(), &CreateTaskRequest{
		WorkspaceID: "ws-comparison", WorkflowID: "wf-comparison", WorkflowStepID: "step-comparison", Title: title,
		Repositories: []TaskRepositoryInput{{RepositoryID: "repo-comparison", BaseBranch: "main"}},
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return taskResult.Task.ID
}

// TestTaskCreationBasesAppliesPriority filters the effective creation-base map
// by the backend priority decision: a plain row contributes its creation base,
// while rows carrying an explicit comparison target or a manual base-branch
// override contribute nothing.
func TestTaskCreationBasesAppliesPriority(t *testing.T) {
	svc, _, repo := createTestService(t)
	ctx := context.Background()
	seedComparisonServiceWorkspace(t, repo, "contributor/widget", "head-42")
	plainID := createTaskWithComparisonWorkspace(t, svc, "Creation base plain")
	targetID := createTaskWithComparisonWorkspace(t, svc, "Creation base with target")
	manualID := createTaskWithComparisonWorkspace(t, svc, "Creation base manual")

	plainRow := singleTaskRepository(t, repo, plainID)
	base := creationBaseForServiceTest("main")
	if err := repo.UpdateTaskRepositoryCreationBase(ctx, plainRow.ID, &base); err != nil {
		t.Fatalf("persist creation base: %v", err)
	}

	targetRow := singleTaskRepository(t, repo, targetID)
	if err := repo.UpdateTaskRepositoryCreationBase(ctx, targetRow.ID, &base); err != nil {
		t.Fatalf("persist creation base: %v", err)
	}
	target, err := comparisonCandidate("contributor/widget", "head-42", "upstream/widget", "base-99").Build()
	if err != nil {
		t.Fatalf("Build comparison target: %v", err)
	}
	if _, _, err := repo.UpdateTaskRepositoryComparisonTarget(ctx, targetRow.ID, &target, nil, false); err != nil {
		t.Fatalf("attach comparison target: %v", err)
	}

	manualRow := singleTaskRepository(t, repo, manualID)
	if err := repo.UpdateTaskRepositoryCreationBase(ctx, manualRow.ID, &base); err != nil {
		t.Fatalf("persist creation base: %v", err)
	}
	if _, _, err := repo.UpdateTaskRepositoryBaseBranchAndClearComparisonTarget(ctx, manualRow.ID, "develop", true); err != nil {
		t.Fatalf("record manual selection: %v", err)
	}

	bases, err := svc.TaskCreationBases(ctx, plainID)
	if err != nil {
		t.Fatalf("TaskCreationBases (plain): %v", err)
	}
	// Single-repo tasks project both the tracker key and the root alias.
	if len(bases) != 2 || !bases[""].Equal(base) || !bases["widget"].Equal(base) {
		t.Fatalf("plain row bases = %v, want the creation base at both keys", bases)
	}

	bases, err = svc.TaskCreationBases(ctx, targetID)
	if err != nil {
		t.Fatalf("TaskCreationBases (target): %v", err)
	}
	if len(bases) != 0 {
		t.Fatalf("comparison-target row bases = %v, want empty (target wins)", bases)
	}

	bases, err = svc.TaskCreationBases(ctx, manualID)
	if err != nil {
		t.Fatalf("TaskCreationBases (manual): %v", err)
	}
	if len(bases) != 0 {
		t.Fatalf("manual-selection row bases = %v, want empty (manual wins)", bases)
	}
}

// TestTaskCreationBasesWithoutRecordIsEmpty pins the legacy contract: a task
// with no recorded creation base projects an empty map, not an error.
func TestTaskCreationBasesWithoutRecordIsEmpty(t *testing.T) {
	svc, _, repo := createTestService(t)
	ctx := context.Background()
	seedComparisonServiceWorkspace(t, repo, "contributor/widget", "head-42")
	taskID := createTaskWithComparisonWorkspace(t, svc, "Creation base absent")

	bases, err := svc.TaskCreationBases(ctx, taskID)
	if err != nil {
		t.Fatalf("TaskCreationBases: %v", err)
	}
	if len(bases) != 0 {
		t.Fatalf("bases = %v, want empty", bases)
	}
	if bases, err := svc.TaskCreationBases(ctx, ""); err != nil || len(bases) != 0 {
		t.Fatalf("TaskCreationBases(\"\") = %v, %v, want empty without error", bases, err)
	}
}

type recordingCreationBasePusher struct {
	mu    sync.Mutex
	calls []map[string]models.CreationBase
}

func (p *recordingCreationBasePusher) PushCreationBasesForTask(ctx context.Context, taskID string, bases map[string]models.CreationBase) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, bases)
}

// TestManualBaseBranchSelectionPushesCreationBaseWithoutTheRow covers the
// live-update contract: a manual picker selection makes the projection drop
// that row, and the push replaces the agentctl map so the tracker loses the
// creation base and falls back to the newly selected branch.
func TestManualBaseBranchSelectionPushesCreationBaseWithoutTheRow(t *testing.T) {
	svc, _, repo := createTestService(t)
	ctx := context.Background()
	seedComparisonServiceWorkspace(t, repo, "contributor/widget", "head-42")
	taskID := createTaskWithComparisonWorkspace(t, svc, "Creation base live push")
	pusher := &recordingCreationBasePusher{}
	svc.SetAgentCreationBasePusher(pusher)

	row := singleTaskRepository(t, repo, taskID)
	base := creationBaseForServiceTest("main")
	if err := repo.UpdateTaskRepositoryCreationBase(ctx, row.ID, &base); err != nil {
		t.Fatalf("persist creation base: %v", err)
	}

	svc.pushTaskCreationBases(ctx, taskID)
	pusher.mu.Lock()
	if len(pusher.calls) != 1 || len(pusher.calls[0]) != 2 || !pusher.calls[0][""].Equal(base) {
		pusher.mu.Unlock()
		t.Fatalf("first push = %v, want the creation base map", pusher.calls)
	}
	pusher.mu.Unlock()

	if _, err := svc.UpdateRepositoryBaseBranch(ctx, UpdateRepositoryBaseBranchRequest{
		TaskID:           taskID,
		TaskRepositoryID: row.ID,
		BaseBranch:       "develop",
	}); err != nil {
		t.Fatalf("manual base branch selection: %v", err)
	}

	pusher.mu.Lock()
	defer pusher.mu.Unlock()
	if len(pusher.calls) != 2 {
		t.Fatalf("push calls = %d, want the manual selection to push again", len(pusher.calls))
	}
	if len(pusher.calls[1]) != 0 {
		t.Fatalf("post-selection push = %v, want an empty map replacing the stale base", pusher.calls[1])
	}
}

func singleTaskRepository(t *testing.T, repo interface {
	ListTaskRepositories(ctx context.Context, taskID string) ([]*models.TaskRepository, error)
}, taskID string) *models.TaskRepository {
	t.Helper()
	rows, err := repo.ListTaskRepositories(context.Background(), taskID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListTaskRepositories: %v rows=%d", err, len(rows))
	}
	return rows[0]
}
