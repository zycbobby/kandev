package lifecycle

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/kandev/kandev/internal/task/models"
)

type creationBaseSetter interface {
	SetCreationBases(ctx context.Context, bases map[string]models.CreationBase) error
}

// CreationBaseProvider hydrates the durable creation-base projection for one
// task. Keys use the same workspace subpath convention as base branches: the
// empty key addresses a single-repository/root tracker.
type CreationBaseProvider func(ctx context.Context, taskID string) (map[string]models.CreationBase, error)

func (m *Manager) SetCreationBaseProvider(fn CreationBaseProvider) {
	m.creationBaseProvider = fn
}

// pushTaskCreationBases refreshes one agentctl instance from durable task
// state at readiness. An empty successful hydration is intentionally NOT
// pushed: SetCreationBases replaces the stored map, and on the full-launch
// path an empty push would wipe what the launch metadata already seeded from
// the freshly observed fork points. The persisted projection catches up once
// the executor records the fork point.
func (m *Manager) pushTaskCreationBases(ctx context.Context, taskID, executionID string, setter creationBaseSetter) {
	if taskID == "" || setter == nil || m.creationBaseProvider == nil {
		return
	}
	bases, err := m.creationBaseProvider(ctx, taskID)
	if err != nil {
		m.logger.Warn("failed to hydrate creation bases for workspace",
			zap.String("task_id", taskID),
			zap.String("execution_id", executionID),
			zap.Error(err))
		return
	}
	if len(bases) == 0 {
		return
	}
	if err := setter.SetCreationBases(ctx, bases); err != nil {
		m.logger.Warn("failed to seed creation bases on agentctl",
			zap.String("task_id", taskID),
			zap.String("execution_id", executionID),
			zap.Error(err))
	}
}

// PushCreationBasesForTask implements the task-service live pusher. The
// durable change may be made from a cancelled HTTP request, so fan-out uses a
// bounded detached context and one execution failure never blocks siblings.
func (m *Manager) PushCreationBasesForTask(ctx context.Context, taskID string, bases map[string]models.CreationBase) {
	if taskID == "" {
		return
	}
	for _, exec := range m.executionStore.List() {
		if exec.TaskID != taskID {
			continue
		}
		client, releaseClient := exec.AcquireAgentCtlClient()
		if client == nil {
			continue
		}
		pushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		err := client.SetCreationBases(pushCtx, bases)
		cancel()
		releaseClient()
		if err != nil {
			m.logger.Warn("failed to push creation bases to agentctl",
				zap.String("task_id", taskID),
				zap.String("execution_id", exec.ID),
				zap.String("session_id", exec.SessionID),
				zap.Error(err))
		}
	}
}

// collectLaunchCreationBases merges the two launch-time sources into the map
// agentctl is seeded with: the durable projection from the provider, and the
// fork points a fresh materialization just observed (stashed on the request
// by the launch step before the executor persists them, so the first session
// gets its creation base without waiting for the DB write). A freshly
// observed fork point wins over a durable entry for the same key because it
// is the newer re-anchor.
func (m *Manager) collectLaunchCreationBases(ctx context.Context, req *LaunchRequest) (map[string]models.CreationBase, error) {
	if req == nil {
		return nil, nil
	}
	merged, err := creationBasesFromMetadata(req.Metadata)
	if err != nil {
		return nil, err
	}
	merged, err = m.mergeDurableCreationBases(ctx, req, merged)
	if err != nil {
		return nil, err
	}
	if len(merged) == 0 {
		return nil, nil
	}
	return merged, nil
}

// mergeDurableCreationBases overlays the durable projection under the
// freshly observed fork points.
func (m *Manager) mergeDurableCreationBases(
	ctx context.Context,
	req *LaunchRequest,
	merged map[string]models.CreationBase,
) (map[string]models.CreationBase, error) {
	if m.creationBaseProvider == nil || req.TaskID == "" {
		return merged, nil
	}
	durable, err := m.creationBaseProvider(ctx, req.TaskID)
	if err != nil {
		return nil, fmt.Errorf("hydrate creation bases: %w", err)
	}
	if len(durable) == 0 {
		return merged, nil
	}
	if merged == nil {
		merged = make(map[string]models.CreationBase, len(durable))
	}
	for key, base := range durable {
		if _, freshlyObserved := merged[key]; !freshlyObserved {
			merged[key] = base
		}
	}
	return merged, nil
}

// stashPreparedCreationBases records the fork points a fresh materialization
// observed on the launch request so they reach the instance seed before the
// executor persists them. Results are index-aligned with the launch's repo
// specs, so the same projection-key derivation as the comparison targets
// applies.
func stashPreparedCreationBases(req *LaunchRequest, prepResult *EnvPrepareResult) error {
	if req == nil || prepResult == nil || len(prepResult.Worktrees) == 0 {
		return nil
	}
	specs := req.RepoSpecs()
	bases := make(map[string]models.CreationBase)
	for i := range prepResult.Worktrees {
		if i >= len(specs) {
			break
		}
		w := prepResult.Worktrees[i]
		if w.CreationBaseRef == "" || w.CreationForkSHA == "" {
			continue
		}
		key, err := launchRepositoryProjectionKey(req, specs[i], i)
		if err != nil {
			return fmt.Errorf("resolve creation-base workspace for repository %q: %w", specs[i].RepoName, err)
		}
		bases[key] = models.CreationBase{
			Version: models.CreationBaseVersion,
			Ref:     w.CreationBaseRef,
			ForkSHA: w.CreationForkSHA,
		}
	}
	if len(bases) == 0 {
		return nil
	}
	metadata := make(map[string]interface{}, len(req.Metadata)+1)
	for k, v := range req.Metadata {
		metadata[k] = v
	}
	metadata[MetadataKeyCreationBases] = bases
	req.Metadata = metadata
	return nil
}
