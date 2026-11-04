package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/kandev/kandev/internal/agentctl/types/streams"
	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/events/bus"
	"github.com/kandev/kandev/internal/task/models"
	"go.uber.org/zap"
)

// stepSignalDeliveryStampPrefix makes repeated deliveries of the same notice
// idempotent on the existing last-agent-error surface.
const stepSignalDeliveryStampPrefix = "signal-delivery:"

// handleSignalDeliveryFailureNoticeEvent surfaces one durable agentctl
// completion-pending notice on the existing session error breadcrumb. It adds
// no UI concept: the message states the workflow step is pending and names the
// agent's recovery path (resend the signal or reply in chat).
func (s *Service) handleSignalDeliveryFailureNoticeEvent(
	ctx context.Context,
	taskID, sessionID, executionID string,
	notice *streams.SignalDeliveryFailure,
) {
	if notice == nil || notice.RequestID == "" || sessionID == "" || s.repo == nil {
		return
	}
	session, err := s.repo.GetTaskSession(ctx, sessionID)
	if err != nil || session == nil {
		if err != nil {
			s.logger.Warn("failed to load session for signal delivery notice",
				zap.String("session_id", sessionID), zap.Error(err))
		}
		return
	}
	if _, pending := models.LoadPendingStepSignal(session.Metadata); pending {
		// A signal reached the backend after the notice was committed; the
		// pending bag is the newer truth and the notice would be stale.
		return
	}
	stamp := stepSignalDeliveryStampPrefix + notice.RequestID
	if existing, ok := models.LoadLastAgentError(session.Metadata); ok && existing.Stamp() == stamp {
		return
	}
	lastErr := models.LastAgentError{
		Message:     stepSignalDeliveryMessage(notice),
		OccurredAt:  time.Now().UTC(),
		Scope:       models.ErrorScopeSession,
		ExecutionID: executionID,
		Code:        models.AgentErrorCodeStepSignalDeliveryFailed,
		Details:     stepSignalDeliveryDetails(notice),
		StampValue:  stamp,
	}
	writeCtx := context.WithoutCancel(ctx)
	if err := s.repo.SetSessionMetadataKey(writeCtx, sessionID, models.SessionMetaKeyLastAgentError, lastErr); err != nil {
		s.logger.Warn("failed to persist signal delivery notice",
			zap.String("session_id", sessionID),
			zap.Error(err))
		return
	}
	s.publishStepSignalDeliveryErrorChanged(writeCtx, taskID, sessionID, lastErr)
}

func stepSignalDeliveryMessage(notice *streams.SignalDeliveryFailure) string {
	return fmt.Sprintf(
		"Workflow step completion is pending: the step_complete signal was not confirmed delivered after %d attempt(s) (reason: %s, first attempt: %s). Resend step_complete_kandev when this session reconnects, or reply in chat to continue this step.",
		notice.Attempts,
		notice.Reason,
		notice.FirstAttemptAt.UTC().Format(time.RFC3339),
	)
}

func stepSignalDeliveryDetails(notice *streams.SignalDeliveryFailure) string {
	return fmt.Sprintf(
		"request_id=%s action=%s reason=%s attempts=%d first_attempt_at=%s",
		notice.RequestID,
		notice.Action,
		notice.Reason,
		notice.Attempts,
		notice.FirstAttemptAt.UTC().Format(time.RFC3339),
	)
}

func (s *Service) publishStepSignalDeliveryErrorChanged(ctx context.Context, taskID, sessionID string, lastErr models.LastAgentError) {
	if s.eventBus == nil {
		return
	}
	eventData := map[string]interface{}{
		"task_id":     taskID,
		"session_id":  sessionID,
		"active":      true,
		"scope":       models.ErrorScopeSession,
		"message":     lastErr.Message,
		"occurred_at": lastErr.OccurredAt.Format(time.RFC3339Nano),
		"stamp":       lastErr.Stamp(),
		"code":        lastErr.Code,
		"details":     lastErr.Details,
	}
	if lastErr.ExecutionID != "" {
		eventData["execution_id"] = lastErr.ExecutionID
	}
	if err := s.eventBus.Publish(ctx, events.TaskSessionErrorChanged, bus.NewEvent(
		events.TaskSessionErrorChanged, "orchestrator", eventData,
	)); err != nil {
		s.logger.Warn("failed to publish signal delivery notice event",
			zap.String("task_id", taskID),
			zap.String("session_id", sessionID),
			zap.Error(err))
	}
}

// clearStepSignalDeliveryNotice removes the completion-pending breadcrumb once
// the workflow no longer needs it: an accepted signal, a consumed pending bag,
// a step change, or a fresh user message. It only clears its own code so a
// genuine agent failure is never erased.
func (s *Service) clearStepSignalDeliveryNotice(ctx context.Context, sessionID string) {
	if sessionID == "" || s.repo == nil {
		return
	}
	session, err := s.repo.GetTaskSession(ctx, sessionID)
	if err != nil || session == nil {
		return
	}
	lastErr, ok := models.LoadLastAgentError(session.Metadata)
	if !ok || lastErr.Code != models.AgentErrorCodeStepSignalDeliveryFailed {
		return
	}
	writeCtx := context.WithoutCancel(ctx)
	if err := s.repo.SetSessionMetadataKey(writeCtx, sessionID, models.SessionMetaKeyLastAgentError, nil); err != nil {
		s.logger.Debug("failed to clear signal delivery notice",
			zap.String("session_id", sessionID), zap.Error(err))
		return
	}
	if session.Metadata != nil {
		delete(session.Metadata, models.SessionMetaKeyLastAgentError)
	}
	if s.eventBus == nil {
		return
	}
	eventData := map[string]interface{}{
		"task_id":    session.TaskID,
		"session_id": sessionID,
		"active":     false,
		"stamp":      lastErr.Stamp(),
	}
	if err := s.eventBus.Publish(writeCtx, events.TaskSessionErrorChanged, bus.NewEvent(
		events.TaskSessionErrorChanged, "orchestrator", eventData,
	)); err != nil {
		s.logger.Warn("failed to publish signal delivery notice clearance",
			zap.String("session_id", sessionID), zap.Error(err))
	}
}
