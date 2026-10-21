package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
)

func TestUpdateTaskRepositoryCreationBase(t *testing.T) {
	repo, db := newTaskExternalIDTestRepo(t)
	ctx := context.Background()
	id := "task-repository-creation-base"
	now := time.Now().UTC()
	seedComparisonTargetAttachment(t, repo, "task-creation-base", "repository-creation-base", now)
	metadata := map[string]interface{}{
		"unrelated": "keep",
		models.ManualBaseBranchOverrideMetadataKey: true,
	}
	if _, err := db.Exec(db.Rebind(`
		INSERT INTO task_repositories
			(id, task_id, repository_id, base_branch, checkout_branch, position, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`), id, "task-creation-base", "repository-creation-base", "main", "feature/creation-base", 0, mustJSON(t, metadata), now, now); err != nil {
		t.Fatalf("insert task repository: %v", err)
	}

	base := models.CreationBase{
		Version: models.CreationBaseVersion,
		Ref:     "main",
		ForkSHA: strings.Repeat("a", 40),
	}
	if err := repo.UpdateTaskRepositoryCreationBase(ctx, id, &base); err != nil {
		t.Fatalf("set creation base: %v", err)
	}

	loaded, err := repo.GetTaskRepository(ctx, id)
	if err != nil {
		t.Fatalf("reload task repository: %v", err)
	}
	got, ok, err := models.LoadCreationBase(loaded.Metadata)
	if err != nil {
		t.Fatalf("load creation base: %v", err)
	}
	if !ok || !got.Equal(base) {
		t.Fatalf("loaded creation base = %#v, want %#v", got, base)
	}
	if loaded.Metadata["unrelated"] != "keep" {
		t.Fatalf("unrelated metadata changed: %#v", loaded.Metadata)
	}
	if !models.HasManualBaseBranchOverride(loaded.Metadata) {
		t.Fatalf("manual override marker was dropped: %#v", loaded.Metadata)
	}
	if loaded.BaseBranch != "main" || loaded.CheckoutBranch != "feature/creation-base" {
		t.Fatalf("creation base write changed branch columns: %#v", loaded)
	}
}

func TestUpdateTaskRepositoryCreationBaseOverwritesOnRematerialization(t *testing.T) {
	repo, db := newTaskExternalIDTestRepo(t)
	ctx := context.Background()
	id := "task-repository-creation-base-remat"
	now := time.Now().UTC()
	seedComparisonTargetAttachment(t, repo, "task-creation-base-remat", "repository-creation-base-remat", now)
	if _, err := db.Exec(db.Rebind(`
		INSERT INTO task_repositories
			(id, task_id, repository_id, base_branch, checkout_branch, position, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`), id, "task-creation-base-remat", "repository-creation-base-remat", "main", "feature/creation-base-remat", 0, mustJSON(t, map[string]interface{}{}), now, now); err != nil {
		t.Fatalf("insert task repository: %v", err)
	}

	first := models.CreationBase{Version: models.CreationBaseVersion, Ref: "main", ForkSHA: strings.Repeat("a", 40)}
	if err := repo.UpdateTaskRepositoryCreationBase(ctx, id, &first); err != nil {
		t.Fatalf("first write: %v", err)
	}
	second := models.CreationBase{Version: models.CreationBaseVersion, Ref: "develop", ForkSHA: strings.Repeat("b", 40)}
	if err := repo.UpdateTaskRepositoryCreationBase(ctx, id, &second); err != nil {
		t.Fatalf("second write: %v", err)
	}
	loaded, err := repo.GetTaskRepository(ctx, id)
	if err != nil {
		t.Fatalf("reload task repository: %v", err)
	}
	got, ok, err := models.LoadCreationBase(loaded.Metadata)
	if err != nil {
		t.Fatalf("load creation base: %v", err)
	}
	if !ok || !got.Equal(second) {
		t.Fatalf("loaded creation base = %#v, want overwrite to %#v", got, second)
	}
}

func TestUpdateTaskRepositoryCreationBaseRejectsMissingRowAndNilBase(t *testing.T) {
	repo, _ := newTaskExternalIDTestRepo(t)
	ctx := context.Background()
	base := models.CreationBase{Version: models.CreationBaseVersion, Ref: "main", ForkSHA: strings.Repeat("a", 40)}

	err := repo.UpdateTaskRepositoryCreationBase(ctx, "missing-task-repository", &base)
	if err == nil || !strings.Contains(err.Error(), "task repository not found") {
		t.Fatalf("missing row error = %v, want task repository not found", err)
	}

	if err := repo.UpdateTaskRepositoryCreationBase(ctx, "any", nil); err == nil {
		t.Fatal("nil creation base should fail closed")
	}
}
