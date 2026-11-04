package process

import (
	"context"
	"time"

	"github.com/kandev/kandev/internal/agentctl/journal"
	"github.com/kandev/kandev/internal/agentctl/server/adapter"
	"github.com/kandev/kandev/internal/agentctl/types/streams"
	"github.com/kandev/kandev/internal/common/agentstream"
	"go.uber.org/zap"
)

const signalNoticeLookupTimeout = 5 * time.Second

// ScheduleSignalDeliveryNotice arms the bounded delivery-window notice for one
// captured signal. The deadline is derived from the record's capture time, so a
// replayed or restarted record that is already past the window fires
// immediately. The timer reads the journal when it fires; it never holds the
// payload in memory. Scheduling is idempotent per signal ID.
func (m *Manager) ScheduleSignalDeliveryNotice(signalID string, createdAt time.Time) {
	if m == nil || signalID == "" {
		return
	}
	window := m.signalDeliveryWindow
	if window <= 0 {
		window = agentstream.SignalDeliveryWindow
	}
	remaining := window
	if !createdAt.IsZero() {
		remaining = window - time.Since(createdAt)
	}
	if remaining <= 0 {
		remaining = time.Millisecond
	}
	m.signalNoticeMu.Lock()
	defer m.signalNoticeMu.Unlock()
	if m.lifetimeCtx != nil && m.lifetimeCtx.Err() != nil {
		return
	}
	if m.signalNoticeTimers == nil {
		m.signalNoticeTimers = make(map[string]*time.Timer)
	}
	if existing := m.signalNoticeTimers[signalID]; existing != nil {
		existing.Stop()
	}
	m.signalNoticeTimers[signalID] = time.AfterFunc(remaining, func() {
		m.emitSignalDeliveryNotice(signalID)
	})
}

// CancelSignalDeliveryNotice stops the pending notice after delivery was
// confirmed. A late timer callback observes the missing entry and does nothing.
func (m *Manager) CancelSignalDeliveryNotice(signalID string) {
	if m == nil || signalID == "" {
		return
	}
	m.signalNoticeMu.Lock()
	timer := m.signalNoticeTimers[signalID]
	delete(m.signalNoticeTimers, signalID)
	m.signalNoticeMu.Unlock()
	if timer != nil {
		timer.Stop()
	}
}

func (m *Manager) stopSignalDeliveryNotices() {
	if m == nil {
		return
	}
	m.signalNoticeMu.Lock()
	timers := m.signalNoticeTimers
	m.signalNoticeTimers = nil
	m.signalNoticeMu.Unlock()
	for _, timer := range timers {
		timer.Stop()
	}
}

// emitSignalDeliveryNotice runs once per armed capture. The notified marker is
// claimed atomically before the notice is committed, so a replay or restart
// cannot emit a second notice for the same request.
func (m *Manager) emitSignalDeliveryNotice(signalID string) {
	m.signalNoticeMu.Lock()
	_, armed := m.signalNoticeTimers[signalID]
	delete(m.signalNoticeTimers, signalID)
	m.signalNoticeMu.Unlock()
	if !armed {
		return
	}
	if m.lifetimeCtx != nil && m.lifetimeCtx.Err() != nil {
		return
	}
	deliveryJournal, err := m.DeliveryJournal()
	if err != nil {
		m.logger.Debug("signal delivery notice skipped: journal unavailable",
			zap.String("request_id", signalID), zap.Error(err))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), signalNoticeLookupTimeout)
	defer cancel()
	signal, found := findPendingSignal(ctx, deliveryJournal, signalID)
	if !found {
		// Delivered, expired, or already pruned: no completion is pending.
		return
	}
	marked, err := deliveryJournal.MarkPendingSignalNotified(ctx, signalID)
	if err != nil {
		m.logger.Warn("failed to persist signal delivery notice marker",
			zap.String("request_id", signalID), zap.Error(err))
		return
	}
	if !marked {
		return
	}
	reason := streams.SignalDeliveryFailureReasonWindowExceeded
	agentstream.RecordSignalDeliveryFailed(string(reason))
	m.PublishSignalDeliveryFailure(streams.SignalDeliveryFailure{
		RequestID:      signal.ID,
		Action:         signal.Action,
		SessionID:      signal.SessionID,
		Reason:         reason,
		Attempts:       signal.Attempts,
		FirstAttemptAt: signal.CreatedAt,
	})
}

func findPendingSignal(ctx context.Context, deliveryJournal *journal.Journal, signalID string) (journal.PendingSignal, bool) {
	signals, err := deliveryJournal.ListPendingSignals(ctx, journal.MaxPendingSignals)
	if err != nil {
		return journal.PendingSignal{}, false
	}
	for _, signal := range signals {
		if signal.ID == signalID {
			return signal, true
		}
	}
	return journal.PendingSignal{}, false
}

// PublishSignalDeliveryFailure commits one durable completion-pending notice
// through the normal agent-event pipeline. Journal replay delivers it after a
// reconnect; the updates channel is only a wakeup for the attached writer.
func (m *Manager) PublishSignalDeliveryFailure(notice streams.SignalDeliveryFailure) {
	event := adapter.AgentEvent{
		Type:                  streams.EventTypeSignalDeliveryFailed,
		SessionID:             notice.SessionID,
		SignalDeliveryFailure: &notice,
	}
	persisted, err := m.persistDeliveryEvent(event)
	if err != nil {
		m.logger.Warn("failed to commit signal delivery failure notice",
			zap.String("request_id", notice.RequestID),
			zap.Error(err))
		return
	}
	if m.sendUpdateNonBlockingRecorded(persisted) {
		return
	}
	m.logger.Warn("updates channel full, dropping signal delivery failure notice",
		zap.String("request_id", notice.RequestID))
}
