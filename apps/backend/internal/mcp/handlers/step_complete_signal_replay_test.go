package handlers

import (
	"context"
	"encoding/json"
	"testing"

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
