package executor

import (
	"context"

	"go.uber.org/zap"

	"github.com/kandev/kandev/internal/task/models"
)

// persistWorktreeCreationBases records the fork point each newly
// materialized worktree branch was cut from, so later comparison surfaces
// can default to the worktree's own work. Best-effort by design: the record
// is an enhancement, not a precondition of materialization, so a failure is
// logged without aborting the launch.
func (e *Executor) persistWorktreeCreationBases(ctx context.Context, req *LaunchAgentRequest, resp *LaunchAgentResponse) {
	if e.taskRepositoryCreationBaseRecorder == nil || resp == nil {
		return
	}
	results := resp.Worktrees
	if len(results) == 0 && req != nil {
		// Single-repository launches carry the fork point on the legacy
		// response fields instead of the Worktrees list.
		if resp.CreationBaseRef != "" && resp.CreationForkSHA != "" && req.TaskRepositoryID != "" {
			results = []RepoWorktreeResult{{
				TaskRepositoryID: req.TaskRepositoryID,
				RepositoryID:     req.RepositoryID,
				CreationBaseRef:  resp.CreationBaseRef,
				CreationForkSHA:  resp.CreationForkSHA,
			}}
		}
	}
	for _, result := range results {
		if result.TaskRepositoryID == "" || result.CreationBaseRef == "" || result.CreationForkSHA == "" {
			continue
		}
		base := &models.CreationBase{
			Version: models.CreationBaseVersion,
			Ref:     result.CreationBaseRef,
			ForkSHA: result.CreationForkSHA,
		}
		if err := e.taskRepositoryCreationBaseRecorder.UpdateTaskRepositoryCreationBase(ctx, result.TaskRepositoryID, base); err != nil {
			e.logger.Warn("failed to persist worktree creation base",
				zap.String("task_repository_id", result.TaskRepositoryID),
				zap.String("creation_base_ref", result.CreationBaseRef),
				zap.Error(err))
		}
	}
}
