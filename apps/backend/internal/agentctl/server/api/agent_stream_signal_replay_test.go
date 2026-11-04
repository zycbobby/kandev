package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kandev/kandev/internal/agentctl/journal"
	"github.com/kandev/kandev/internal/agentctl/server/adapter"
	"github.com/kandev/kandev/internal/agentctl/types/streams"
	mcpserver "github.com/kandev/kandev/internal/mcp/server"
	ws "github.com/kandev/kandev/pkg/websocket"
)

func attachSignalReplayClient(t *testing.T, server *Server, procMgr deliveryJournalProvider) *mcpserver.ChannelBackendClient {
	t.Helper()
	backend := mcpserver.NewChannelBackendClient(server.logger)
	t.Cleanup(backend.Close)
	backend.SetSessionID("session-1")
	backend.SetPendingSignalStore(func() (journal.PendingSignalStore, error) {
		return procMgr.DeliveryJournal()
	})
	server.mcpBackendClient = backend
	return backend
}

// deliveryJournalProvider is the narrow manager surface replay depends on.
type deliveryJournalProvider interface {
	DeliveryJournal() (*journal.Journal, error)
}

func seedCapturedStepComplete(t *testing.T, deliveryJournal *journal.Journal, id string) journal.PendingSignal {
	t.Helper()
	signal := journal.PendingSignal{
		ID:        id,
		Action:    ws.ActionMCPStepComplete,
		SessionID: "session-1",
		Payload:   json.RawMessage(`{"task_id":"task-1","session_id":"session-1","summary":"done"}`),
		Attempts:  1,
	}
	if err := deliveryJournal.PutPendingSignal(context.Background(), signal); err != nil {
		t.Fatalf("seed captured signal: %v", err)
	}
	return signal
}

func TestSignalReplayWritesCapturedSignalOnConnect(t *testing.T) {
	server, procMgr, deliveryJournal := newDurableDeliveryTestServer(t)
	backend := attachSignalReplayClient(t, server, procMgr)
	seedCapturedStepComplete(t, deliveryJournal, "req-replay-1")

	connectionCh := make(chan *websocket.Conn, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connectionCh <- conn
	}))
	t.Cleanup(httpServer.Close)
	clientConn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	t.Cleanup(func() { _ = clientConn.Close() })
	serverConn := <-connectionCh

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	writeMessage := func(data []byte) error {
		if err := serverConn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
			return err
		}
		return serverConn.WriteMessage(websocket.TextMessage, data)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go server.runAgentStreamWriter(ctx, serverConn, "stream-replay", nil, backend.GetRequestChannel(), writeMessage, &wg)

	if err := clientConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, data, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("read replayed signal: %v", err)
	}
	var request ws.Message
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatalf("decode replayed signal: %v", err)
	}
	if request.Type != ws.MessageTypeRequest || request.ID != "req-replay-1" || request.Action != ws.ActionMCPStepComplete {
		t.Fatalf("replayed request = %+v, want original step_complete request", request)
	}

	response, err := ws.NewResponse(request.ID, request.Action, map[string]any{"accepted": true})
	if err != nil {
		t.Fatalf("build response: %v", err)
	}
	backend.HandleResponse(response)
	pending, err := deliveryJournal.ListPendingSignals(context.Background(), journal.MaxPendingSignals)
	if err != nil {
		t.Fatalf("list pending signals: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("captured signals after response = %d, want 0", len(pending))
	}

	cancel()
	wg.Wait()
}

func TestSignalReplayStoreUnavailableKeepsStreamAlive(t *testing.T) {
	server, _, _ := newDurableDeliveryTestServer(t)
	backend := mcpserver.NewChannelBackendClient(server.logger)
	t.Cleanup(backend.Close)
	server.mcpBackendClient = backend
	backend.SetPendingSignalStore(func() (journal.PendingSignalStore, error) {
		return nil, errors.New("journal unavailable")
	})

	wrote := false
	if !server.replayPendingSignals("stream-1", func([]byte) error { wrote = true; return nil }) {
		t.Fatal("store failure must not terminate the stream writer")
	}
	if wrote {
		t.Fatal("store failure must not write an empty replay")
	}
}

func TestSignalReplayWriteFailureStopsWriterAndKeepsCapture(t *testing.T) {
	server, procMgr, deliveryJournal := newDurableDeliveryTestServer(t)
	attachSignalReplayClient(t, server, procMgr)
	seedCapturedStepComplete(t, deliveryJournal, "req-replay-2")

	if server.replayPendingSignals("stream-1", func([]byte) error { return errors.New("socket write failed") }) {
		t.Fatal("write failure must end the stream writer")
	}
	pending, err := deliveryJournal.ListPendingSignals(context.Background(), journal.MaxPendingSignals)
	if err != nil {
		t.Fatalf("list pending signals: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "req-replay-2" {
		t.Fatalf("pending signals after write failure = %+v, want the captured record", pending)
	}
}

// TestSignalDeliveryNoticeReplayedOnConnect proves the completion-pending
// notice travels through the durable journal and is replayed to the backend
// after a reconnect, not only while a stream is attached.
func TestSignalDeliveryNoticeReplayedOnConnect(t *testing.T) {
	server, procMgr, _ := newDurableDeliveryTestServer(t)
	procMgr.PublishSignalDeliveryFailure(streams.SignalDeliveryFailure{
		RequestID:      "req-notice",
		Action:         ws.ActionMCPStepComplete,
		SessionID:      "session-1",
		Reason:         streams.SignalDeliveryFailureReasonWindowExceeded,
		Attempts:       2,
		FirstAttemptAt: time.Now().UTC(),
	})

	var replayed []adapter.AgentEvent
	if _, err := server.replayAgentStream(context.Background(), 0, func(event adapter.AgentEvent) error {
		replayed = append(replayed, event)
		return nil
	}); err != nil {
		t.Fatalf("replay delivery stream: %v", err)
	}
	var notice *streams.SignalDeliveryFailure
	for _, event := range replayed {
		if event.Type == streams.EventTypeSignalDeliveryFailed {
			notice = event.SignalDeliveryFailure
		}
	}
	if notice == nil {
		t.Fatal("durable replay did not deliver the completion-pending notice")
	}
	if notice.RequestID != "req-notice" || notice.Reason != streams.SignalDeliveryFailureReasonWindowExceeded || notice.Attempts != 2 {
		t.Fatalf("replayed notice = %+v", notice)
	}
}
