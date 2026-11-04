package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kandev/kandev/internal/agentctl/journal"
	"github.com/kandev/kandev/internal/common/agentstream"
	ws "github.com/kandev/kandev/pkg/websocket"
	"go.uber.org/zap"
)

// Stable codes carried by SignalDeliveryError so an agent can distinguish a
// capture failure from an ordinary backend error.
const (
	SignalCaptureUnavailableCode = "STEP_SIGNAL_CAPTURE_UNAVAILABLE"
	SignalCaptureFullCode        = "STEP_SIGNAL_CAPTURE_FULL"
	SignalCaptureInvalidCode     = "STEP_SIGNAL_CAPTURE_INVALID"
	SignalDeliveryPendingCode    = "STEP_SIGNAL_DELIVERY_PENDING"
)

// errPendingSignalStoreUnavailable reports that no durable capture store is
// wired. Capture never falls back to unbounded in-memory retention.
var errPendingSignalStoreUnavailable = errors.New("pending signal store is unavailable")

// signalDeliveredTimeout bounds the store write that records a confirmed
// delivery; the stream reader must not park on a journal lock indefinitely.
const signalDeliveredTimeout = 5 * time.Second

// SignalDeliveryScheduler owns the bounded delivery-window timers for captured
// signals. The process manager implements it so timer lifetime follows the
// instance's lifetime context rather than the stream connection.
type SignalDeliveryScheduler interface {
	ScheduleSignalDeliveryNotice(signalID string)
	CancelSignalDeliveryNotice(signalID string)
}

// SignalDeliveryError reports an idempotent workflow signal that was not
// captured or not confirmed delivered. Retryable failures are safe to retry:
// either nothing was sent, or the capture is retained and replayed.
type SignalDeliveryError struct {
	Code      string
	Message   string
	Retryable bool
	Err       error
}

func (e *SignalDeliveryError) Error() string {
	if e == nil {
		return "signal delivery error"
	}
	switch {
	case e.Code != "" && e.Message != "":
		return e.Code + ": " + e.Message
	case e.Message != "":
		return e.Message
	default:
		return e.Code
	}
}

func (e *SignalDeliveryError) Unwrap() error { return e.Err }

// idempotentSignalActions is the explicit allowlist of MCP actions that are
// safe to capture before publication and replay after a reconnect. Only
// actions whose backend handler deduplicates repeated deliveries belong here;
// adding an action requires proving it has no repeated external side effect.
var idempotentSignalActions = map[string]struct{}{
	ws.ActionMCPStepComplete: {},
}

func isIdempotentSignalAction(action string) bool {
	_, ok := idempotentSignalActions[action]
	return ok
}

// SetPendingSignalStore wires the durable capture store provider. The provider
// is resolved per operation so a configured but unavailable journal is
// reported at capture time instead of silently disabling capture.
func (c *ChannelBackendClient) SetPendingSignalStore(provider func() (journal.PendingSignalStore, error)) {
	c.signalStoreMu.Lock()
	defer c.signalStoreMu.Unlock()
	c.signalStore = provider
}

// SetSignalDeliveryScheduler wires the owner of the bounded delivery-window
// notice. Without it capture still works; only the completion-pending notice
// is skipped.
func (c *ChannelBackendClient) SetSignalDeliveryScheduler(scheduler SignalDeliveryScheduler) {
	c.signalStoreMu.Lock()
	defer c.signalStoreMu.Unlock()
	c.signalScheduler = scheduler
}

func (c *ChannelBackendClient) pendingSignalStore() (journal.PendingSignalStore, error) {
	c.signalStoreMu.RLock()
	provider := c.signalStore
	c.signalStoreMu.RUnlock()
	if provider == nil {
		return nil, errPendingSignalStoreUnavailable
	}
	return provider()
}

func (c *ChannelBackendClient) signalDeliveryScheduler() SignalDeliveryScheduler {
	c.signalStoreMu.RLock()
	defer c.signalStoreMu.RUnlock()
	return c.signalScheduler
}

// captureSignal durably records one idempotent signal before it is published.
// A capture failure refuses the send: the signal must never be delivered
// without the record that makes a lost response replayable.
func (c *ChannelBackendClient) captureSignal(
	ctx context.Context,
	id, action, sessionID string,
	payload json.RawMessage,
) error {
	store, err := c.pendingSignalStore()
	if err != nil {
		agentstream.RecordSignalCapture("store_unavailable")
		return &SignalDeliveryError{
			Code:      SignalCaptureUnavailableCode,
			Message:   fmt.Sprintf("%s was not sent because durable signal capture is unavailable; chat remains available", action),
			Retryable: true,
			Err:       err,
		}
	}
	signal := journal.PendingSignal{
		ID:        id,
		Action:    action,
		SessionID: sessionID,
		Payload:   payload,
		// The immediate send is the first delivery attempt; replays advance it.
		Attempts: 1,
	}
	if err := store.PutPendingSignal(ctx, signal); err != nil {
		code, outcome := classifySignalCaptureError(err)
		agentstream.RecordSignalCapture(outcome)
		return &SignalDeliveryError{
			Code:      code,
			Message:   fmt.Sprintf("%s was not sent because it could not be captured: %v", action, err),
			Retryable: true,
			Err:       err,
		}
	}
	agentstream.RecordSignalCapture("captured")
	if scheduler := c.signalDeliveryScheduler(); scheduler != nil {
		scheduler.ScheduleSignalDeliveryNotice(id)
	}
	return nil
}

func classifySignalCaptureError(err error) (code, outcome string) {
	switch {
	case errors.Is(err, journal.ErrPendingSignalStoreFull):
		return SignalCaptureFullCode, "store_full"
	case errors.Is(err, journal.ErrPendingSignalInvalid):
		return SignalCaptureInvalidCode, "store_invalid"
	default:
		return SignalCaptureUnavailableCode, "store_unavailable"
	}
}

// signalDeliveryFailure converts a transport failure after capture into a
// typed retryable error while leaving the capture pending for replay.
func signalDeliveryFailure(captured bool, action string, cause error) error {
	if !captured {
		return cause
	}
	return &SignalDeliveryError{
		Code: SignalDeliveryPendingCode,
		Message: fmt.Sprintf(
			"%s was captured but not confirmed delivered; it will be replayed after the agent stream reconnects. Retrying is safe because the backend deduplicates repeated signals",
			action,
		),
		Retryable: true,
		Err:       cause,
	}
}

// confirmSignalDelivered marks one captured record delivered after a backend
// response (success or typed terminal error) arrived for its request ID. It
// returns false when the ID was not a captured signal.
func (c *ChannelBackendClient) confirmSignalDelivered(id string) bool {
	store, err := c.pendingSignalStore()
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), signalDeliveredTimeout)
	defer cancel()
	err = store.MarkPendingSignalDelivered(ctx, id)
	switch {
	case err == nil:
		if scheduler := c.signalDeliveryScheduler(); scheduler != nil {
			scheduler.CancelSignalDeliveryNotice(id)
		}
		return true
	case errors.Is(err, journal.ErrPendingSignalNotFound):
		return false
	default:
		c.logger.Warn("failed to mark captured signal delivered",
			zap.String("request_id", id),
			zap.Error(err))
		return false
	}
}

// pendingSignalAttemptRecorder is the journal-only extension used by replay.
// Keeping it optional preserves small store fakes that only implement the
// PendingSignalStore contract.
type pendingSignalAttemptRecorder interface {
	IncrementPendingSignalAttempts(ctx context.Context, id string) error
}

// PendingSignalReplayBatch returns one bounded page of undelivered captured
// idempotent signals as wire requests carrying their original request IDs.
// The attempt counter advances for each request handed to the writer.
func (c *ChannelBackendClient) PendingSignalReplayBatch(ctx context.Context) ([]*ws.Message, error) {
	store, err := c.pendingSignalStore()
	if err != nil {
		agentstream.RecordSignalReplay("store_unavailable")
		return nil, err
	}
	signals, err := store.ListPendingSignals(ctx, journal.MaxPendingSignals)
	if err != nil {
		agentstream.RecordSignalReplay("store_unavailable")
		return nil, err
	}
	c.pendingMu.Lock()
	sessionID := c.sessionID
	c.pendingMu.Unlock()
	requests := make([]*ws.Message, 0, len(signals))
	for _, signal := range signals {
		if !isIdempotentSignalAction(signal.Action) {
			continue
		}
		if signal.SessionID != "" && sessionID != "" && signal.SessionID != sessionID {
			continue
		}
		request, err := ws.NewRequest(signal.ID, signal.Action, json.RawMessage(signal.Payload))
		if err != nil {
			c.logger.Warn("failed to build captured signal replay",
				zap.String("request_id", signal.ID),
				zap.Error(err))
			continue
		}
		if recorder, ok := store.(pendingSignalAttemptRecorder); ok {
			if err := recorder.IncrementPendingSignalAttempts(ctx, signal.ID); err != nil {
				c.logger.Debug("failed to record captured signal replay attempt",
					zap.String("request_id", signal.ID),
					zap.Error(err))
			}
		}
		agentstream.RecordSignalReplay("replayed")
		requests = append(requests, request)
	}
	return requests, nil
}
