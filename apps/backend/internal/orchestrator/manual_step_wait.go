package orchestrator

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/events/bus"
	"github.com/kandev/kandev/internal/task/models"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
)

// manualStepTransitionReader reads the durable step-transition ledger rows a
// manual-step wait episode identity is derived from.
type manualStepTransitionReader interface {
	GetLatestStepTransitionForStep(ctx context.Context, taskID, toStepID string) (*models.StepTransition, error)
}

// manualStepWaitOccurrenceID builds the episode identity from the immutable
// ledger row id. Delegates to taskmodels.ManualStepWaitRowID so the notifier's
// occurrence ids and the derived waiting-row ids (and their shared sidecar
// keys) are one identity everywhere. transitionID 0 is the documented fallback
// for a step whose entry has no ledger row.
func manualStepWaitOccurrenceID(taskID, stepID string, transitionID int64) string {
	return models.ManualStepWaitRowID(taskID, stepID, transitionID)
}

// notifyManualStepWait publishes events.TaskManualStepEntered once per call
// for the task's current step, when that step is a manual step. It is the
// single emitter behind every waiting convergence: publishSessionWaitingEvent
// calls it for session-backed entries, and the sessionless no-auto-start
// entry calls it directly. The gate is the step's own semantics only — never
// the step name or the on_enter action spelling.
func (s *Service) notifyManualStepWait(ctx context.Context, taskID, sessionID, stepID string) {
	if s.eventBus == nil || s.workflowStepGetter == nil || taskID == "" || stepID == "" {
		return
	}
	step, err := s.workflowStepGetter.GetStep(ctx, stepID)
	if err != nil || step == nil {
		// Without the step's configuration the manual-step judgment is
		// impossible; fail closed rather than notifying for an auto step.
		s.logger.Warn("manual step wait: step unavailable, skipping notification",
			zap.String("task_id", taskID),
			zap.String("step_id", stepID),
			zap.Error(err))
		return
	}
	if wfmodels.StepRunsOnEntry(step) || step.CompleteTaskOnEnter {
		return
	}

	transitionID, actorKind, actorID, occurredAt := s.manualStepWaitEpisodeIdentity(ctx, taskID, stepID)
	payload := map[string]interface{}{
		metaKeyTaskID:         taskID,
		metaKeySessionID:      sessionID,
		metaKeyWorkflowStepID: stepID,
		"occurrence_id":       manualStepWaitOccurrenceID(taskID, stepID, transitionID),
		"actor_kind":          actorKind,
		"actor_id":            actorID,
		"occurred_at":         occurredAt.UTC().Format(time.RFC3339Nano),
	}
	_ = s.eventBus.Publish(ctx, events.TaskManualStepEntered, bus.NewEvent(
		events.TaskManualStepEntered,
		"orchestrator",
		payload,
	))
}

// manualStepWaitEpisodeIdentity resolves the episode identity inputs from the
// newest ledger row whose to_workflow_step_id is the current step. Missing
// rows degrade to the zero transition id (with a WARN, per the design's
// fallback) and an unattributed actor, never to an error that could fail the
// waiting convergence itself.
func (s *Service) manualStepWaitEpisodeIdentity(ctx context.Context, taskID, stepID string) (transitionID int64, actorKind, actorID string, occurredAt time.Time) {
	occurredAt = time.Now().UTC()
	reader, ok := s.repo.(manualStepTransitionReader)
	if !ok || s.repo == nil {
		s.logger.Warn("manual step wait: repository cannot read step transitions, using zero fallback identity",
			zap.String("task_id", taskID),
			zap.String("step_id", stepID))
		return 0, "", "", occurredAt
	}
	transition, err := reader.GetLatestStepTransitionForStep(ctx, taskID, stepID)
	if err != nil {
		s.logger.Warn("manual step wait: failed to read the latest step transition, using zero fallback identity",
			zap.String("task_id", taskID),
			zap.String("step_id", stepID),
			zap.Error(err))
		return 0, "", "", occurredAt
	}
	if transition == nil {
		s.logger.Warn("manual step wait: no step transition row for the current step, using zero fallback identity",
			zap.String("task_id", taskID),
			zap.String("step_id", stepID))
		return 0, "", "", occurredAt
	}
	if !transition.OccurredAt.IsZero() {
		occurredAt = transition.OccurredAt
	}
	return transition.ID, transition.ActorKind, transition.ActorID, occurredAt
}
