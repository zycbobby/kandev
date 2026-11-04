package mcp

import "fmt"

// Stable codes carried by RequestDeliveryError so an agent can tell a request
// that never reached the backend from an ordinary tool failure.
const (
	RequestDeliveryClientClosedCode = "MCP_REQUEST_CLIENT_CLOSED"
	RequestDeliveryCancelledCode    = "MCP_REQUEST_CANCELLED"
	RequestDeliverySendTimeoutCode  = "MCP_REQUEST_SEND_TIMEOUT"
)

// RequestDeliveryError reports an MCP request that was not published to the
// agent stream. The backend never received it, so Retryable is true: a retry
// cannot repeat a side effect, even for a non-idempotent tool.
type RequestDeliveryError struct {
	Code      string
	Message   string
	Retryable bool
	Err       error
}

func (e *RequestDeliveryError) Error() string {
	if e == nil {
		return "request delivery error"
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

func (e *RequestDeliveryError) Unwrap() error { return e.Err }

// BackendError preserves a structured WebSocket error for MCP tool handlers.
// Details may contain a committed partial result and must remain available to
// callers that can recover from an independent provider failure.
type BackendError struct {
	Code    string
	Message string
	Details map[string]interface{}
}

func (e *BackendError) Error() string {
	if e == nil {
		return "backend error"
	}
	if e.Code == "" {
		return fmt.Sprintf("backend error: %s", e.Message)
	}
	return fmt.Sprintf("backend error [%s]: %s", e.Code, e.Message)
}
