package agentstream

import "expvar"

// Transport metrics are process-global expvar values, matching the delivery
// journal metric convention. Every map label is a value from the closed
// AgentStreamDisconnectReason set; no session, submission, or payload identity
// is ever a label.
var (
	disconnectTotal         = expvar.NewMap("agent_stream_disconnect_total")
	eventQueueDepth         = expvar.NewInt("agent_stream_event_queue_depth")
	mcpRequestFailuresTotal = expvar.NewMap("agent_stream_mcp_request_failures_total")

	signalCaptureTotal        = expvar.NewMap("workflow_step_signal_capture_total")
	signalReplayTotal         = expvar.NewMap("workflow_step_signal_replay_total")
	signalDeliveryFailedTotal = expvar.NewMap("workflow_step_signal_delivery_failed_total")
)

// Closed label sets for the signal counters. No request, session, submission,
// or payload identity is ever a label.
var (
	signalCaptureOutcomes = map[string]struct{}{
		"captured": {}, "store_full": {}, "store_unavailable": {}, "store_invalid": {},
	}
	signalReplayOutcomes = map[string]struct{}{
		"replayed": {}, "write_failed": {}, "store_unavailable": {},
	}
	signalDeliveryFailedReasons = map[string]struct{}{
		"delivery_window_exceeded": {},
	}
)

// RecordDisconnect counts one terminated stream by typed reason.
func RecordDisconnect(reason AgentStreamDisconnectReason) {
	if !reason.Valid() {
		return
	}
	disconnectTotal.Add(string(reason), 1)
}

// SetEventQueueDepth publishes the current reader-side event queue depth.
func SetEventQueueDepth(depth int) {
	if depth < 0 {
		depth = 0
	}
	eventQueueDepth.Set(int64(depth))
}

// RecordMCPRequestFailure counts one MCP control request lost to the transport
// by typed reason.
func RecordMCPRequestFailure(reason AgentStreamDisconnectReason) {
	if !reason.Valid() {
		return
	}
	mcpRequestFailuresTotal.Add(string(reason), 1)
}

// RecordSignalCapture counts one idempotent workflow-signal capture attempt.
// A capture failure is separate from a transport failure: the store can be
// full or unavailable while the transport is healthy, and vice versa.
func RecordSignalCapture(outcome string) {
	signalCaptureTotal.Add(boundedSignalLabel(signalCaptureOutcomes, outcome), 1)
}

// RecordSignalReplay counts one bounded replay page attempt outcome or one
// replayed signal.
func RecordSignalReplay(outcome string) {
	signalReplayTotal.Add(boundedSignalLabel(signalReplayOutcomes, outcome), 1)
}

// RecordSignalDeliveryFailed counts one durable completion-pending notice.
func RecordSignalDeliveryFailed(reason string) {
	signalDeliveryFailedTotal.Add(boundedSignalLabel(signalDeliveryFailedReasons, reason), 1)
}

func boundedSignalLabel(allowed map[string]struct{}, value string) string {
	if _, ok := allowed[value]; ok {
		return value
	}
	return "unknown"
}
