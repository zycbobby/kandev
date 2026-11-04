package process

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agentctl/journal"
	"github.com/kandev/kandev/internal/agentctl/server/config"
	"github.com/kandev/kandev/internal/agentctl/types/streams"
	"github.com/kandev/kandev/internal/common/agentstream"
)

func newSignalNoticeTestManager(t *testing.T) (*Manager, *journal.Journal) {
	t.Helper()
	cfg := &config.InstanceConfig{
		WorkDir:            t.TempDir(),
		SessionID:          "session-notice",
		DurableJournalPath: filepath.Join(t.TempDir(), "delivery.bbolt"),
	}
	manager := NewManager(cfg, newTestLogger(t))
	t.Cleanup(func() { _ = manager.StopForTeardown(context.Background()) })
	deliveryJournal, err := manager.DeliveryJournal()
	if err != nil {
		t.Fatalf("open delivery journal: %v", err)
	}
	return manager, deliveryJournal
}

func waitForNoticeEvents(t *testing.T, deliveryJournal *journal.Journal, streamID string) []journal.Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events, _, err := deliveryJournal.Replay(context.Background(), streamID, 0, 16)
		if err != nil && !errors.Is(err, journal.ErrStreamNotFound) {
			t.Fatalf("replay notice events: %v", err)
		}
		notices := make([]journal.Event, 0, len(events))
		for _, event := range events {
			if event.Type == streams.EventTypeSignalDeliveryFailed {
				notices = append(notices, event)
			}
		}
		if len(notices) > 0 {
			return notices
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for a signal delivery notice")
	return nil
}

func TestSignalDeliveryNoticeEmittedOnceWithoutPayload(t *testing.T) {
	manager, deliveryJournal := newSignalNoticeTestManager(t)
	const signalID = "req-notice"
	if err := deliveryJournal.PutPendingSignal(context.Background(), journal.PendingSignal{
		ID: signalID, Action: "mcp.step_complete", SessionID: "session-notice",
		Payload: json.RawMessage(`{"summary":"secret-signal-payload"}`), Attempts: 2,
	}); err != nil {
		t.Fatalf("seed captured signal: %v", err)
	}
	manager.signalDeliveryWindow = 10 * time.Millisecond

	manager.ScheduleSignalDeliveryNotice(signalID, time.Now().UTC())
	notices := waitForNoticeEvents(t, deliveryJournal, manager.DeliveryStreamID())
	if len(notices) != 1 {
		t.Fatalf("notice events = %d, want 1", len(notices))
	}
	payload := string(notices[0].Payload)
	if strings.Contains(payload, "secret-signal-payload") {
		t.Fatalf("notice leaked the captured payload: %s", payload)
	}
	var event streams.AgentEvent
	if err := json.Unmarshal(notices[0].Payload, &event); err != nil {
		t.Fatalf("decode notice event: %v", err)
	}
	notice := event.SignalDeliveryFailure
	if notice == nil {
		t.Fatalf("notice event missing typed notice: %s", payload)
	}
	if notice.RequestID != signalID || notice.Action != "mcp.step_complete" ||
		notice.SessionID != "session-notice" || notice.Attempts != 2 ||
		notice.Reason != streams.SignalDeliveryFailureReasonWindowExceeded || notice.FirstAttemptAt.IsZero() {
		t.Fatalf("notice = %+v", notice)
	}

	// Re-arming the same capture (replay/restart shape) must not emit twice.
	manager.ScheduleSignalDeliveryNotice(signalID, time.Now().UTC())
	time.Sleep(40 * time.Millisecond)
	events, _, err := deliveryJournal.Replay(context.Background(), manager.DeliveryStreamID(), 0, 16)
	if err != nil && !errors.Is(err, journal.ErrStreamNotFound) {
		t.Fatalf("replay after re-arm: %v", err)
	}
	count := 0
	for _, event := range events {
		if event.Type == streams.EventTypeSignalDeliveryFailed {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("notice events after re-arm = %d, want 1", count)
	}
}

func TestSignalDeliveryNoticeCanceledByDelivery(t *testing.T) {
	manager, deliveryJournal := newSignalNoticeTestManager(t)
	const signalID = "req-delivered"
	if err := deliveryJournal.PutPendingSignal(context.Background(), journal.PendingSignal{
		ID: signalID, Action: "mcp.step_complete", SessionID: "session-notice",
		Payload: json.RawMessage(`{"summary":"done"}`), Attempts: 1,
	}); err != nil {
		t.Fatalf("seed captured signal: %v", err)
	}
	manager.signalDeliveryWindow = 10 * time.Millisecond
	manager.ScheduleSignalDeliveryNotice(signalID, time.Now().UTC())
	if err := deliveryJournal.MarkPendingSignalDelivered(context.Background(), signalID); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	manager.CancelSignalDeliveryNotice(signalID)
	time.Sleep(40 * time.Millisecond)

	events, _, err := deliveryJournal.Replay(context.Background(), manager.DeliveryStreamID(), 0, 16)
	if err != nil && !errors.Is(err, journal.ErrStreamNotFound) {
		t.Fatalf("replay events: %v", err)
	}
	for _, event := range events {
		if event.Type == streams.EventTypeSignalDeliveryFailed {
			t.Fatalf("delivered signal produced a notice: %s", event.Payload)
		}
	}
}

func TestSignalDeliveryWindowDefaultsToTransportConstant(t *testing.T) {
	if agentstream.SignalDeliveryWindow != 60*time.Second {
		t.Fatalf("SignalDeliveryWindow = %s, want 60s", agentstream.SignalDeliveryWindow)
	}
}
