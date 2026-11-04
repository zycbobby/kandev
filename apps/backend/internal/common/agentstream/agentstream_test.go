package agentstream

import (
	"context"
	"errors"
	"expvar"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestAgentStreamReasonSetIsClosed(t *testing.T) {
	for _, reason := range []AgentStreamDisconnectReason{
		AgentStreamReasonEventQueueFull,
		AgentStreamReasonWriteTimeout,
		AgentStreamReasonPingTimeout,
		AgentStreamReasonReadTimeout,
		AgentStreamReasonOwnerChanged,
		AgentStreamReasonReadError,
	} {
		if !reason.Valid() {
			t.Fatalf("reason %q is not part of the closed set", reason)
		}
	}
	for _, reason := range []AgentStreamDisconnectReason{"", "timeout", "unknown", "EVENT_QUEUE_FULL"} {
		if reason.Valid() {
			t.Fatalf("reason %q unexpectedly accepted", reason)
		}
	}
}

func TestAgentStreamReasonConstants(t *testing.T) {
	want := map[AgentStreamDisconnectReason]string{
		AgentStreamReasonEventQueueFull: "event_queue_full",
		AgentStreamReasonWriteTimeout:   "write_timeout",
		AgentStreamReasonPingTimeout:    "ping_timeout",
		AgentStreamReasonReadTimeout:    "read_timeout",
		AgentStreamReasonOwnerChanged:   "owner_changed",
		AgentStreamReasonReadError:      "read_error",
	}
	for reason, value := range want {
		if string(reason) != value {
			t.Fatalf("reason = %q, want %q", reason, value)
		}
	}
}

func TestAgentStreamHealthRecordsFirstReason(t *testing.T) {
	health := NewHealth()
	health.SetReason(AgentStreamReasonWriteTimeout)
	health.SetReason(AgentStreamReasonReadError)
	reason, ok := health.Reason()
	if !ok || reason != AgentStreamReasonWriteTimeout {
		t.Fatalf("reason = %q/%v, want first write_timeout", reason, ok)
	}
}

func TestAgentStreamHealthClassifiesFailures(t *testing.T) {
	timeoutErr := &fakeTimeoutError{}
	cases := []struct {
		name string
		err  error
		want AgentStreamDisconnectReason
	}{
		{"write deadline", timeoutErr, AgentStreamReasonWriteTimeout},
		{"read deadline", timeoutErr, AgentStreamReasonReadTimeout},
		{"read reset", errors.New("connection reset by peer"), AgentStreamReasonReadError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			health := NewHealth()
			switch tc.name {
			case "write deadline":
				health.RecordWriteFailure(tc.err)
			default:
				health.RecordReadFailure(tc.err)
			}
			reason, ok := health.Reason()
			if !ok || reason != tc.want {
				t.Fatalf("reason = %q/%v, want %q", reason, ok, tc.want)
			}
		})
	}
}

func TestAgentStreamHealthIgnoresCleanClose(t *testing.T) {
	health := NewHealth()
	health.RecordReadFailure(&websocket.CloseError{Code: websocket.CloseNormalClosure, Text: ""})
	if _, ok := health.Reason(); ok {
		t.Fatal("clean close recorded a disconnect reason")
	}
	health.RecordReadFailure(&websocket.CloseError{Code: websocket.ClosePolicyViolation, Text: string(AgentStreamReasonEventQueueFull)})
	reason, ok := health.Reason()
	if !ok || reason != AgentStreamReasonEventQueueFull {
		t.Fatalf("reason = %q/%v, want event_queue_full from close text", reason, ok)
	}
}

func TestAgentStreamHealthPongExpiry(t *testing.T) {
	health := NewHealth()
	if health.PongExpired(time.Minute) {
		t.Fatal("fresh health reported an expired pong")
	}
	health.mu.Lock()
	health.lastPong = time.Now().Add(-time.Hour)
	health.mu.Unlock()
	if !health.PongExpired(time.Minute) {
		t.Fatal("stale pong was not detected")
	}
	health.MarkPong()
	if health.PongExpired(time.Minute) {
		t.Fatal("pong timestamp was not refreshed")
	}
}

func TestAgentStreamDisconnectErrorCarriesReason(t *testing.T) {
	base := errors.New("transport lost")
	err := NewDisconnectError(AgentStreamReasonWriteTimeout, base)
	reason, ok := ReasonOf(err)
	if !ok || reason != AgentStreamReasonWriteTimeout {
		t.Fatalf("reason = %q/%v, want write_timeout", reason, ok)
	}
	if !errors.Is(err, base) {
		t.Fatal("typed error did not wrap the transport error")
	}
	if reason, _ := ReasonOf(NewDisconnectError("bogus", base)); reason != AgentStreamReasonReadError {
		t.Fatalf("invalid reason fell through as %q", reason)
	}
}

func TestAgentStreamMetricsAreBoundedAndPublished(t *testing.T) {
	for _, name := range []string{
		"agent_stream_disconnect_total",
		"agent_stream_event_queue_depth",
		"agent_stream_mcp_request_failures_total",
	} {
		if expvar.Get(name) == nil {
			t.Fatalf("metric %q is not published", name)
		}
	}

	RecordDisconnect("bogus")
	RecordMCPRequestFailure("bogus")
	if disconnectTotal.Get("bogus") != nil {
		t.Fatal("disconnect metric accepted a label outside the closed set")
	}
	if mcpRequestFailuresTotal.Get("bogus") != nil {
		t.Fatal("MCP failure metric accepted a label outside the closed set")
	}

	before := disconnectCount(t, AgentStreamReasonWriteTimeout)
	RecordDisconnect(AgentStreamReasonWriteTimeout)
	if got := disconnectCount(t, AgentStreamReasonWriteTimeout); got != before+1 {
		t.Fatalf("disconnect count = %d, want %d", got, before+1)
	}

	SetEventQueueDepth(3)
	if depth := eventQueueDepth.Value(); depth != 3 {
		t.Fatalf("queue depth = %d, want 3", depth)
	}
}

func disconnectCount(t *testing.T, reason AgentStreamDisconnectReason) int64 {
	t.Helper()
	entry := disconnectTotal.Get(string(reason))
	if entry == nil {
		return 0
	}
	counter, ok := entry.(*expvar.Int)
	if !ok {
		t.Fatalf("disconnect counter has type %T", entry)
	}
	return counter.Value()
}

func TestAgentStreamPingPumpDetectsMissingPong(t *testing.T) {
	var (
		mu     sync.Mutex
		cancel bool
	)
	health := NewHealth()
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunPingPump(t.Context(), noopControlWriter{}, health, func() {
			mu.Lock()
			cancel = true
			mu.Unlock()
		}, 5*time.Millisecond, 25*time.Millisecond, 5*time.Millisecond)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ping pump did not stop after the pong deadline")
	}
	reason, ok := health.Reason()
	if !ok || reason != AgentStreamReasonPingTimeout {
		t.Fatalf("reason = %q/%v, want ping_timeout", reason, ok)
	}
	mu.Lock()
	defer mu.Unlock()
	if !cancel {
		t.Fatal("ping pump did not cancel the stream")
	}
}

func TestAgentStreamPingPumpSurvivesHealthyPongs(t *testing.T) {
	health := NewHealth()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunPingPump(ctx, controlledPongWriter{health: health}, health, func() {}, 5*time.Millisecond, 50*time.Millisecond, 5*time.Millisecond)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ping pump did not stop")
	}
	if reason, ok := health.Reason(); ok {
		t.Fatalf("healthy pongs recorded reason %q", reason)
	}
}

type fakeTimeoutError struct{}

func (fakeTimeoutError) Error() string   { return "i/o timeout" }
func (fakeTimeoutError) Timeout() bool   { return true }
func (fakeTimeoutError) Temporary() bool { return true }

type noopControlWriter struct{}

func (noopControlWriter) WriteControl(int, []byte, time.Time) error { return nil }

// controlledPongWriter marks a pong on every control write so the pump stays
// healthy while the test cancels the context.
type controlledPongWriter struct{ health *Health }

func (w controlledPongWriter) WriteControl(int, []byte, time.Time) error {
	w.health.MarkPong()
	return nil
}
