package streams

import "time"

// SignalDeliveryFailureReason identifies why a captured idempotent signal was
// not confirmed delivered within the bounded window. Only this closed set is
// used as a metric label.
type SignalDeliveryFailureReason string

const (
	// SignalDeliveryFailureReasonWindowExceeded means the captured signal was
	// still undelivered when the bounded delivery window expired.
	SignalDeliveryFailureReasonWindowExceeded SignalDeliveryFailureReason = "delivery_window_exceeded"
)

func (r SignalDeliveryFailureReason) Valid() bool {
	return r == SignalDeliveryFailureReasonWindowExceeded
}

// SignalDeliveryFailure is the durable completion-pending notice emitted by
// agentctl for one captured signal. It deliberately carries no signal payload:
// only identity, reason, attempt count, and the first attempt time cross the
// process boundary so the backend can surface the pending completion without
// duplicating agent-authored content.
type SignalDeliveryFailure struct {
	RequestID      string                      `json:"request_id"`
	Action         string                      `json:"action"`
	SessionID      string                      `json:"session_id"`
	Reason         SignalDeliveryFailureReason `json:"reason"`
	Attempts       int                         `json:"attempts"`
	FirstAttemptAt time.Time                   `json:"first_attempt_at"`
}
