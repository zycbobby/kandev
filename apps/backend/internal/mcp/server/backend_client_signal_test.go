package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agentctl/journal"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
)

// fakePendingSignalStore is a bounded in-memory stand-in for the journal
// store. The journal implementation is exercised by its own package tests;
// these tests cover the client's capture/replay contract.
type fakePendingSignalStore struct {
	mu            sync.Mutex
	signals       map[string]journal.PendingSignal
	putErr        error
	putCalls      int
	delivered     []string
	incrementErrs int
}

func newFakePendingSignalStore() *fakePendingSignalStore {
	return &fakePendingSignalStore{signals: make(map[string]journal.PendingSignal)}
}

func (s *fakePendingSignalStore) PutPendingSignal(_ context.Context, signal journal.PendingSignal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putCalls++
	if s.putErr != nil {
		return s.putErr
	}
	if _, exists := s.signals[signal.ID]; exists {
		return nil
	}
	if signal.CreatedAt.IsZero() {
		signal.CreatedAt = time.Now().UTC()
	}
	s.signals[signal.ID] = signal
	return nil
}

func (s *fakePendingSignalStore) ListPendingSignals(_ context.Context, limit int) ([]journal.PendingSignal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]journal.PendingSignal, 0, len(s.signals))
	for _, signal := range s.signals {
		if signal.DeliveredAt != nil {
			continue
		}
		out = append(out, signal)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *fakePendingSignalStore) MarkPendingSignalDelivered(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	signal, ok := s.signals[id]
	if !ok {
		return journal.ErrPendingSignalNotFound
	}
	if signal.DeliveredAt != nil {
		return nil
	}
	now := time.Now().UTC()
	signal.DeliveredAt = &now
	s.signals[id] = signal
	s.delivered = append(s.delivered, id)
	return nil
}

func (s *fakePendingSignalStore) DeletePendingSignal(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.signals[id]; !ok {
		return journal.ErrPendingSignalNotFound
	}
	delete(s.signals, id)
	return nil
}

func (s *fakePendingSignalStore) IncrementPendingSignalAttempts(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	signal, ok := s.signals[id]
	if !ok {
		s.incrementErrs++
		return journal.ErrPendingSignalNotFound
	}
	signal.Attempts++
	s.signals[id] = signal
	return nil
}

func (s *fakePendingSignalStore) get(id string) (journal.PendingSignal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	signal, ok := s.signals[id]
	return signal, ok
}

func (s *fakePendingSignalStore) deliveredIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.delivered...)
}

type scheduledNotice struct {
	id        string
	createdAt time.Time
}

type fakeSignalScheduler struct {
	mu        sync.Mutex
	scheduled []scheduledNotice
	canceled  []string
}

func (s *fakeSignalScheduler) ScheduleSignalDeliveryNotice(signalID string, createdAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scheduled = append(s.scheduled, scheduledNotice{id: signalID, createdAt: createdAt})
}

func (s *fakeSignalScheduler) CancelSignalDeliveryNotice(signalID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.canceled = append(s.canceled, signalID)
}

func (s *fakeSignalScheduler) snapshot() (scheduled []scheduledNotice, canceled []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]scheduledNotice(nil), s.scheduled...), append([]string(nil), s.canceled...)
}

func scheduledNoticeIDs(notices []scheduledNotice) []string {
	ids := make([]string, len(notices))
	for i, notice := range notices {
		ids[i] = notice.id
	}
	return ids
}

func newSignalTestClient(t *testing.T, store journal.PendingSignalStore, scheduler SignalDeliveryScheduler) *ChannelBackendClient {
	t.Helper()
	client := NewChannelBackendClient(nil)
	t.Cleanup(client.Close)
	client.SetSessionID("session-signal")
	client.SetPendingSignalStore(func() (journal.PendingSignalStore, error) { return store, nil })
	if scheduler != nil {
		client.SetSignalDeliveryScheduler(scheduler)
	}
	return client
}

func TestStepCompleteSignalCapturedBeforePublish(t *testing.T) {
	store := newFakePendingSignalStore()
	scheduler := &fakeSignalScheduler{}
	client := newSignalTestClient(t, store, scheduler)
	payload := map[string]any{"task_id": "task-1", "session_id": "session-signal", "summary": "done"}

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.RequestPayload(context.Background(), ws.ActionMCPStepComplete, payload, nil)
	}()

	var request *ws.Message
	select {
	case request = <-client.GetRequestChannel():
	case <-time.After(time.Second):
		t.Fatal("step_complete was not published")
	}
	captured, ok := store.get(request.ID)
	require.True(t, ok, "signal must be captured before it is published")
	require.Equal(t, ws.ActionMCPStepComplete, captured.Action)
	require.Equal(t, "session-signal", captured.SessionID)
	require.JSONEq(t, string(request.Payload), string(captured.Payload))
	require.Equal(t, 1, captured.Attempts)

	scheduled, _ := scheduler.snapshot()
	require.Equal(t, []string{request.ID}, scheduledNoticeIDs(scheduled))

	response, err := ws.NewResponse(request.ID, request.Action, map[string]any{"accepted": true})
	require.NoError(t, err)
	client.HandleResponse(response)
	require.NoError(t, <-errCh)

	require.Equal(t, []string{request.ID}, store.deliveredIDs())
	_, canceled := scheduler.snapshot()
	require.Equal(t, []string{request.ID}, canceled)
}

func TestStepCompleteSignalCaptureUnavailableReturnsTypedError(t *testing.T) {
	providerErr := errors.New("journal unavailable")
	client := NewChannelBackendClient(nil)
	t.Cleanup(client.Close)
	client.SetSessionID("session-signal")
	client.SetPendingSignalStore(func() (journal.PendingSignalStore, error) { return nil, providerErr })

	err := client.RequestPayload(context.Background(), ws.ActionMCPStepComplete, map[string]any{"summary": "done"}, nil)
	var deliveryErr *SignalDeliveryError
	require.ErrorAs(t, err, &deliveryErr)
	require.Equal(t, SignalCaptureUnavailableCode, deliveryErr.Code)
	require.True(t, deliveryErr.Retryable)
	require.ErrorIs(t, err, providerErr)

	select {
	case request := <-client.GetRequestChannel():
		t.Fatalf("uncaptured request %q was published", request.ID)
	default:
	}
}

func TestStepCompleteSignalCaptureFullReturnsTypedError(t *testing.T) {
	store := newFakePendingSignalStore()
	store.putErr = journal.ErrPendingSignalStoreFull
	client := newSignalTestClient(t, store, nil)

	err := client.RequestPayload(context.Background(), ws.ActionMCPStepComplete, map[string]any{"summary": "done"}, nil)
	var deliveryErr *SignalDeliveryError
	require.ErrorAs(t, err, &deliveryErr)
	require.Equal(t, SignalCaptureFullCode, deliveryErr.Code)
	require.ErrorIs(t, err, journal.ErrPendingSignalStoreFull)
}

func TestNonIdempotentMCPActionIsNotCaptured(t *testing.T) {
	store := newFakePendingSignalStore()
	client := newSignalTestClient(t, store, nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.RequestPayload(context.Background(), "mcp.tools.call", map[string]any{"name": "x"}, nil)
	}()
	request := <-client.GetRequestChannel()
	response, err := ws.NewResponse(request.ID, request.Action, nil)
	require.NoError(t, err)
	client.HandleResponse(response)
	require.NoError(t, <-errCh)

	require.Zero(t, store.putCalls, "non-idempotent tools must not enter the capture store")
}

func TestStepCompleteSignalSendFailureKeepsCapturePending(t *testing.T) {
	store := newFakePendingSignalStore()
	scheduler := &fakeSignalScheduler{}
	client := newSignalTestClient(t, store, scheduler)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := client.RequestPayload(ctx, ws.ActionMCPStepComplete, map[string]any{"summary": "done"}, nil)
	var deliveryErr *SignalDeliveryError
	require.ErrorAs(t, err, &deliveryErr)
	require.Equal(t, SignalDeliveryPendingCode, deliveryErr.Code)
	require.True(t, deliveryErr.Retryable)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	store.mu.Lock()
	require.Len(t, store.signals, 1, "capture must survive the failed send")
	var pendingID string
	for id := range store.signals {
		pendingID = id
	}
	store.mu.Unlock()

	_, canceled := scheduler.snapshot()
	require.Empty(t, canceled, "a failed send must not cancel the notice timer")

	late, err := ws.NewResponse(pendingID, ws.ActionMCPStepComplete, map[string]any{"accepted": true})
	require.NoError(t, err)
	client.HandleResponse(late)
	require.Equal(t, []string{pendingID}, store.deliveredIDs())
	_, canceled = scheduler.snapshot()
	require.Equal(t, []string{pendingID}, canceled)
}

func TestStepCompleteSignalReplayUsesOriginalIdentity(t *testing.T) {
	store := newFakePendingSignalStore()
	client := newSignalTestClient(t, store, nil)
	payload := json.RawMessage(`{"task_id":"task-1","session_id":"session-signal","summary":"done"}`)
	require.NoError(t, store.PutPendingSignal(context.Background(), journal.PendingSignal{
		ID: "req-original", Action: ws.ActionMCPStepComplete, SessionID: "session-signal", Payload: payload, Attempts: 1,
	}))
	require.NoError(t, store.PutPendingSignal(context.Background(), journal.PendingSignal{
		ID: "req-other-session", Action: ws.ActionMCPStepComplete, SessionID: "session-other", Payload: payload, Attempts: 1,
	}))
	require.NoError(t, store.PutPendingSignal(context.Background(), journal.PendingSignal{
		ID: "req-tool", Action: "mcp.tools.call", SessionID: "session-signal", Payload: payload, Attempts: 1,
	}))

	requests, err := client.PendingSignalReplayBatch(context.Background())
	require.NoError(t, err)
	require.Len(t, requests, 1, "only this session's idempotent signals replay")
	require.Equal(t, "req-original", requests[0].ID)
	require.Equal(t, ws.ActionMCPStepComplete, requests[0].Action)
	require.JSONEq(t, string(payload), string(requests[0].Payload))

	replayed, ok := store.get("req-original")
	require.True(t, ok)
	require.Equal(t, 2, replayed.Attempts)
}

func TestStepCompleteSignalReplayRearmsNoticeFromCaptureTime(t *testing.T) {
	store := newFakePendingSignalStore()
	scheduler := &fakeSignalScheduler{}
	client := newSignalTestClient(t, store, scheduler)
	capturedAt := time.Now().UTC().Add(-2 * time.Minute)
	require.NoError(t, store.PutPendingSignal(context.Background(), journal.PendingSignal{
		ID: "req-restarted", Action: ws.ActionMCPStepComplete, SessionID: "session-signal",
		Payload: json.RawMessage(`{"summary":"done"}`), CreatedAt: capturedAt, Attempts: 1,
	}))

	requests, err := client.PendingSignalReplayBatch(context.Background())
	require.NoError(t, err)
	require.Len(t, requests, 1)

	scheduled, _ := scheduler.snapshot()
	require.Len(t, scheduled, 1, "a capture from before an agentctl restart must re-arm its notice")
	require.Equal(t, "req-restarted", scheduled[0].id)
	require.Equal(t, capturedAt, scheduled[0].createdAt,
		"the window is derived from the capture time, not the reconnect time")
}

func TestStepCompleteSignalReplaySkipsDeliveredRecords(t *testing.T) {
	store := newFakePendingSignalStore()
	client := newSignalTestClient(t, store, nil)
	require.NoError(t, store.PutPendingSignal(context.Background(), journal.PendingSignal{
		ID: "req-delivered", Action: ws.ActionMCPStepComplete, SessionID: "session-signal",
		Payload: json.RawMessage(`{"summary":"done"}`),
	}))
	require.NoError(t, store.MarkPendingSignalDelivered(context.Background(), "req-delivered"))

	requests, err := client.PendingSignalReplayBatch(context.Background())
	require.NoError(t, err)
	require.Empty(t, requests)
}

// TestStepCompleteSignalCaptureIndependentOfBackendWriterLatency pins the
// isolation Form C relies on: the capture is committed to the agentctl journal
// before publication, so a delayed backend (SQLite writer contention, slow
// handling) only delays delivery. It cannot prevent the capture from being
// durable, and it does not turn the call into a capture failure.
func TestStepCompleteSignalCaptureIndependentOfBackendWriterLatency(t *testing.T) {
	store := newFakePendingSignalStore()
	client := newSignalTestClient(t, store, nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.RequestPayload(context.Background(), ws.ActionMCPStepComplete, map[string]any{"summary": "done"}, nil)
	}()
	var request *ws.Message
	select {
	case request = <-client.GetRequestChannel():
	case <-time.After(time.Second):
		t.Fatal("step_complete was not published")
	}
	captured, ok := store.get(request.ID)
	require.True(t, ok, "capture must complete before the backend sees the request")
	require.Nil(t, captured.DeliveredAt)

	// Simulate backend handling latency (writer wait). The request stays
	// pending; the capture remains durable, and the call does not fail.
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-errCh:
		t.Fatalf("request completed before the simulated writer delay ended: %v", err)
	default:
	}
	response, err := ws.NewResponse(request.ID, request.Action, map[string]any{"accepted": true})
	require.NoError(t, err)
	client.HandleResponse(response)
	require.NoError(t, <-errCh)
	require.Equal(t, []string{request.ID}, store.deliveredIDs())
}
