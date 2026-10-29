package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/orchestrator/watcher"
	"github.com/kandev/kandev/internal/steptelemetry"
	"github.com/kandev/kandev/internal/task/models"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
)

// manualStepWaitEvents filters a recording bus down to the manual-step wait
// notification payloads, so tests stay focused on the subject under test.
func manualStepWaitEvents(eb *recordingEventBus) []map[string]interface{} {
	out := make([]map[string]interface{}, 0)
	for _, recorded := range eb.events {
		if recorded.subject != events.TaskManualStepEntered {
			continue
		}
		data, ok := recorded.event.Data.(map[string]interface{})
		if !ok {
			continue
		}
		out = append(out, data)
	}
	return out
}

// moveTaskIntoStepWithAgentAttribution moves the seeded task into stepID with
// an agent attribution so the newest ledger row for stepID is an attributed
// move rather than the task-creation genesis row. It returns the move's
// ledger id, the occurrence identity the notifier must reuse.
func moveTaskIntoStepWithAgentAttribution(t *testing.T, repo repoStore, taskID, stepID string) int64 {
	t.Helper()
	ctx := context.Background()
	task, err := repo.GetTask(ctx, taskID)
	require.NoError(t, err)
	task.WorkflowStepID = stepID
	moveCtx := steptelemetry.WithAttribution(ctx, steptelemetry.Attribution{
		Trigger:   steptelemetry.TriggerEngineTransition,
		ActorKind: steptelemetry.ActorAgent,
		ActorID:   "session-move-actor",
	})
	require.NoError(t, repo.UpdateTask(moveCtx, task))
	ledger, ok := repo.(interface {
		ListTaskStepTransitions(ctx context.Context, taskID string, beforeID int64, limit int) ([]models.StepTransition, error)
	})
	require.True(t, ok, "repository must expose ListTaskStepTransitions")
	rows, err := ledger.ListTaskStepTransitions(ctx, taskID, 0, 10)
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	return rows[0].ID
}

func TestManualStepWaitNotifierFiresForStepWithoutOnEnter(t *testing.T) {
	repo := setupTestRepo(t)
	seedSession(t, repo, "task-mw-plain", "session-mw-plain", "step-source")
	transitionID := moveTaskIntoStepWithAgentAttribution(t, repo, "task-mw-plain", "step-manual")

	stepGetter := newMockStepGetter()
	stepGetter.steps["step-manual"] = &wfmodels.WorkflowStep{
		ID: "step-manual", WorkflowID: "wf1", Name: "Manual Review",
	}
	eb := &recordingEventBus{}
	svc := createTestService(repo, stepGetter, newMockTaskRepo())
	svc.eventBus = eb

	svc.publishSessionWaitingEvent(context.Background(), "task-mw-plain", "session-mw-plain", "step-manual")

	events := manualStepWaitEvents(eb)
	require.Len(t, events, 1, "a manual step without on_enter must publish exactly one manual-step wait event")
	data := events[0]
	require.Equal(t, "task-mw-plain", data["task_id"])
	require.Equal(t, "session-mw-plain", data["session_id"])
	require.Equal(t, "step-manual", data["workflow_step_id"])
	require.Equal(t, manualStepWaitOccurrenceID("task-mw-plain", "step-manual", transitionID), data["occurrence_id"])
	require.Equal(t, "agent", data["actor_kind"])
	require.Equal(t, "session-move-actor", data["actor_id"])
	require.NotEmpty(t, data["occurred_at"], "the payload must carry the transition's occurred_at")
}

// TestManualStepWaitNotifierCoversNonStartOnEnterSteps pins review focus 1:
// a step whose only on_enter action is reset_agent_context enters through
// launchAfterOnEnterDispatch, not the no-on_enter branch, and must still
// notify exactly once.
func TestManualStepWaitNotifierCoversNonStartOnEnterSteps(t *testing.T) {
	repo := setupTestRepo(t)
	seedSession(t, repo, "task-mw-reset", "session-mw-reset", "step-source")
	transitionID := moveTaskIntoStepWithAgentAttribution(t, repo, "task-mw-reset", "step-reset")

	stepGetter := newMockStepGetter()
	stepGetter.steps["step-reset"] = &wfmodels.WorkflowStep{
		ID: "step-reset", WorkflowID: "wf1", Name: "Reset Then Wait",
		Events: wfmodels.StepEvents{OnEnter: []wfmodels.OnEnterAction{
			{Type: wfmodels.OnEnterResetAgentContext},
		}},
	}
	seedExecutorRunning(t, repo, "session-mw-reset", "task-mw-reset", "exec-mw-reset")
	eb := &recordingEventBus{}
	svc := createTestServiceWithAgent(repo, stepGetter, newMockTaskRepo(), &mockAgentManager{repoForExecutionLookup: repo})
	svc.eventBus = eb

	session, err := repo.GetTaskSession(context.Background(), "session-mw-reset")
	require.NoError(t, err)
	svc.processOnEnter(context.Background(), "task-mw-reset", session, stepGetter.steps["step-reset"], "task description", 0, nil)

	events := manualStepWaitEvents(eb)
	require.Len(t, events, 1, "a reset_agent_context-only step must publish exactly one manual-step wait event")
	require.Equal(t, manualStepWaitOccurrenceID("task-mw-reset", "step-reset", transitionID), events[0]["occurrence_id"])
}

func TestManualStepWaitNotifierSkipsAutoStartAndCompleteOnEnterSteps(t *testing.T) {
	autoStartStep := &wfmodels.WorkflowStep{
		ID: "step-auto", WorkflowID: "wf1", Name: "Auto Start",
		Events: wfmodels.StepEvents{OnEnter: []wfmodels.OnEnterAction{
			{Type: wfmodels.OnEnterAutoStartAgent},
		}},
	}
	completeStep := &wfmodels.WorkflowStep{
		ID: "step-done", WorkflowID: "wf1", Name: "Done", CompleteTaskOnEnter: true,
	}

	for name, step := range map[string]*wfmodels.WorkflowStep{
		"auto-start step":        autoStartStep,
		"complete-on-enter step": completeStep,
	} {
		t.Run(name, func(t *testing.T) {
			repo := setupTestRepo(t)
			seedSession(t, repo, "task-mw-gate", "session-mw-gate", "step-source")
			moveTaskIntoStepWithAgentAttribution(t, repo, "task-mw-gate", step.ID)

			stepGetter := newMockStepGetter()
			stepGetter.steps[step.ID] = step
			eb := &recordingEventBus{}
			svc := createTestService(repo, stepGetter, newMockTaskRepo())
			svc.eventBus = eb

			svc.publishSessionWaitingEvent(context.Background(), "task-mw-gate", "session-mw-gate", step.ID)

			require.Empty(t, manualStepWaitEvents(eb), "steps that start work or complete the task on entry must not notify")
		})
	}
}

// TestManualStepWaitNotifierReusesOccurrenceIdentityWithinEpisode pins the
// orchestrator's half of episode convergence: every waiting convergence of
// the same episode publishes the same occurrence id, so the notification
// service's existing occurrence dedup collapses repeated waiting events into
// one delivery.
func TestManualStepWaitNotifierReusesOccurrenceIdentityWithinEpisode(t *testing.T) {
	repo := setupTestRepo(t)
	seedSession(t, repo, "task-mw-repeat", "session-mw-repeat", "step-source")
	transitionID := moveTaskIntoStepWithAgentAttribution(t, repo, "task-mw-repeat", "step-manual")

	stepGetter := newMockStepGetter()
	stepGetter.steps["step-manual"] = &wfmodels.WorkflowStep{
		ID: "step-manual", WorkflowID: "wf1", Name: "Manual Review",
	}
	eb := &recordingEventBus{}
	svc := createTestService(repo, stepGetter, newMockTaskRepo())
	svc.eventBus = eb

	svc.publishSessionWaitingEvent(context.Background(), "task-mw-repeat", "session-mw-repeat", "step-manual")
	svc.publishSessionWaitingEvent(context.Background(), "task-mw-repeat", "session-mw-repeat", "step-manual")

	events := manualStepWaitEvents(eb)
	require.Len(t, events, 2, "both waiting convergences publish")
	want := manualStepWaitOccurrenceID("task-mw-repeat", "step-manual", transitionID)
	require.Equal(t, want, events[0]["occurrence_id"])
	require.Equal(t, want, events[1]["occurrence_id"], "the same episode must reuse one occurrence identity")
}

func TestManualStepWaitNotifierCoversSessionlessEntry(t *testing.T) {
	repo := setupTestRepo(t)
	seedTaskWithoutSession(t, repo, "task-mw-nosession", "step-source")
	transitionID := moveTaskIntoStepWithAgentAttribution(t, repo, "task-mw-nosession", "step-manual")

	stepGetter := newMockStepGetter()
	stepGetter.steps["step-manual"] = &wfmodels.WorkflowStep{
		ID: "step-manual", WorkflowID: "wf1", Name: "Manual Review",
	}
	eb := &recordingEventBus{}
	svc := createTestService(repo, stepGetter, newMockTaskRepo())
	svc.eventBus = eb

	svc.handleTaskMovedNoSession(context.Background(), watcher.TaskMovedEventData{
		TaskID:           "task-mw-nosession",
		FromStepID:       "step-source",
		ToStepID:         "step-manual",
		StepTransitionID: transitionID,
	})

	events := manualStepWaitEvents(eb)
	require.Len(t, events, 1, "a sessionless task entering a manual step must publish exactly one event")
	require.Equal(t, manualStepWaitOccurrenceID("task-mw-nosession", "step-manual", transitionID), events[0]["occurrence_id"])
	require.Equal(t, "", events[0]["session_id"], "a sessionless entry carries no session id")
}

// TestManualStepWaitNotifierFallsBackWithoutLedgerRow pins D2's fallback:
// when no ledger row names the step, the episode identity degrades to the
// zero transition id rather than failing the waiting convergence.
func TestManualStepWaitNotifierFallsBackWithoutLedgerRow(t *testing.T) {
	repo := setupTestRepo(t)
	// A task with no workflow writes no genesis ledger row at all, so the
	// step named by the waiting convergence has no row to derive from.
	seedSession(t, repo, "task-mw-fallback", "session-mw-fallback", "")

	stepGetter := newMockStepGetter()
	stepGetter.steps["step-manual"] = &wfmodels.WorkflowStep{
		ID: "step-manual", WorkflowID: "wf1", Name: "Manual Review",
	}
	eb := &recordingEventBus{}
	svc := createTestService(repo, stepGetter, newMockTaskRepo())
	svc.eventBus = eb

	svc.publishSessionWaitingEvent(context.Background(), "task-mw-fallback", "session-mw-fallback", "step-manual")

	events := manualStepWaitEvents(eb)
	require.Len(t, events, 1)
	require.Equal(t, manualStepWaitOccurrenceID("task-mw-fallback", "step-manual", 0), events[0]["occurrence_id"])
}
