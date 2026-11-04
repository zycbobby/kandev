package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
	ws "github.com/kandev/kandev/pkg/websocket"
)

// TestStepCompleteSignalReplayAppliesOnce simulates the durable replay path
// delivering the same captured step_complete signal twice. The backend's
// existing pending-signal claim is the idempotency boundary: the first call
// applies and publishes, the duplicate returns already_signaled without a
// second workflow event.
func TestStepCompleteSignalReplayAppliesOnce(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestTaskService(t)
	seedStepCompleteTarget(t, repo, "task-replay", "session-replay", "step-1", models.TaskSessionStateRunning)
	bus := &mcpRecordingEventBus{}
	h := newStepCompleteHandler(t, svc, repo, bus)

	msg := makeWSMessage(t, ws.ActionMCPStepComplete, map[string]interface{}{
		"task_id":    "task-replay",
		"session_id": "session-replay",
		"summary":    "captured signal replayed",
	})

	first, err := h.handleStepComplete(ctx, msg)
	require.NoError(t, err)
	require.NotNil(t, first)
	var firstPayload map[string]interface{}
	require.NoError(t, json.Unmarshal(first.Payload, &firstPayload))
	assert.Equal(t, true, firstPayload["accepted"])

	second, err := h.handleStepComplete(ctx, msg)
	require.NoError(t, err)
	require.NotNil(t, second)
	var secondPayload map[string]interface{}
	require.NoError(t, json.Unmarshal(second.Payload, &secondPayload))
	assert.Equal(t, false, secondPayload["accepted"])
	assert.Equal(t, "already_signaled", secondPayload["reason"])

	assert.Len(t, bus.events, 1, "a replayed duplicate must not publish a second completion event")
}

// TestStepCompleteSignalAcceptedClearsCompletionPendingNotice proves the
// frontend's existing agent-error surface stops showing completion-pending as
// soon as the backend accepts the (possibly resent) signal, including the
// already_signaled duplicate path.
func TestStepCompleteSignalAcceptedClearsCompletionPendingNotice(t *testing.T) {
	for _, tc := range []struct {
		name        string
		preSeedBag  bool
		wantReason  string
		wantCleared bool
	}{
		{name: "fresh signal", wantCleared: true},
		{name: "already signaled", preSeedBag: true, wantReason: "already_signaled", wantCleared: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			svc, repo := newTestTaskService(t)
			seedStepCompleteTarget(t, repo, "task-notice", "session-notice", "step-1", models.TaskSessionStateWaitingForInput)
			if tc.preSeedBag {
				require.NoError(t, repo.SetSessionMetadataKey(ctx, "session-notice", models.SessionMetaKeyPendingStepCompletion, models.PendingStepCompletionSignal{
					StepID: "step-1", Source: models.StepCompletionSourceAgent, Summary: "accepted earlier",
					SignaledAt: time.Now().UTC(),
				}))
			}
			notice := models.LastAgentError{
				Message: "Workflow step completion is pending", OccurredAt: time.Now().UTC(),
				Scope: models.ErrorScopeSession, Code: models.AgentErrorCodeStepSignalDeliveryFailed,
				StampValue: "signal-delivery:req-notice",
			}
			require.NoError(t, repo.SetSessionMetadataKey(ctx, "session-notice", models.SessionMetaKeyLastAgentError, notice))
			bus := &mcpRecordingEventBus{}
			h := newStepCompleteHandler(t, svc, repo, bus)

			msg := makeWSMessage(t, ws.ActionMCPStepComplete, map[string]interface{}{
				"task_id": "task-notice", "session_id": "session-notice", "summary": "resent",
			})
			resp, err := h.handleStepComplete(ctx, msg)
			require.NoError(t, err)
			require.NotNil(t, resp)
			var payload map[string]interface{}
			require.NoError(t, json.Unmarshal(resp.Payload, &payload))
			if tc.wantReason != "" {
				assert.Equal(t, tc.wantReason, payload["reason"])
			}

			session, err := repo.GetTaskSession(ctx, "session-notice")
			require.NoError(t, err)
			if tc.wantCleared {
				if _, ok := models.LoadLastAgentError(session.Metadata); ok {
					t.Fatal("accepted signal did not clear the completion-pending notice")
				}
			}
			found := false
			for _, event := range bus.events {
				if event.Type == "task_session.error_changed" {
					found = true
				}
			}
			if tc.wantCleared && !found {
				t.Fatal("clearing the notice must publish the existing session error surface event")
			}
		})
	}
}
