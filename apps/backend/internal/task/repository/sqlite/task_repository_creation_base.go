package sqlite

import (
	"context"
	"errors"

	"github.com/kandev/kandev/internal/task/models"
)

// UpdateTaskRepositoryCreationBase atomically persists the creation base
// recorded when the attachment's worktree branch was cut. It never changes
// the checkout or base branch, preserves unrelated metadata keys, and bumps
// task_repositories.updated_at, so it follows the comparison-target narrow
// mutation pattern, including the owning task's row lock: a concurrent
// runner switch's compatibility re-check must resolve fully before or fully
// after this write.
func (r *Repository) UpdateTaskRepositoryCreationBase(
	ctx context.Context,
	id string,
	base *models.CreationBase,
) error {
	if base == nil {
		return errors.New("creation base is required")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	taskRepo, err := r.lockTaskThenGetTaskRepository(ctx, tx, id)
	if err != nil {
		return err
	}
	if taskRepo.Metadata == nil {
		taskRepo.Metadata = make(map[string]interface{})
	}
	current, present, err := models.LoadCreationBase(taskRepo.Metadata)
	if err != nil {
		return err
	}
	if present && current.Equal(*base) {
		return nil
	}
	if err := models.PutCreationBase(taskRepo.Metadata, base); err != nil {
		return err
	}
	taskRepo.UpdatedAt = r.nowUTC()
	if err := updateTaskRepositoryMetadata(ctx, tx, taskRepo); err != nil {
		return err
	}
	return tx.Commit()
}
