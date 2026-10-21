package service

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/worktree"
)

// TaskComparisonTargets exposes the complete durable comparison-target map
// used to hydrate agentctl after startup, recovery, or a live PR retarget.
// An empty map is a successful projection and means all cached targets must
// be cleared from running workspaces.
func (s *Service) TaskComparisonTargets(ctx context.Context, taskID string) (map[string]models.ComparisonTarget, error) {
	if taskID == "" {
		return map[string]models.ComparisonTarget{}, nil
	}
	return s.collectTaskComparisonTargets(ctx, taskID)
}

// pushTaskComparisonTargets refreshes every running execution from the
// persisted projection. It intentionally pushes an empty map too, because a
// manual base selection, same-repository PR, or source-aware detach must clear
// an older explicit target from live agentctl state.
func (s *Service) pushTaskComparisonTargets(ctx context.Context, taskID string) {
	if s.comparisonTargetPusher == nil {
		return
	}
	targets, err := s.collectTaskComparisonTargets(ctx, taskID)
	if err != nil {
		s.logger.Warn("comparison target live push skipped",
			zap.String("task_id", taskID),
			zap.Error(err))
		return
	}
	s.comparisonTargetPusher.PushComparisonTargetsForTask(ctx, taskID, targets)
}

// RemoveComparisonTargetForChange removes only the provider change that owns
// an attachment's current target. A stale detach cannot erase a newer target
// created by a retarget or a different PR association.
func (s *Service) RemoveComparisonTargetForChange(
	ctx context.Context,
	taskID, repositoryID, provider, kind string,
	number int,
) error {
	if !validComparisonTargetDetachRequest(taskID, repositoryID, provider, kind, number) {
		return nil
	}
	if err := s.authorizeTaskID(ctx, taskID); err != nil {
		return err
	}
	taskRepos, err := s.taskRepos.ListTaskRepositories(ctx, taskID)
	if err != nil {
		return err
	}
	taskRepo, err := comparisonTargetAttachmentForChange(taskRepos, repositoryID, provider, kind, number)
	if err != nil {
		return err
	}
	if taskRepo == nil {
		return nil
	}
	current, ok, err := models.LoadComparisonTarget(taskRepo.Metadata)
	if err != nil {
		return fmt.Errorf("load comparison target for detach: %w", err)
	}
	if !ok || current.Provider != provider || current.Kind != kind || current.Number != number {
		return nil
	}
	if _, changed, err := s.taskRepos.UpdateTaskRepositoryComparisonTarget(ctx, taskRepo.ID, nil, &current, false); err != nil {
		return fmt.Errorf("remove comparison target for detach: %w", err)
	} else if !changed {
		return nil
	}
	s.applyComparisonTargetSideEffects(context.WithoutCancel(ctx), taskID, taskRepo.RepositoryID, taskRepo.BaseBranch)
	return nil
}

func validComparisonTargetDetachRequest(taskID, repositoryID, provider, kind string, number int) bool {
	return taskID != "" && repositoryID != "" && provider != "" && kind != "" && number > 0
}

func comparisonTargetAttachmentForChange(
	taskRepos []*models.TaskRepository,
	repositoryID, provider, kind string,
	number int,
) (*models.TaskRepository, error) {
	var matched *models.TaskRepository
	for _, candidate := range taskRepos {
		if candidate == nil || candidate.RepositoryID != repositoryID {
			continue
		}
		current, ok, err := models.LoadComparisonTarget(candidate.Metadata)
		if err != nil {
			return nil, fmt.Errorf("load comparison target for detach: %w", err)
		}
		if !ok || current.Provider != provider || current.Kind != kind || current.Number != number {
			continue
		}
		if matched != nil {
			// Two attachments can point at the same repository. Do not clear
			// either one when the provider change does not identify one exact
			// attachment.
			return nil, nil
		}
		matched = candidate
	}
	return matched, nil
}

func (s *Service) applyComparisonTargetSideEffects(ctx context.Context, taskID, repositoryID, baseBranch string) {
	if s.sessions != nil {
		if _, err := s.sessions.ResetTaskSessionBasesForRepository(ctx, taskID, repositoryID, baseBranch); err != nil {
			s.logger.Warn("comparison target update: failed to reset session bases",
				zap.String("task_id", taskID),
				zap.String("repository_id", repositoryID),
				zap.Error(err))
		}
	}
	if task, err := s.tasks.GetTask(ctx, taskID); err == nil && task != nil {
		s.publishTaskEvent(ctx, events.TaskUpdated, task, nil)
	}
	s.pushTaskComparisonTargets(ctx, taskID)
}

func (s *Service) collectTaskComparisonTargets(ctx context.Context, taskID string) (map[string]models.ComparisonTarget, error) {
	return collectTrackerProjection(ctx, s.taskRepos, s.workspaceSourceExecutorType, taskID, "comparison-target", s.loadComparisonTargetInput)
}

// collectTrackerProjection builds one agentctl tracker-key map from a per-row
// projection loader. The loader returns the row's repository entity, its
// projected value, and whether the row contributes an entry; rows that do not
// contribute may still return a repository so the positional branch-identity
// plans — and therefore every row's tracker key — stay aligned across the
// base-branch, comparison-target, and creation-base projections.
func collectTrackerProjection[T any](
	ctx context.Context,
	lister interface {
		ListTaskRepositories(ctx context.Context, taskID string) ([]*models.TaskRepository, error)
	},
	resolveExecutorType func(ctx context.Context, taskID string) (string, error),
	taskID, label string,
	loadRow func(ctx context.Context, taskRepo *models.TaskRepository) (*models.Repository, T, bool, error),
) (map[string]T, error) {
	taskRepos, err := lister.ListTaskRepositories(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("list task repositories for %s map: %w", label, err)
	}
	executorType, err := resolveExecutorType(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("resolve executor type for %s map: %w", label, err)
	}
	repos, values, hasValue, err := loadTrackerProjectionRows(ctx, taskRepos, loadRow)
	if err != nil {
		return nil, err
	}
	inputs := trackerProjectionInputs(repos, taskRepos)
	plans := worktree.BuildBranchIdentityPlans(inputs)
	result := make(map[string]T, len(taskRepos)+1)
	for i, value := range values {
		if !hasValue[i] || repos[i] == nil {
			continue
		}
		key, err := taskRepositoryWorkspaceTrackerKey(executorType, i, repos[i], taskRepos[i], plans[i].PathSlug)
		if err != nil {
			return nil, fmt.Errorf("resolve %s workspace for repository %q: %w", label, repos[i].Name, err)
		}
		result[key] = value
	}
	if len(taskRepos) == 1 && hasValue[0] && executorType != string(models.ExecutorTypePluginRemote) {
		result[""] = values[0]
	}
	return result, nil
}

// loadTrackerProjectionRows runs the per-row loader positionally, keeping the
// repository entities aligned with the task-repository rows.
func loadTrackerProjectionRows[T any](
	ctx context.Context,
	taskRepos []*models.TaskRepository,
	loadRow func(ctx context.Context, taskRepo *models.TaskRepository) (*models.Repository, T, bool, error),
) ([]*models.Repository, []T, []bool, error) {
	repos := make([]*models.Repository, len(taskRepos))
	values := make([]T, len(taskRepos))
	hasValue := make([]bool, len(taskRepos))
	for i, taskRepo := range taskRepos {
		if taskRepo == nil {
			continue
		}
		repo, value, ok, err := loadRow(ctx, taskRepo)
		if err != nil {
			return nil, nil, nil, err
		}
		if ok {
			values[i] = value
			hasValue[i] = true
		}
		repos[i] = repo
	}
	return repos, values, hasValue, nil
}

// trackerProjectionInputs builds the branch-identity inputs for every row so
// each projection's tracker keys agree with the worktree layout.
func trackerProjectionInputs(repos []*models.Repository, taskRepos []*models.TaskRepository) []worktree.BranchIdentityInput {
	inputs := make([]worktree.BranchIdentityInput, len(taskRepos))
	for i, taskRepo := range taskRepos {
		defaultBranch := ""
		if repos[i] != nil {
			defaultBranch = repos[i].DefaultBranch
		}
		inputs[i] = worktree.BranchIdentityInput{
			RepositoryID:   taskRepo.RepositoryID,
			BaseBranch:     taskRepo.BaseBranch,
			CheckoutBranch: taskRepo.CheckoutBranch,
			DefaultBranch:  defaultBranch,
			PRNumber:       taskRepositoryPRNumber(taskRepo.Metadata),
			Position:       taskRepo.Position,
		}
	}
	return inputs
}

func (s *Service) loadComparisonTargetInput(
	ctx context.Context,
	taskRepo *models.TaskRepository,
) (*models.Repository, models.ComparisonTarget, bool, error) {
	target, hasTarget, err := models.LoadComparisonTarget(taskRepo.Metadata)
	if err != nil {
		return nil, models.ComparisonTarget{}, false,
			fmt.Errorf("load comparison target for task repository %s: %w", taskRepo.ID, err)
	}
	repo, repoErr := s.repoEntities.GetRepository(ctx, taskRepo.RepositoryID)
	if repoErr != nil && !hasTarget {
		return nil, models.ComparisonTarget{}, false, nil
	}
	if repoErr != nil {
		return nil, models.ComparisonTarget{}, false,
			fmt.Errorf("resolve repository %s for comparison-target map: %w", taskRepo.RepositoryID, repoErr)
	}
	if (repo == nil || repo.Name == "") && !hasTarget {
		return nil, models.ComparisonTarget{}, false, nil
	}
	if repo == nil || repo.Name == "" {
		return nil, models.ComparisonTarget{}, false,
			fmt.Errorf("repository %s for comparison-target map is missing or unnamed", taskRepo.RepositoryID)
	}
	return repo, target, hasTarget, nil
}
