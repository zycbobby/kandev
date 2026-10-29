package backendapp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/events/bus"
)

// recordingManualStepWaitNotifier captures the calls the bus wiring makes, so
// the payload-to-service mapping is asserted without constructing the
// concrete notification service.
type recordingManualStepWaitNotifier struct {
	calls []manualStepWaitCall
}

type manualStepWaitCall struct {
	taskID, sessionID, occurrenceID, actorKind, actorID string
}

func (n *recordingManualStepWaitNotifier) HandleManualStepWait(_ context.Context, taskID, sessionID, occurrenceID, actorKind, actorID string) {
	n.calls = append(n.calls, manualStepWaitCall{
		taskID: taskID, sessionID: sessionID, occurrenceID: occurrenceID,
		actorKind: actorKind, actorID: actorID,
	})
}

func newGatewayTestEventBus(t *testing.T) *bus.MemoryEventBus {
	t.Helper()
	log, err := logger.NewFromZap(zap.NewNop())
	require.NoError(t, err)
	return bus.NewMemoryEventBus(log)
}

// TestSubscribeManualStepWaitNotificationsWiresBusPayloadToService asserts the
// full wiring: a task.manual_step_entered bus event reaches the notification
// service with every payload field mapped onto HandleManualStepWait.
func TestSubscribeManualStepWaitNotificationsWiresBusPayloadToService(t *testing.T) {
	eventBus := newGatewayTestEventBus(t)
	notifier := &recordingManualStepWaitNotifier{}
	log, err := logger.NewFromZap(zap.NewNop())
	require.NoError(t, err)
	subscribeManualStepWaitNotifications(eventBus, notifier, log)

	require.NoError(t, eventBus.Publish(context.Background(), events.TaskManualStepEntered, bus.NewEvent(
		events.TaskManualStepEntered,
		"orchestrator",
		map[string]interface{}{
			"task_id":          "task-1",
			"session_id":       "session-1",
			"workflow_step_id": "step-review",
			"occurrence_id":    "mw:task-1:step-review:7",
			"actor_kind":       "agent",
			"actor_id":         "session-move-actor",
			"occurred_at":      "2026-10-28T10:00:00Z",
		},
	)))

	require.Len(t, notifier.calls, 1)
	call := notifier.calls[0]
	require.Equal(t, "task-1", call.taskID)
	require.Equal(t, "session-1", call.sessionID)
	require.Equal(t, "mw:task-1:step-review:7", call.occurrenceID)
	require.Equal(t, "agent", call.actorKind)
	require.Equal(t, "session-move-actor", call.actorID)
}

// TestSubscribeManualStepWaitNotificationsDropsUnidentifiablePayloads keeps
// occurrences without a task or occurrence identity out of the pipeline: they
// cannot be deduplicated downstream, so delivering them could repeat forever.
func TestSubscribeManualStepWaitNotificationsDropsUnidentifiablePayloads(t *testing.T) {
	eventBus := newGatewayTestEventBus(t)
	notifier := &recordingManualStepWaitNotifier{}
	log, err := logger.NewFromZap(zap.NewNop())
	require.NoError(t, err)
	subscribeManualStepWaitNotifications(eventBus, notifier, log)

	for _, payload := range []interface{}{
		map[string]interface{}{"session_id": "session-1", "occurrence_id": "mw:t:s:1"},
		map[string]interface{}{"task_id": "task-1"},
		"not a map",
	} {
		require.NoError(t, eventBus.Publish(context.Background(), events.TaskManualStepEntered, bus.NewEvent(
			events.TaskManualStepEntered,
			"orchestrator",
			payload,
		)))
	}

	require.Empty(t, notifier.calls)
}

// TestManualStepWaitOccurrenceMapping table-tests the payload extraction the
// subscription relies on, including a sessionless entry (empty session id).
func TestManualStepWaitOccurrenceMapping(t *testing.T) {
	taskID, sessionID, occurrenceID, actorKind, actorID, ok := manualStepWaitOccurrence(map[string]interface{}{
		"task_id":       "task-1",
		"occurrence_id": "mw:task-1:step-review:7",
		"actor_kind":    "human",
	})
	require.True(t, ok)
	require.Equal(t, "task-1", taskID)
	require.Equal(t, "", sessionID)
	require.Equal(t, "mw:task-1:step-review:7", occurrenceID)
	require.Equal(t, "human", actorKind)
	require.Equal(t, "", actorID)
}
