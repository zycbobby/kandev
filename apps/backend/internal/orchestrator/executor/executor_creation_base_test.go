package executor

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
)

type fakeCreationBaseRecorder struct {
	mu       sync.Mutex
	recorded map[string]models.CreationBase
	failIDs  map[string]struct{}
}

func newFakeCreationBaseRecorder() *fakeCreationBaseRecorder {
	return &fakeCreationBaseRecorder{
		recorded: make(map[string]models.CreationBase),
		failIDs:  make(map[string]struct{}),
	}
}

func (r *fakeCreationBaseRecorder) UpdateTaskRepositoryCreationBase(ctx context.Context, taskRepositoryID string, base *models.CreationBase) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.failIDs[taskRepositoryID]; ok {
		return errors.New("recorder unavailable")
	}
	r.recorded[taskRepositoryID] = *base
	return nil
}

func (r *fakeCreationBaseRecorder) get(taskRepositoryID string) (models.CreationBase, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	base, ok := r.recorded[taskRepositoryID]
	return base, ok
}

// TestPersistWorktreeCreationBases asserts the executor forwards each newly
// cut worktree branch's fork point to the task-repository seam, verbatim:
// ref exactly as captured at materialization and the cut-time commit.
func TestPersistWorktreeCreationBases(t *testing.T) {
	repo := newMockRepository()
	exec := newTestExecutor(t, &mockAgentManager{}, repo)
	recorder := newFakeCreationBaseRecorder()
	exec.SetTaskRepositoryCreationBaseRecorder(recorder)

	exec.persistWorktreeCreationBases(context.Background(), &LaunchAgentResponse{
		Worktrees: []RepoWorktreeResult{
			{
				TaskRepositoryID: "tr-creation-1",
				RepositoryID:     "repo-1",
				CreationBaseRef:  "main",
				CreationForkSHA:  strings.Repeat("a", 40),
			},
			{
				// Reuse results carry no fork point and must be skipped.
				TaskRepositoryID: "tr-creation-2",
				RepositoryID:     "repo-2",
			},
		},
	})

	base, ok := recorder.get("tr-creation-1")
	if !ok {
		t.Fatal("creation base for tr-creation-1 was not recorded")
	}
	want := models.CreationBase{Version: models.CreationBaseVersion, Ref: "main", ForkSHA: strings.Repeat("a", 40)}
	if !base.Equal(want) {
		t.Fatalf("recorded creation base = %#v, want %#v", base, want)
	}
	if _, ok := recorder.get("tr-creation-2"); ok {
		t.Fatal("reuse result without a creation base must not be recorded")
	}
}

// TestPersistWorktreeCreationBases_FailureIsNonFatal pins the
// best-effort contract: a recorder failure warns but never aborts the
// launch, and later rows are still attempted.
func TestPersistWorktreeCreationBases_FailureIsNonFatal(t *testing.T) {
	repo := newMockRepository()
	exec := newTestExecutor(t, &mockAgentManager{}, repo)
	recorder := newFakeCreationBaseRecorder()
	recorder.failIDs["tr-creation-fail"] = struct{}{}
	exec.SetTaskRepositoryCreationBaseRecorder(recorder)

	exec.persistWorktreeCreationBases(context.Background(), &LaunchAgentResponse{
		Worktrees: []RepoWorktreeResult{
			{
				TaskRepositoryID: "tr-creation-fail",
				RepositoryID:     "repo-1",
				CreationBaseRef:  "main",
				CreationForkSHA:  strings.Repeat("b", 40),
			},
			{
				TaskRepositoryID: "tr-creation-after-fail",
				RepositoryID:     "repo-2",
				CreationBaseRef:  "develop",
				CreationForkSHA:  strings.Repeat("c", 40),
			},
		},
	})

	if _, ok := recorder.get("tr-creation-fail"); ok {
		t.Fatal("failing row should not be recorded")
	}
	if _, ok := recorder.get("tr-creation-after-fail"); !ok {
		t.Fatal("recorder failure must not abort later rows")
	}
}

// TestPersistWorktreeCreationBases_NilDependenciesIsNoop keeps the seam
// optional: unit-test executors without a recorder skip persistence.
func TestPersistWorktreeCreationBases_NilDependenciesIsNoop(t *testing.T) {
	repo := newMockRepository()
	exec := newTestExecutor(t, &mockAgentManager{}, repo)

	exec.persistWorktreeCreationBases(context.Background(), &LaunchAgentResponse{
		Worktrees: []RepoWorktreeResult{{
			TaskRepositoryID: "tr-creation-1",
			CreationBaseRef:  "main",
			CreationForkSHA:  strings.Repeat("a", 40),
		}},
	})
	exec.persistWorktreeCreationBases(context.Background(), nil)
}
