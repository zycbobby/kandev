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
