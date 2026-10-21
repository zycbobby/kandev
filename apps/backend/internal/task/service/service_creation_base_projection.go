package service

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/kandev/kandev/internal/task/models"
)

// TaskCreationBases exposes the effective creation-base map used to hydrate
// agentctl after startup, recovery, or a live base-branch selection. The map
// already carries the backend priority decision — rows with an explicit
// comparison target or a manual base-branch override are excluded — so every
// entry is a default that should actually take effect.
func (s *Service) TaskCreationBases(ctx context.Context, taskID string) (map[string]models.CreationBase, error) {
	if taskID == "" {
		return map[string]models.CreationBase{}, nil
	}
	return s.collectTaskCreationBases(ctx, taskID)
}

// pushTaskCreationBases refreshes every running execution from the persisted
// projection. It intentionally pushes an empty map too, because a manual
// base-branch selection or a newly attached comparison target must evict a
// stale creation base from live agentctl state, not just from the next
// launch.
func (s *Service) pushTaskCreationBases(ctx context.Context, taskID string) {
	if s.creationBasePusher == nil {
		return
	}
	bases, err := s.collectTaskCreationBases(ctx, taskID)
	if err != nil {
		s.logger.Warn("creation base live push skipped",
			zap.String("task_id", taskID),
			zap.Error(err))
		return
	}
	s.creationBasePusher.PushCreationBasesForTask(ctx, taskID, bases)
}

// collectTaskCreationBases mirrors the comparison-target projection through
// the shared tracker-key helper. The priority filter lives in
// loadCreationBaseInput.
func (s *Service) collectTaskCreationBases(ctx context.Context, taskID string) (map[string]models.CreationBase, error) {
	return collectTrackerProjection(ctx, s.taskRepos, s.workspaceSourceExecutorType, taskID, "creation-base", s.loadCreationBaseInput)
}

// loadCreationBaseInput loads one row's creation base together with its
// repository entity, applying the priority decision: an explicit comparison
// target (fork PR flow) or a manual base-branch selection outranks the
// creation-base default, so such rows deliberately contribute nothing and the
// agentctl-side priority stays "explicit target > creation base > origin-first".
func (s *Service) loadCreationBaseInput(
	ctx context.Context,
	taskRepo *models.TaskRepository,
) (*models.Repository, models.CreationBase, bool, error) {
	if _, hasTarget, err := models.LoadComparisonTarget(taskRepo.Metadata); err != nil {
		return nil, models.CreationBase{}, false,
			fmt.Errorf("load comparison target for task repository %s: %w", taskRepo.ID, err)
	} else if hasTarget || models.HasManualBaseBranchOverride(taskRepo.Metadata) {
		return nil, models.CreationBase{}, false, nil
	}
	base, hasBase, err := models.LoadCreationBase(taskRepo.Metadata)
	if err != nil {
		return nil, models.CreationBase{}, false,
			fmt.Errorf("load creation base for task repository %s: %w", taskRepo.ID, err)
	}
	repo, repoErr := s.repoEntities.GetRepository(ctx, taskRepo.RepositoryID)
	if repoErr != nil && !hasBase {
		return nil, models.CreationBase{}, false, nil
	}
	if repoErr != nil {
		return nil, models.CreationBase{}, false,
			fmt.Errorf("resolve repository %s for creation-base map: %w", taskRepo.RepositoryID, repoErr)
	}
	if repo == nil || repo.Name == "" {
		if !hasBase {
			return nil, models.CreationBase{}, false, nil
		}
		return nil, models.CreationBase{}, false,
			fmt.Errorf("repository %s for creation-base map is missing or unnamed", taskRepo.RepositoryID)
	}
	return repo, base, hasBase, nil
}
