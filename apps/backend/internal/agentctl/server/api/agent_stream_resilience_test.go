package api

import (
	"bytes"
	"context"
	"encoding/json"
	"expvar"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kandev/kandev/internal/agentctl/journal"
	"github.com/kandev/kandev/internal/agentctl/server/adapter"
	"github.com/kandev/kandev/internal/agentctl/server/config"
	"github.com/kandev/kandev/internal/agentctl/server/process"
	"github.com/kandev/kandev/internal/common/agentstream"
	mcpserver "github.com/kandev/kandev/internal/mcp/server"
	ws "github.com/kandev/kandev/pkg/websocket"
)

// TestAgentStreamWriteDeadlineAppliesToEveryWrite drives the production write
// closure against a peer that never reads. The connection's buffers are
// shrunk so the first large frame blocks and the write deadline must surface
// as a typed write_timeout instead of blocking forever.
func TestAgentStreamWriteDeadlineAppliesToEveryWrite(t *testing.T) {
	setAgentStreamWriteDeadline(t, 100*time.Millisecond)
	serverConn, _ := newAgentStreamConnPair(t)
	server := &Server{logger: newTestLogger()}
	health := agentstream.NewHealth()
	write := server.newAgentStreamWriteMessage(serverConn, &sync.Mutex{}, health)

	payload := bytes.Repeat([]byte("x"), 1<<20)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		err := write(payload)
		if err == nil {
			continue
		}
		if !agentstream.IsTimeout(err) {
			t.Fatalf("write error = %v, want a deadline timeout", err)
		}
		reason, ok := health.Reason()
		if !ok || reason != agentstream.AgentStreamReasonWriteTimeout {
			t.Fatalf("reason = %q/%v, want write_timeout", reason, ok)
		}
		return
	}
	t.Fatal("write deadline never fired against a non-reading peer")
}

func TestAgentStreamPingTimeoutDisconnect(t *testing.T) {
	setAgentStreamTimings(t, 20*time.Millisecond, 80*time.Millisecond, 200*time.Millisecond, time.Second)
	log := newTestLogger()
	cfg := &config.InstanceConfig{Port: 0, WorkDir: t.TempDir()}
	server := httptest.NewServer(NewServer(cfg, process.NewManager(cfg, log), nil, nil, log).router)
	t.Cleanup(server.Close)

	before := agentStreamMetricValue(t, "agent_stream_disconnect_total", string(agentstream.AgentStreamReasonPingTimeout))
	// The client never reads, so it never answers the server's pings.
	conn := dialTestWS(t, server)
	t.Cleanup(func() { _ = conn.Close() })

	waitForAgentStreamMetric(t, "agent_stream_disconnect_total",
		string(agentstream.AgentStreamReasonPingTimeout), before+1, 3*time.Second)
}

// TestAgentStreamMCPRequestPriorityDuringEventBacklog replays a journal page
// whose writes are slow and asks whether a control request can still reach
// the peer without waiting for the whole page.
func TestAgentStreamMCPRequestPriorityDuringEventBacklog(t *testing.T) {
	server, procMgr, deliveryJournal := newDurableDeliveryTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	appendAgentStreamJournalEvents(t, ctx, deliveryJournal, 40)

	backend := mcpserver.NewChannelBackendClient(newTestLogger())
	t.Cleanup(backend.Close)
	server.mcpBackendClient = backend

	firstEvent := make(chan struct{})
	var firstOnce sync.Once
	mcpWritten := make(chan time.Time, 1)
	writeMessage := func(data []byte) error {
		var message ws.Message
		if json.Unmarshal(data, &message) == nil && message.Type == ws.MessageTypeRequest {
			mcpWritten <- time.Now()
			return nil
		}
		firstOnce.Do(func() { close(firstEvent) })
		// Simulate a slow reader: every event frame takes real time.
		time.Sleep(20 * time.Millisecond)
		return nil
	}
	writer := durableAgentStreamWriter{
		server: server, streamID: "stream-1", deliveryStreamID: "session-1",
		updatesCh: procMgr.GetUpdates(), mcpRequestCh: backend.GetRequestChannel(),
		wakeups: procMgr.DeliveryWakeups(), writeMessage: writeMessage,
	}
	go writer.run(ctx, 0, true)

	select {
	case <-firstEvent:
	case <-time.After(2 * time.Second):
		t.Fatal("durable replay did not start writing events")
	}

	requestCtx, requestCancel := context.WithCancel(ctx)
	defer requestCancel()
	go func() { _ = backend.RequestPayload(requestCtx, "mcp.test", nil, nil) }()

	start := time.Now()
	select {
	case writtenAt := <-mcpWritten:
		if elapsed := writtenAt.Sub(start); elapsed > 500*time.Millisecond {
			t.Fatalf("MCP request waited %v behind the event backlog", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("MCP request was not written while the event backlog was draining")
	}
}

func appendAgentStreamJournalEvents(t *testing.T, ctx context.Context, deliveryJournal *journal.Journal, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		payload, err := json.Marshal(adapter.AgentEvent{Type: adapter.EventTypeMessageChunk, Text: fmt.Sprintf("event-%d", i)})
		if err != nil {
			t.Fatalf("marshal event %d: %v", i, err)
		}
		if _, err := deliveryJournal.Append(ctx, journal.Event{
			SessionID: "session-1", IncarnationID: "session-1", HarnessGeneration: 1,
			StreamID: "session-1", Type: adapter.EventTypeMessageChunk, Payload: payload,
		}); err != nil {
			t.Fatalf("append event %d: %v", i, err)
		}
	}
}

func newAgentStreamConnPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	connCh := make(chan *websocket.Conn, 1)
	release := make(chan struct{})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connCh <- conn
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		httpServer.Close()
	})

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial WebSocket: %v", err)
	}
	serverConn := <-connCh
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})
	// Keep both directions tiny so a single large frame blocks instead of
	// disappearing into kernel buffers.
	if tcp, ok := serverConn.UnderlyingConn().(*net.TCPConn); ok {
		_ = tcp.SetWriteBuffer(1024)
	}
	if tcp, ok := clientConn.UnderlyingConn().(*net.TCPConn); ok {
		_ = tcp.SetReadBuffer(1024)
	}
	return serverConn, clientConn
}

func setAgentStreamWriteDeadline(t *testing.T, value time.Duration) {
	t.Helper()
	previous := agentStreamWriteDeadline
	agentStreamWriteDeadline = value
	t.Cleanup(func() { agentStreamWriteDeadline = previous })
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
