package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kandev/kandev/internal/agentctl/journal"
	"github.com/kandev/kandev/internal/common/agentstream"
	"github.com/kandev/kandev/internal/common/logger"
	mcporigin "github.com/kandev/kandev/internal/mcp/origin"
	ws "github.com/kandev/kandev/pkg/websocket"
	"go.uber.org/zap"
)

// ErrEmptyBackendPayload identifies a response with no payload bytes when a result sink was provided.
var ErrEmptyBackendPayload = errors.New("mcp backend response payload was empty")

// MCPRequest represents an MCP request to be sent to the backend.
type MCPRequest struct {
	ID      string          `json:"id"`
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload"`
}

// MCPResponse represents an MCP response from the backend.
type MCPResponse struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"` // "response" or "error"
	Payload json.RawMessage `json:"payload"`
}

type backendResponse struct {
	message   *ws.Message
	err       error
	sessionID string
}

type pendingRequest struct {
	result    chan backendResponse
	streamID  string
	sessionID string
}

// ChannelBackendClient implements BackendClient using channels.
// It sends MCP requests through a channel that will be read by the agent stream handler,
// and receives responses through a callback mechanism.
type ChannelBackendClient struct {
	requestCh chan *ws.Message
	pending   map[string]*pendingRequest
	pendingMu sync.Mutex
	sessionID string
	done      chan struct{}
	closeOnce sync.Once
	closeMu   sync.Mutex
	closed    bool
	publishWG sync.WaitGroup
	logger    *logger.Logger

	// signalStoreMu guards the durable capture provider and notice scheduler.
	// Store I/O never runs under this lock.
	signalStoreMu   sync.RWMutex
	signalStore     func() (journal.PendingSignalStore, error)
	signalScheduler SignalDeliveryScheduler
}

// NewChannelBackendClient creates a new channel-based backend client.
func NewChannelBackendClient(log *logger.Logger) *ChannelBackendClient {
	clientLogger := logger.Default()
	if log != nil {
		clientLogger = log
	}
	clientLogger = clientLogger.WithFields(zap.String("component", "mcp-backend-client"))
	return &ChannelBackendClient{
		requestCh: make(chan *ws.Message),
		pending:   make(map[string]*pendingRequest),
		done:      make(chan struct{}),
		logger:    clientLogger,
	}
}

// GetRequestChannel returns the channel for outgoing MCP requests.
// The agent stream handler should read from this channel and forward to the backend.
func (c *ChannelBackendClient) GetRequestChannel() <-chan *ws.Message {
	return c.requestCh
}

// SetSessionID sets the server-owned session correlation used in bridge logs.
func (c *ChannelBackendClient) SetSessionID(sessionID string) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	c.sessionID = sessionID
}

// HandleResponse handles an incoming MCP response from the backend.
// This should be called by the agent stream handler when it receives a response.
func (c *ChannelBackendClient) HandleResponse(msg *ws.Message) {
	if c.completeRequest(msg.ID, backendResponse{message: msg}) {
		return
	}
	// A replayed signal has no waiting producer. The response still confirms
	// delivery, so mark it before treating the response as unmatched.
	if c.confirmSignalDelivered(msg.ID) {
		return
	}
	c.logger.Debug("dropping MCP response with no pending request",
		zap.String("request_id", msg.ID),
		zap.String("type", string(msg.Type)),
		zap.String("action", msg.Action))
}

// BindRequestToStream records which backend stream delivered a request.
func (c *ChannelBackendClient) BindRequestToStream(requestID, streamID string) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if pending, ok := c.pending[requestID]; ok {
		pending.streamID = streamID
	}
}

// FailRequest releases one pending request after a transport failure.
func (c *ChannelBackendClient) FailRequest(requestID string, err error) {
	if c.completeRequest(requestID, backendResponse{err: err}) {
		recordMCPRequestFailure(err)
	}
}

// FailStreamRequests releases requests delivered by one disconnected stream.
func (c *ChannelBackendClient) FailStreamRequests(streamID string, err error) {
	c.pendingMu.Lock()
	failed := make([]*pendingRequest, 0)
	for id, pending := range c.pending {
		if pending.streamID == streamID {
			delete(c.pending, id)
			failed = append(failed, pending)
		}
	}
	c.pendingMu.Unlock()

	for _, pending := range failed {
		recordMCPRequestFailure(err)
		pending.result <- backendResponse{err: err, sessionID: pending.sessionID}
	}
}

// recordMCPRequestFailure counts one request lost to a transport failure. Only
// typed reasons from the closed agent-stream set are labelled.
func recordMCPRequestFailure(err error) {
	if reason, ok := agentstream.ReasonOf(err); ok {
		agentstream.RecordMCPRequestFailure(reason)
	}
}

func (c *ChannelBackendClient) completeRequest(requestID string, response backendResponse) bool {
	c.pendingMu.Lock()
	pending, ok := c.pending[requestID]
	if ok {
		delete(c.pending, requestID)
	}
	c.pendingMu.Unlock()
	if ok {
		if response.sessionID == "" {
			response.sessionID = pending.sessionID
		}
		pending.result <- response
	}
	return ok
}

// RequestPayload sends a request to the backend and unmarshals the response.
// The request will be cancelled if the context is cancelled or if Reset() is called.
func (c *ChannelBackendClient) RequestPayload(ctx context.Context, action string, payload, result interface{}) error {
	captured := isIdempotentSignalAction(action)
	if !c.beginPublish() {
		if captured {
			return &SignalDeliveryError{
				Code:      SignalCaptureUnavailableCode,
				Message:   fmt.Sprintf("%s was not sent because the MCP backend client is closed", action),
				Retryable: true,
			}
		}
		return sendFailure(false, action, RequestDeliveryClientClosedCode, "the MCP backend client is closed", nil)
	}
	publishing := true
	defer func() {
		if publishing {
			c.publishWG.Done()
		}
	}()

	id := uuid.New().String()
	start := time.Now()

	msg, err := ws.NewRequest(id, action, payload)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	if mcporigin.IsTrustedInternalCall(ctx) {
		mcporigin.AttachTrustedInternalCall(msg)
	}

	c.pendingMu.Lock()
	sessionID := c.sessionID
	c.pendingMu.Unlock()

	// Capture before publication: the record must exist before the request can
	// be lost, so a missing response is always replayable.
	if captured {
		if err := c.captureSignal(ctx, id, action, sessionID, msg.Payload); err != nil {
			return err
		}
	}

	// Create response channel
	respChan := make(chan backendResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = &pendingRequest{result: respChan, sessionID: sessionID}
	c.pendingMu.Unlock()

	c.logger.Debug("sending MCP request through agent stream",
		zap.String("request_id", id),
		zap.String("action", action),
		zap.String("session_id", sessionID),
		zap.Any("payload", backendPayloadForLog(action, payload)))

	// Ensure cleanup on exit
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	// Send request through channel
	select {
	case c.requestCh <- msg:
		// Request sent
	case <-c.done:
		return sendFailure(captured, action, RequestDeliveryClientClosedCode, "the MCP backend client is closed", fmt.Errorf("MCP backend client is closed"))
	case <-ctx.Done():
		c.logger.Debug("MCP request cancelled before send",
			zap.String("request_id", id),
			zap.String("action", action),
			zap.String("session_id", sessionID),
			zap.Duration("duration", time.Since(start)),
			zap.Error(ctx.Err()))
		return sendFailure(captured, action, RequestDeliveryCancelledCode, "the request was cancelled before publication", ctx.Err())
	case <-time.After(5 * time.Second):
		c.logger.Warn("timed out sending MCP request to agent stream",
			zap.String("request_id", id),
			zap.String("action", action),
			zap.String("session_id", sessionID),
			zap.Duration("duration", time.Since(start)))
		// The producer's bounded send expired, which means the stream writer
		// was blocked (typically on a stalled frame write) for the whole
		// budget. Count it as a typed transport failure.
		agentstream.RecordMCPRequestFailure(agentstream.AgentStreamReasonWriteTimeout)
		return sendFailure(captured, action, RequestDeliverySendTimeoutCode, "the agent-stream send deadline expired", fmt.Errorf("timeout sending request to agent stream"))
	}
	publishing = false
	c.publishWG.Done()

	// Wait for response
	select {
	case response := <-respChan:
		if response.err != nil {
			c.logger.Warn("MCP request failed after publication",
				zap.String("request_id", id),
				zap.String("action", action),
				zap.String("session_id", response.sessionID),
				zap.Duration("duration", time.Since(start)),
				zap.Error(response.err))
			return signalDeliveryFailure(captured, action, response.err)
		}
		resp := response.message
		if captured {
			// A backend response, including a typed terminal error, settles the
			// captured record even when the originating call already returned.
			c.confirmSignalDelivered(id)
		}
		c.logger.Debug("received MCP response from backend",
			zap.String("request_id", id),
			zap.String("action", action),
			zap.String("type", string(resp.Type)),
			zap.Duration("duration", time.Since(start)))
		if resp.Type == ws.MessageTypeError {
			var ep ws.ErrorPayload
			if json.Unmarshal(resp.Payload, &ep) == nil {
				return &BackendError{Code: ep.Code, Message: ep.Message, Details: ep.Details}
			}
			return fmt.Errorf("backend error: %s", string(resp.Payload))
		}
		if result == nil {
			return nil
		}
		if len(resp.Payload) == 0 {
			c.logger.Warn(ErrEmptyBackendPayload.Error(),
				zap.String("request_id", id),
				zap.String("action", action),
				zap.String("session_id", response.sessionID),
				zap.Duration("duration", time.Since(start)))
			return fmt.Errorf("empty response payload for action %q: %w", action, ErrEmptyBackendPayload)
		}
		if err := json.Unmarshal(resp.Payload, result); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
		return nil
	case <-ctx.Done():
		c.logger.Warn("MCP request context cancelled while waiting for response",
			zap.String("request_id", id),
			zap.String("action", action),
			zap.Duration("duration", time.Since(start)),
			zap.Error(ctx.Err()))
		return signalDeliveryFailure(captured, action, ctx.Err())
	case <-c.done:
		return signalDeliveryFailure(captured, action, fmt.Errorf("MCP backend client is closed"))
	}
}

// sendFailure converts a pre-publication failure into a typed error. A captured
// signal keeps its capture-pending semantics. Any other request never reached
// the backend, so retrying it cannot repeat a side effect.
func sendFailure(captured bool, action, code, reason string, cause error) error {
	if captured {
		return signalDeliveryFailure(true, action, cause)
	}
	return &RequestDeliveryError{
		Code:      code,
		Message:   fmt.Sprintf("%s was not sent because %s; retrying is safe", action, reason),
		Retryable: true,
		Err:       cause,
	}
}

func (c *ChannelBackendClient) beginPublish() bool {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.closed {
		return false
	}
	c.publishWG.Add(1)
	return true
}

func backendPayloadForLog(action string, payload interface{}) interface{} {
	if action != ws.ActionMCPInvokePluginTool {
		return payload
	}
	values, ok := payload.(map[string]any)
	if !ok {
		return "<redacted>"
	}
	safe := make(map[string]any, len(values))
	for key, value := range values {
		if key != pluginToolArgumentsKey {
			safe[key] = value
		}
	}
	return safe
}

// Reset clears all pending MCP requests.
// This should be called when starting a new ACP session to prevent
// stale requests from a previous session from interfering.
func (c *ChannelBackendClient) Reset() {
	c.pendingMu.Lock()
	pending := make([]*pendingRequest, 0, len(c.pending))
	for id, request := range c.pending {
		delete(c.pending, id)
		pending = append(pending, request)
	}
	c.pendingMu.Unlock()

	for _, request := range pending {
		request.result <- backendResponse{
			err:       fmt.Errorf("MCP request cancelled: session reset"),
			sessionID: request.sessionID,
		}
	}
}

// Close prevents new requests and cancels pending requests.
func (c *ChannelBackendClient) Close() {
	c.closeOnce.Do(func() {
		c.closeMu.Lock()
		c.closed = true
		close(c.done)
		c.closeMu.Unlock()
	})
	c.publishWG.Wait()
}
