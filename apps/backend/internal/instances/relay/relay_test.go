package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/instances"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"go.uber.org/zap"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

type memoryStore struct {
	mu   sync.Mutex
	rows map[string]bool
}

func (s *memoryStore) Record(_ context.Context, owner, instance, event, id, task, session, title, body string, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := instance + event + id
	if s.rows[key] {
		return false, nil
	}
	s.rows[key] = true
	return true, nil
}
func (s *memoryStore) LatestOccurrenceTime(context.Context, string) (time.Time, error) {
	return time.Time{}, nil
}

type broadcasts struct{ messages chan *ws.Message }

func (b *broadcasts) BroadcastToUser(owner string, msg *ws.Message) bool {
	if owner != "owner" {
		panic("wrong owner")
	}
	b.messages <- msg
	return true
}
func fixture(t *testing.T, handler http.Handler) (*Relay, *memoryStore, *broadcasts) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	log, err := logger.NewFromZap(zap.NewNop())
	require.NoError(t, err)
	store := &memoryStore{rows: map[string]bool{}}
	hub := &broadcasts{messages: make(chan *ws.Message, 20)}
	r := NewRelay(instances.Instance{ID: "remote", OwnerUserID: "owner", BaseURL: server.URL, Name: "Remote"}, "token", store, hub, log)
	t.Cleanup(r.Stop)
	return r, store, hub
}
func occurrence() Occurrence {
	return Occurrence{Event: "session.turn_finished", OccurrenceID: "turn", TaskID: "task", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}
func await(t *testing.T, hub *broadcasts) *ws.Message {
	t.Helper()
	select {
	case m := <-hub.messages:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no broadcast")
		return nil
	}
}
func TestRelayRecordsAndBroadcastsNewOccurrenceOnly(t *testing.T) {
	done := make(chan struct{})
	r, s, h := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/ws" {
			_, _ = w.Write([]byte("[]"))
			return
		}
		require.Equal(t, "Bearer token", req.Header.Get("Authorization"))
		c, err := (&websocket.Upgrader{}).Upgrade(w, req, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		var subscription ws.Message
		if c.ReadJSON(&subscription) != nil {
			return
		}
		o := occurrence()
		m, _ := ws.NewNotification(o.Event, o)
		_ = c.WriteJSON(m)
		_ = c.WriteJSON(m)
		unknown, _ := ws.NewNotification("unrelated", o)
		_ = c.WriteJSON(unknown)
		empty, _ := ws.NewNotification("office.inbox_item", map[string]string{"title": "missing identity"})
		_ = c.WriteJSON(empty)
		close(done)
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	r.Start(t.Context())
	m := await(t, h)
	require.Equal(t, "remote_notification", m.Action)
	var payload Occurrence
	require.NoError(t, json.Unmarshal(m.Payload, &payload))
	require.NotEmpty(t, payload.ID)
	require.Equal(t, "remote", payload.InstanceID)
	require.Equal(t, "session.turn_finished", payload.Event)
	<-done
	r.Stop()
	require.Len(t, s.rows, 1)
	require.Empty(t, h.messages)
}
func TestRelayDedupAcrossLiveAndReconcile(t *testing.T) {
	o := occurrence()
	r, s, h := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode([]Occurrence{o}))
	}))
	require.NoError(t, r.record(t.Context(), o))
	require.NoError(t, r.reconcile(t.Context()))
	require.Len(t, s.rows, 1)
	require.Len(t, h.messages, 1)
}
func TestParkedDeferralInherited(t *testing.T) {
	r, s, h := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { _, _ = w.Write([]byte("[]")) }))
	require.NoError(t, r.reconcile(t.Context()))
	require.Empty(t, s.rows)
	require.Empty(t, h.messages)
	require.NoError(t, r.record(t.Context(), occurrence()))
	require.Len(t, s.rows, 1)
	require.Len(t, h.messages, 1)
}
func TestRelayReconnectBackoffNoSleep(t *testing.T) {
	attempted := make(chan struct{}, 4)
	r, _, _ := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/ws" {
			attempted <- struct{}{}
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("[]"))
	}))
	r.Start(t.Context())
	select {
	case <-attempted:
	case <-time.After(5 * time.Second):
		t.Fatal("no attempt")
	}
	select {
	case <-attempted:
	case <-time.After(5 * time.Second):
		t.Fatal("retry did not reconnect")
	}
	r.Stop()
	r.Start(t.Context())
	select {
	case <-attempted:
	case <-time.After(5 * time.Second):
		t.Fatal("restart failed")
	}
	r.Stop()
}
