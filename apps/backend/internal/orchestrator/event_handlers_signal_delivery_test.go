package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agent/runtime/lifecycle"
	"github.com/kandev/kandev/internal/agentctl/types/streams"
	"github.com/kandev/kandev/internal/events"
	"github.com/kandev/kandev/internal/task/models"
)

func newSignalDeliveryNotice() *streams.SignalDeliveryFailure {
	return &streams.SignalDeliveryFailure{
		RequestID:      "req-notice",
		Action:         "mcp.step_complete",
		SessionID:      "s1",
		Reason:         streams.SignalDeliveryFailureReasonWindowExceeded,
		Attempts:       2,
		FirstAttemptAt: time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC),
	}
}

func signalDeliveryPayload(notice *streams.SignalDeliveryFailure) *lifecycle.AgentStreamEventPayload {
	return &lifecycle.AgentStreamEventPayload{
		Type:        "agent/event",
		TaskID:      "t1",
		SessionID:   "s1",
		ExecutionID: "exec-1",
		Data: &lifecycle.AgentStreamEventData{
			Type:                  streams.EventTypeSignalDeliveryFailed,
			SignalDeliveryFailure: notice,
		},
	}
}

func callSignalDeliveryNoticeHandler(ctx context.Context, svc *Service, payload *lifecycle.AgentStreamEventPayload) {
	svc.handleSignalDeliveryFailureNoticeEvent(ctx, payload.TaskID, payload.SessionID, payload.ExecutionID, payload.Data.SignalDeliveryFailure)
}

func TestSignalDeliveryNoticeSurfacesCompletionPending(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")
	eventBus := &recordingEventBus{}
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	svc.eventBus = eventBus

	svc.handleAgentStreamEvent(ctx, signalDeliveryPayload(newSignalDeliveryNotice()))

	session, err := repo.GetTaskSession(ctx, "s1")
	if err != nil || session == nil {
		t.Fatalf("load session: %v", err)
	}
	lastErr, ok := models.LoadLastAgentError(session.Metadata)
	if !ok {
		t.Fatalf("completion-pending notice missing from session metadata: %#v", session.Metadata)
	}
	if lastErr.Code != models.AgentErrorCodeStepSignalDeliveryFailed {
		t.Fatalf("notice code = %q", lastErr.Code)
	}
	if !strings.Contains(lastErr.Message, "Workflow step completion is pending") ||
		!strings.Contains(lastErr.Message, string(streams.SignalDeliveryFailureReasonWindowExceeded)) ||
		!strings.Contains(lastErr.Message, "2 attempt") {
		t.Fatalf("notice message does not expose reason/attempts: %q", lastErr.Message)
	}
	if strings.Contains(lastErr.Message, "secret") || strings.Contains(lastErr.Details, "secret") {
		t.Fatalf("notice leaked signal payload: %q / %q", lastErr.Message, lastErr.Details)
	}
	if !strings.Contains(lastErr.Details, "request_id=req-notice") {
		t.Fatalf("notice details missing request identity: %q", lastErr.Details)
	}
	found := false
	for _, event := range eventBus.events {
		if event.event.Type == events.TaskSessionErrorChanged {
			found = true
		}
	}
	if !found {
		t.Fatal("notice did not publish the existing session error surface event")
	}

	// Replaying the same notice must not create a second error record.
	before := lastErr.Stamp()
	svc.handleAgentStreamEvent(ctx, signalDeliveryPayload(newSignalDeliveryNotice()))
	session, _ = repo.GetTaskSession(ctx, "s1")
	again, ok := models.LoadLastAgentError(session.Metadata)
	if !ok || again.Stamp() != before {
		t.Fatalf("duplicate notice changed the error stamp: %q -> %q", before, again.Stamp())
	}
}

func TestSignalDeliveryNoticeClearedByAcceptedSignal(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	svc.eventBus = &recordingEventBus{}

	callSignalDeliveryNoticeHandler(ctx, svc, signalDeliveryPayload(newSignalDeliveryNotice()))
	if _, ok := models.LoadLastAgentError(mustSession(t, repo, "s1").Metadata); !ok {
		t.Fatal("notice was not persisted")
	}

	// Accepting the signal writes the pending bag; the subsequent clear (as
	// performed by the MCP handler or a consumed transition) removes the notice.
	if err := repo.SetSessionMetadataKey(ctx, "s1", models.SessionMetaKeyPendingStepCompletion, models.PendingStepCompletionSignal{
		StepID: "step1", Source: models.StepCompletionSourceAgent, Summary: "done", SignaledAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed accepted signal: %v", err)
	}
	svc.clearStepSignalDeliveryNotice(ctx, "s1")
	if _, ok := models.LoadLastAgentError(mustSession(t, repo, "s1").Metadata); ok {
		t.Fatal("accepted signal did not clear the completion-pending notice")
	}
}

func TestSignalDeliveryNoticeClearedByChatFallback(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	svc.eventBus = &recordingEventBus{}

	callSignalDeliveryNoticeHandler(ctx, svc, signalDeliveryPayload(newSignalDeliveryNotice()))
	session := mustSession(t, repo, "s1")
	// A fresh user message takes the same path that clears a stale pending bag.
	svc.clearPendingStepSignal(ctx, session)
	if _, ok := models.LoadLastAgentError(mustSession(t, repo, "s1").Metadata); ok {
		t.Fatal("chat fallback did not clear the completion-pending notice")
	}
}

func TestSignalDeliveryNoticeSkippedWhenSignalAlreadyPending(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")
	svc := createTestService(repo, newMockStepGetter(), newMockTaskRepo())
	if err := repo.SetSessionMetadataKey(ctx, "s1", models.SessionMetaKeyPendingStepCompletion, models.PendingStepCompletionSignal{
		StepID: "step1", Source: models.StepCompletionSourceAgent, Summary: "accepted first", SignaledAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed pending signal: %v", err)
	}

	callSignalDeliveryNoticeHandler(ctx, svc, signalDeliveryPayload(newSignalDeliveryNotice()))
	if _, ok := models.LoadLastAgentError(mustSession(t, repo, "s1").Metadata); ok {
		t.Fatal("a notice arriving after an accepted signal must not surface completion-pending")
	}
}

func mustSession(t *testing.T, repo interface {
	GetTaskSession(context.Context, string) (*models.TaskSession, error)
}, sessionID string) *models.TaskSession {
	t.Helper()
	session, err := repo.GetTaskSession(context.Background(), sessionID)
	if err != nil || session == nil {
		t.Fatalf("load session %q: %v", sessionID, err)
	}
	return session
}
