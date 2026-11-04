package handlers

import (
	"context"

	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/events/bus"
	"github.com/kandev/kandev/internal/task/models"
	"go.uber.org/zap"
)

// sessionMetadataSetter is implemented by the SQLite repository. Handler
// fakes that omit it simply skip notice cleanup.
type sessionMetadataSetter interface {
	SetSessionMetadataKey(ctx context.Context, sessionID, key string, value interface{}) error
}

// clearStepSignalDeliveryNotice removes the completion-pending breadcrumb once
// a step_complete signal is accepted (including the already_signaled duplicate
// path, which proves the backend has the signal). It only clears its own code
// so a genuine agent failure is never erased.
func (h *Handlers) clearStepSignalDeliveryNotice(ctx context.Context, session *models.TaskSession) {
	if h == nil || session == nil || h.sessionRepo == nil {
		return
	}
	lastErr, ok := models.LoadLastAgentError(session.Metadata)
	if !ok || lastErr.Code != models.AgentErrorCodeStepSignalDeliveryFailed {
		return
	}
	setter, ok := h.sessionRepo.(sessionMetadataSetter)
	if !ok {
		return
	}
	writeCtx := context.WithoutCancel(ctx)
	if err := setter.SetSessionMetadataKey(writeCtx, session.ID, models.SessionMetaKeyLastAgentError, nil); err != nil {
		h.logger.Debug("failed to clear signal delivery notice",
			zap.String("session_id", session.ID), zap.Error(err))
		return
	}
	if session.Metadata != nil {
		delete(session.Metadata, models.SessionMetaKeyLastAgentError)
	}
	if h.eventBus == nil {
		return
	}
	eventData := map[string]interface{}{
		"task_id":    session.TaskID,
		"session_id": session.ID,
		"active":     false,
		"stamp":      lastErr.Stamp(),
	}
	if err := h.eventBus.Publish(writeCtx, events.TaskSessionErrorChanged, bus.NewEvent(
		events.TaskSessionErrorChanged, "mcp-handlers", eventData,
	)); err != nil {
		h.logger.Warn("failed to publish signal delivery notice clearance",
			zap.String("session_id", session.ID), zap.Error(err))
	}
}
