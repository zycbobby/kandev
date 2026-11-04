// Package agentstream owns the transport contract shared by the two ends of
// the agentctl agent event stream: the closed set of disconnect reasons, the
// keepalive/deadline thresholds both ends must agree on, and the bounded
// metrics that describe transport failures.
package agentstream

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// AgentStreamDisconnectReason is the closed set of reasons an agent stream
// transport can terminate. Only these values are used as metric labels.
type AgentStreamDisconnectReason string

const (
	AgentStreamReasonEventQueueFull AgentStreamDisconnectReason = "event_queue_full"
	AgentStreamReasonWriteTimeout   AgentStreamDisconnectReason = "write_timeout"
	AgentStreamReasonPingTimeout    AgentStreamDisconnectReason = "ping_timeout"
	AgentStreamReasonReadTimeout    AgentStreamDisconnectReason = "read_timeout"
	AgentStreamReasonOwnerChanged   AgentStreamDisconnectReason = "owner_changed"
	AgentStreamReasonReadError      AgentStreamDisconnectReason = "read_error"
)

// Valid reports whether the reason belongs to the closed set.
func (r AgentStreamDisconnectReason) Valid() bool {
	switch r {
	case AgentStreamReasonEventQueueFull,
		AgentStreamReasonWriteTimeout,
		AgentStreamReasonPingTimeout,
		AgentStreamReasonReadTimeout,
		AgentStreamReasonOwnerChanged,
		AgentStreamReasonReadError:
		return true
	default:
		return false
	}
}

// Transport thresholds. They are shared so both ends detect a half-open
// connection with the same timing:
//
//   - PingInterval (30s) keeps NAT and proxy bindings warm during long agent
//     turns and must stay well below PongTimeout so one lost ping is not a
//     disconnect.
//   - PongTimeout (90s) is the maximum age of the last observed pong, i.e.
//     three missed pings.
//   - ReadDeadline (2 * PongTimeout) is a backstop behind the ping pump: a
//     dead pump still surfaces the stream as dead. It is refreshed by pongs.
//   - WriteDeadline (10s) bounds one websocket frame so a stalled peer
//     surfaces as write_timeout instead of blocking the writer forever.
//   - EventQueueMaxItems/EventQueueMaxBytes bound reader-side retention when
//     the event handler stalls. A full queue closes the stream and the
//     durable journal replays the events after reconnect.
const (
	PingInterval  = 30 * time.Second
	PongTimeout   = 90 * time.Second
	WriteDeadline = 10 * time.Second
	ReadDeadline  = 2 * PongTimeout

	EventQueueMaxItems = 4096
	EventQueueMaxBytes = 4 << 20
)

// DisconnectError carries the typed reason a stream ended. It wraps the
// underlying transport error so callers can still classify it with errors.Is.
type DisconnectError struct {
	Reason AgentStreamDisconnectReason
	Err    error
}

func (e *DisconnectError) Error() string {
	if e.Err == nil {
		return string(e.Reason)
	}
	return fmt.Sprintf("%s: %v", e.Reason, e.Err)
}

func (e *DisconnectError) Unwrap() error { return e.Err }

// NewDisconnectError wraps err with a typed reason. An invalid reason is
// replaced with AgentStreamReasonReadError so a metric label always belongs
// to the closed set.
func NewDisconnectError(reason AgentStreamDisconnectReason, err error) error {
	if !reason.Valid() {
		reason = AgentStreamReasonReadError
	}
	return &DisconnectError{Reason: reason, Err: err}
}

// ReasonOf extracts the typed reason from an error chain.
func ReasonOf(err error) (AgentStreamDisconnectReason, bool) {
	var disconnectErr *DisconnectError
	if errors.As(err, &disconnectErr) {
		return disconnectErr.Reason, true
	}
	return "", false
}

// ReasonFromCloseText maps a peer close frame's text to a typed reason. Peers
// use it to tell us why they closed (for example event_queue_full).
func ReasonFromCloseText(text string) (AgentStreamDisconnectReason, bool) {
	reason := AgentStreamDisconnectReason(text)
	if !reason.Valid() {
		return "", false
	}
	return reason, true
}

// IsTimeout reports whether err is a network timeout, which is how an expired
// read or write deadline surfaces from gorilla.
func IsTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// ClassifyWriteError maps a failed websocket write to a disconnect reason. A
// deadline failure is write_timeout; any other write failure is generic
// transport loss and is reported as read_error, matching how the peer's read
// loop classifies the same dead connection.
func ClassifyWriteError(err error) AgentStreamDisconnectReason {
	if IsTimeout(err) {
		return AgentStreamReasonWriteTimeout
	}
	return AgentStreamReasonReadError
}

// Health tracks liveness evidence for one stream. It is shared by the reader,
// the writer, and the ping pump. The first recorded reason wins so the root
// cause is reported rather than a follow-on symptom.
type Health struct {
	mu       sync.Mutex
	reason   AgentStreamDisconnectReason
	lastPong time.Time
}

// NewHealth returns a health tracker whose pong clock starts now.
func NewHealth() *Health {
	return &Health{lastPong: time.Now()}
}

// MarkPong records that the peer answered a ping.
func (h *Health) MarkPong() {
	h.mu.Lock()
	h.lastPong = time.Now()
	h.mu.Unlock()
}

// PongExpired reports whether no pong has arrived within timeout.
func (h *Health) PongExpired(timeout time.Duration) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return time.Since(h.lastPong) >= timeout
}

// SetReason records the stream's disconnect reason if none was recorded yet.
func (h *Health) SetReason(reason AgentStreamDisconnectReason) {
	if !reason.Valid() {
		return
	}
	h.mu.Lock()
	if h.reason == "" {
		h.reason = reason
	}
	h.mu.Unlock()
}

// RecordWriteFailure classifies one failed write and records write_timeout for
// deadline failures. Generic write failures are left to the read loop, which
// observes the same dead connection as read_error.
func (h *Health) RecordWriteFailure(err error) {
	if err == nil || !IsTimeout(err) {
		return
	}
	h.SetReason(AgentStreamReasonWriteTimeout)
}

// RecordReadFailure classifies one failed read. A clean peer close is not a
// failure and records nothing; a typed close reason is preserved.
func (h *Health) RecordReadFailure(err error) {
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		if reason, ok := ReasonFromCloseText(closeErr.Text); ok {
			h.SetReason(reason)
			return
		}
		if closeErr.Code == websocket.CloseNormalClosure || closeErr.Code == websocket.CloseGoingAway {
			return
		}
	}
	if IsTimeout(err) {
		h.SetReason(AgentStreamReasonReadTimeout)
		return
	}
	h.SetReason(AgentStreamReasonReadError)
}

// Reason returns the recorded reason, if any.
func (h *Health) Reason() (AgentStreamDisconnectReason, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.reason == "" {
		return "", false
	}
	return h.reason, true
}

// ControlWriter writes websocket control frames. *websocket.Conn implements
// it; tests provide their own.
type ControlWriter interface {
	WriteControl(messageType int, data []byte, deadline time.Time) error
}

// RunPingPump sends keepalive pings until ctx ends or the peer stops ponging.
// It sets ping_timeout and cancels the stream when the pong deadline expires
// or a ping cannot be written. Only control frames are written here, so it
// never races the stream's data writer.
func RunPingPump(
	ctx context.Context,
	conn ControlWriter,
	health *Health,
	cancel context.CancelFunc,
	pingInterval, pongTimeout, writeDeadline time.Duration,
) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if health.PongExpired(pongTimeout) {
			health.SetReason(AgentStreamReasonPingTimeout)
			cancel()
			return
		}
		if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeDeadline)); err != nil {
			if ctx.Err() != nil {
				return
			}
			health.SetReason(AgentStreamReasonPingTimeout)
			cancel()
			return
		}
	}
}
