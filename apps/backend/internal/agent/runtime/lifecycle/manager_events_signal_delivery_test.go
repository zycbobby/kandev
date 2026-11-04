package lifecycle

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	agentctl "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	"github.com/kandev/kandev/internal/agentctl/types/streams"
)

func validSignalDeliveryNotice() *streams.SignalDeliveryFailure {
	return &streams.SignalDeliveryFailure{
		RequestID:      "req-notice",
		Action:         "mcp.step_complete",
		SessionID:      "session-1",
		Reason:         streams.SignalDeliveryFailureReasonWindowExceeded,
		Attempts:       2,
		FirstAttemptAt: time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC),
	}
}

// TestHandleAgentEventPublishesSignalDeliveryNotice pins the durable notice
// crossing the lifecycle boundary with identity, reason, and attempts intact,
// without being counted as model activity.
func TestHandleAgentEventPublishesSignalDeliveryNotice(t *testing.T) {
	h := newWorkspaceEventsHarness(t)
	notice := validSignalDeliveryNotice()

	h.mgr.handleAgentEvent(h.exec, agentctl.AgentEvent{
		Type:                  streams.EventTypeSignalDeliveryFailed,
		SignalDeliveryFailure: notice,
	})

	got := h.only(t)
	payload, ok := got.Event.Data.(AgentStreamEventPayload)
	require.True(t, ok, "payload type = %T", got.Event.Data)
	require.Equal(t, streams.EventTypeSignalDeliveryFailed, payload.Data.Type)
	require.Equal(t, "task-1", payload.TaskID)
	require.Equal(t, "session-1", payload.SessionID)
	published := payload.Data.SignalDeliveryFailure
	require.NotNil(t, published)
	require.Equal(t, "req-notice", published.RequestID)
	require.Equal(t, "mcp.step_complete", published.Action)
	require.Equal(t, streams.SignalDeliveryFailureReasonWindowExceeded, published.Reason)
	require.Equal(t, 2, published.Attempts)
	require.False(t, published.FirstAttemptAt.IsZero())
}

func TestHandleAgentEventDropsMalformedSignalDeliveryNotice(t *testing.T) {
	h := newWorkspaceEventsHarness(t)

	h.mgr.handleAgentEvent(h.exec, agentctl.AgentEvent{
		Type: streams.EventTypeSignalDeliveryFailed,
	})
	require.Zero(t, h.count(), "a notice without its typed payload is dropped")

	stale := validSignalDeliveryNotice()
	stale.SessionID = "session-other"
	h.mgr.handleAgentEvent(h.exec, agentctl.AgentEvent{
		Type:                  streams.EventTypeSignalDeliveryFailed,
		SignalDeliveryFailure: stale,
	})
	require.Zero(t, h.count(), "a notice from another session is dropped")

	unknown := validSignalDeliveryNotice()
	unknown.Reason = "made_up_reason"
	h.mgr.handleAgentEvent(h.exec, agentctl.AgentEvent{
		Type:                  streams.EventTypeSignalDeliveryFailed,
		SignalDeliveryFailure: unknown,
	})
	require.Zero(t, h.count(), "an unknown reason is dropped")
}
