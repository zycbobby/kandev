package client

import (
	"context"
	"encoding/json"
	"errors"
	"expvar"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kandev/kandev/internal/common/agentstream"
	ws "github.com/kandev/kandev/pkg/websocket"
)

func TestAgentStreamEventQueueFullTypedDisconnect(t *testing.T) {
	setAgentStreamEventQueueLimit(t, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for i := 0; i < 16; i++ {
			data, _ := json.Marshal(AgentEvent{Type: "message_chunk", Text: "backlog"})
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		}
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	client := &Client{
		baseURL:         server.URL,
		httpClient:      &http.Client{Timeout: 5 * time.Second},
		logger:          newTestLogger(),
		pendingRequests: make(map[string]chan *ws.Message),
	}
	handlerBlocked := make(chan struct{})
	disconnected := make(chan error, 1)
	err := client.StreamUpdates(context.Background(), func(AgentEvent) {
		<-handlerBlocked
	}, nil, func(err error) {
		disconnected <- err
	})
	if err != nil {
		t.Fatalf("connect stream: %v", err)
	}
	t.Cleanup(client.Close)

	before := agentStreamMetricValue(t, "agent_stream_disconnect_total", string(agentstream.AgentStreamReasonEventQueueFull))
	waitForAgentStreamMetric(t, "agent_stream_disconnect_total",
		string(agentstream.AgentStreamReasonEventQueueFull), before+1, 3*time.Second)
	close(handlerBlocked)

	select {
	case disconnectErr := <-disconnected:
		reason, ok := agentstream.ReasonOf(disconnectErr)
		if !ok || reason != agentstream.AgentStreamReasonEventQueueFull {
			t.Fatalf("disconnect reason = %q/%v, want event_queue_full: %v", reason, ok, disconnectErr)
		}
		if !errors.Is(disconnectErr, ErrAgentEventQueueFull) {
			t.Fatalf("disconnect error %v does not wrap ErrAgentEventQueueFull", disconnectErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queue overflow did not surface a disconnect")
	}
}

func TestAgentStreamPingTimeoutTypedDisconnect(t *testing.T) {
	setAgentStreamTimings(t, 10*time.Millisecond, 60*time.Millisecond, 200*time.Millisecond, time.Second)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// Never read: the server neither answers pings nor sends frames.
		<-release
		_ = conn.Close()
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	client := &Client{
		baseURL:         server.URL,
		httpClient:      &http.Client{Timeout: 5 * time.Second},
		logger:          newTestLogger(),
		pendingRequests: make(map[string]chan *ws.Message),
	}
	disconnected := make(chan error, 1)
	if err := client.StreamUpdates(context.Background(), func(AgentEvent) {}, nil, func(err error) {
		disconnected <- err
	}); err != nil {
		t.Fatalf("connect stream: %v", err)
	}
	t.Cleanup(client.Close)

	select {
	case disconnectErr := <-disconnected:
		reason, ok := agentstream.ReasonOf(disconnectErr)
		if !ok || reason != agentstream.AgentStreamReasonPingTimeout {
			t.Fatalf("disconnect reason = %q/%v, want ping_timeout: %v", reason, ok, disconnectErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("missing pong did not surface a disconnect")
	}
}

// TestAgentStreamRequestWriteHonorsDeadline proves prompt/control writes to a
// peer that stopped reading fail on the client write deadline instead of
// waiting for the caller's much longer context.
func TestAgentStreamRequestWriteHonorsDeadline(t *testing.T) {
	setAgentStreamTimings(t, time.Minute, 90*time.Second, 2*time.Minute, 100*time.Millisecond)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if tcp, ok := conn.UnderlyingConn().(*net.TCPConn); ok {
			_ = tcp.SetReadBuffer(1024)
		}
		<-release
		_ = conn.Close()
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	client := &Client{
		baseURL:         server.URL,
		httpClient:      &http.Client{Timeout: 5 * time.Second},
		logger:          newTestLogger(),
		pendingRequests: make(map[string]chan *ws.Message),
	}
	if err := client.StreamUpdates(context.Background(), func(AgentEvent) {}, nil, nil); err != nil {
		t.Fatalf("connect stream: %v", err)
	}
	t.Cleanup(client.Close)

	client.mu.RLock()
	conn := client.agentStreamConn
	client.mu.RUnlock()
	if conn == nil {
		t.Fatal("stream connection was not retained")
	}
	if tcp, ok := conn.UnderlyingConn().(*net.TCPConn); ok {
		_ = tcp.SetWriteBuffer(1024)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_, err := client.sendStreamRequest(ctx, "agent.prompt", map[string]string{
		"text": strings.Repeat("x", 4<<20),
	})
	if err == nil {
		t.Fatal("oversized request to a non-reading peer succeeded")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("request write took %v, want the write deadline to fire first", elapsed)
	}
	if !agentstream.IsTimeout(err) {
		t.Fatalf("request error = %v, want a deadline timeout", err)
	}
}

func setAgentStreamEventQueueLimit(t *testing.T, limit int) {
	t.Helper()
	previous := maxAgentEventQueueItems
	maxAgentEventQueueItems = limit
	t.Cleanup(func() { maxAgentEventQueueItems = previous })
}

func setAgentStreamTimings(t *testing.T, ping, pong, read, write time.Duration) {
	t.Helper()
	previousPing, previousPong := agentStreamPingInterval, agentStreamPongTimeout
	previousRead, previousWrite := agentStreamReadDeadline, agentStreamWriteDeadline
	agentStreamPingInterval, agentStreamPongTimeout = ping, pong
	agentStreamReadDeadline, agentStreamWriteDeadline = read, write
	t.Cleanup(func() {
		agentStreamPingInterval, agentStreamPongTimeout = previousPing, previousPong
		agentStreamReadDeadline, agentStreamWriteDeadline = previousRead, previousWrite
	})
}

func agentStreamMetricValue(t *testing.T, name, label string) int64 {
	t.Helper()
	metric := expvar.Get(name)
	if metric == nil {
		t.Fatalf("metric %q is not published", name)
	}
	values, ok := metric.(*expvar.Map)
	if !ok {
		t.Fatalf("metric %q has type %T, want *expvar.Map", name, metric)
	}
	entry := values.Get(label)
	if entry == nil {
		return 0
	}
	counter, ok := entry.(*expvar.Int)
	if !ok {
		t.Fatalf("metric %q label %q has type %T, want *expvar.Int", name, label, entry)
	}
	return counter.Value()
}

func waitForAgentStreamMetric(t *testing.T, name, label string, want int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if agentStreamMetricValue(t, name, label) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("metric %q{%s} = %d, want at least %d",
		name, label, agentStreamMetricValue(t, name, label), want)
}
