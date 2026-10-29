package service

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/notifications/models"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	"go.uber.org/zap"
)

// relayedService builds a service whose only delivery surface is a capture
// provider subscribed to the given events, with a task getter that would
// resolve a DIFFERENT owner than the relay recipient: local task→owner
// resolution must never run for relayed occurrences.
func relayedService(t *testing.T, events ...string) (*Service, *captureProvider, *notificationTestRepository) {
	t.Helper()
	t.Setenv(desktopNativeNotificationsEnv, "true")
	log, err := logger.NewFromZap(zap.NewNop())
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	repo := &notificationTestRepository{
		providers: []*models.Provider{{ID: "provider-1", UserID: "instance-owner", Type: models.ProviderTypeLocal, Enabled: true}},
		subscriptions: map[string][]*models.Subscription{
			"provider-1": subscriptionList("provider-1", "instance-owner", events),
		},
	}
	// The workspace owner a local resolution would pick is another user
	// entirely; delivering to it would prove the recipient was derived.
	service := NewService(repo, notificationTestTaskGetter{
		task:      &taskmodels.Task{Title: "Local task", WorkspaceID: "workspace-1"},
		workspace: &taskmodels.Workspace{ID: "workspace-1", OwnerID: "someone-else"},
	}, nil, log, nil)
	capture := &captureProvider{}
	service.providers[models.ProviderTypeLocal] = capture
	return service, capture, repo
}

func subscriptionList(providerID, userID string, events []string) []*models.Subscription {
	subs := make([]*models.Subscription, 0, len(events))
	for _, eventType := range events {
		subs = append(subs, &models.Subscription{ProviderID: providerID, UserID: userID, EventType: eventType, Enabled: true})
	}
	return subs
}

func relayedClarification() (string, string, string, string, string, string, string, string) {
	return "instance-owner", "inst-1", "Remote Studio", EventTaskSessionClarificationAsked,
		"remote-task", "remote-session", "pending-1", "Agent needs your answer"
}

// TestHandleRelayedOccurrenceDeliversToExplicitRecipientWithOrigin pins the
// relayed delivery contract: the recipient is the instance owner (never the
// locally resolved workspace owner), the message carries the origin
// identity, and the occurrence is never recorded into the local history a
// federating peer reconciles against.
func TestHandleRelayedOccurrenceDeliversToExplicitRecipientWithOrigin(t *testing.T) {
	service, capture, repo := relayedService(t, EventTaskSessionClarificationAsked)
	userID, originID, originName, eventType, taskID, sessionID, occurrenceID, title := relayedClarification()

	service.HandleRelayedOccurrence(context.Background(), userID, originID, originName, eventType, taskID, sessionID, occurrenceID, title, `The agent asked a question on "Remote task".`)

	if len(capture.messages) != 1 {
		t.Fatalf("relayed delivery = %#v, want exactly one message", capture.messages)
	}
	message := capture.messages[0]
	if message.EventType != EventTaskSessionClarificationAsked || message.OccurrenceID != occurrenceID {
		t.Fatalf("relayed message identity = %#v", message)
	}
	if message.OriginInstanceID != originID || message.OriginInstanceName != originName {
		t.Fatalf("relayed origin = (%q, %q), want (%q, %q)", message.OriginInstanceID, message.OriginInstanceName, originID, originName)
	}
	if message.Title != title || message.Body != `The agent asked a question on "Remote task".` {
		t.Fatalf("remote copy must win: %#v", message)
	}
	if len(repo.occurrences) != 0 {
		t.Fatalf("relayed occurrence wrote local history rows: %#v, want none", repo.occurrences)
	}
	if len(repo.deliveries) != 1 {
		t.Fatalf("delivery claims = %#v, want one", repo.deliveries)
	}
}

// TestHandleRelayedOccurrenceDedupsReplays pins at-most-once delivery for a
// repeated observation: the delivery claim keyed on (provider, event,
// occurrence) collapses the replay into one provider dispatch.
func TestHandleRelayedOccurrenceDedupsReplays(t *testing.T) {
	service, capture, _ := relayedService(t, EventTaskSessionClarificationAsked)
	userID, originID, originName, eventType, taskID, sessionID, occurrenceID, title := relayedClarification()

	service.HandleRelayedOccurrence(context.Background(), userID, originID, originName, eventType, taskID, sessionID, occurrenceID, title, "body")
	service.HandleRelayedOccurrence(context.Background(), userID, originID, originName, eventType, taskID, sessionID, occurrenceID, title, "body")

	if len(capture.messages) != 1 {
		t.Fatalf("replayed relayed occurrence delivered %#v, want exactly once", capture.messages)
	}
}

// TestHandleRelayedOccurrenceHonorsSubscriptions pins equal treatment: an
// unsubscribed kind stays quiet when relayed, exactly like a local event of
// that kind.
func TestHandleRelayedOccurrenceHonorsSubscriptions(t *testing.T) {
	service, capture, _ := relayedService(t, EventTaskSessionClarificationAsked)
	userID, originID, originName, _, taskID, sessionID, occurrenceID, title := relayedClarification()

	service.HandleRelayedOccurrence(context.Background(), userID, originID, originName, EventTaskSessionTurnFinished, taskID, sessionID, occurrenceID, title, "body")

	if len(capture.messages) != 0 {
		t.Fatalf("unsubscribed relayed kind delivered %#v, want silence", capture.messages)
	}
}

// TestHandleRelayedOccurrenceFallsBackToLocalCopy pins the degraded-payload
// path: an empty remote title or body falls back to the local copy for the
// event kind, including the office inbox item kind, which has no entry in
// semanticMessageCopy.
func TestHandleRelayedOccurrenceFallsBackToLocalCopy(t *testing.T) {
	service, capture, _ := relayedService(t,
		EventTaskSessionClarificationAsked,
		EventTaskManualStepEntered,
		EventOfficeInboxItem,
		EventTaskSessionTurnFinished,
	)
	userID, originID, originName, _, taskID, sessionID, _, _ := relayedClarification()

	cases := []struct {
		eventType string
		title     string
		body      string
	}{
		{eventType: EventTaskSessionClarificationAsked, title: "Agent needs your answer", body: "The agent asked a question."},
		{eventType: EventTaskManualStepEntered, title: "Task waiting on you", body: "A task moved to a step that waits for you."},
		{eventType: EventOfficeInboxItem, title: "New inbox item", body: "A new item needs your attention."},
		{eventType: EventTaskSessionTurnFinished, title: "Agent turn finished", body: "The agent finished a turn."},
	}
	for _, tc := range cases {
		service.HandleRelayedOccurrence(context.Background(), userID, originID, originName, tc.eventType, taskID, sessionID, tc.eventType+"-occurrence", "", "")
	}
	if len(capture.messages) != len(cases) {
		t.Fatalf("fallback deliveries = %#v, want one per kind", capture.messages)
	}
	for index, tc := range cases {
		message := capture.messages[index]
		if message.Title != tc.title || message.Body != tc.body {
			t.Fatalf("%s fallback copy = (%q, %q), want (%q, %q)", tc.eventType, message.Title, message.Body, tc.title, tc.body)
		}
	}
}

// TestHandleRelayedOccurrenceIgnoresUnidentifiedOccurrences mirrors the
// relay's own gate: no recipient or no occurrence identity means no delivery.
func TestHandleRelayedOccurrenceIgnoresUnidentifiedOccurrences(t *testing.T) {
	service, capture, repo := relayedService(t, EventTaskSessionClarificationAsked)
	_, originID, originName, eventType, taskID, sessionID, occurrenceID, title := relayedClarification()

	service.HandleRelayedOccurrence(context.Background(), "", originID, originName, eventType, taskID, sessionID, occurrenceID, title, "body")
	service.HandleRelayedOccurrence(context.Background(), "instance-owner", originID, originName, eventType, taskID, sessionID, "", title, "body")

	if len(capture.messages) != 0 || len(repo.deliveries) != 0 {
		t.Fatalf("unidentified relayed occurrences delivered %#v claims=%#v, want none", capture.messages, repo.deliveries)
	}
}
