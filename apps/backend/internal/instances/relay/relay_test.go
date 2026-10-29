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

// relayedCall is one HandleRelayedOccurrence invocation as the fake notifier
// observed it: the explicit recipient plus the origin identity and the
// occurrence itself.
type relayedCall struct {
	userID             string
	originInstanceID   string
	originInstanceName string
	eventType          string
	taskID             string
	sessionID          string
	occurrenceID       string
	title              string
	body               string
}

type fakeNotifier struct {
	mu    sync.Mutex
	calls []relayedCall
}

func (n *fakeNotifier) HandleRelayedOccurrence(_ context.Context, userID, originInstanceID, originInstanceName, eventType, taskID, sessionID, occurrenceID, title, body string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, relayedCall{
		userID:             userID,
		originInstanceID:   originInstanceID,
		originInstanceName: originInstanceName,
		eventType:          eventType,
		taskID:             taskID,
		sessionID:          sessionID,
		occurrenceID:       occurrenceID,
		title:              title,
		body:               body,
	})
}

func (n *fakeNotifier) recorded() []relayedCall {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]relayedCall(nil), n.calls...)
}

func fixture(t *testing.T, handler http.Handler) (*Relay, *memoryStore, *fakeNotifier) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	log, err := logger.NewFromZap(zap.NewNop())
	require.NoError(t, err)
	store := &memoryStore{rows: map[string]bool{}}
	notifier := &fakeNotifier{}
	r := NewRelay(instances.Instance{ID: "remote", OwnerUserID: "owner", BaseURL: server.URL, Name: "Remote"}, "token", store, notifier, log)
	t.Cleanup(r.Stop)
	return r, store, notifier
}
func occurrence() Occurrence {
	return Occurrence{Event: "session.turn_finished", OccurrenceID: "turn", TaskID: "task", Title: "Agent turn finished", Body: "The agent finished a turn.", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

// TestRelayV2ForwardsEveryAllowedEventKindToNotifier pins the relayed set
// (v2): each of the four semantic kinds reaches the unified notification
// pipeline as one HandleRelayedOccurrence call addressed to the instance
// owner with the origin identity attached.
func TestRelayV2ForwardsEveryAllowedEventKindToNotifier(t *testing.T) {
	kinds := []string{
		"session.turn_finished",
		"session.clarification_requested",
		"office.inbox_item",
		"task.manual_step_entered",
	}
	r, s, n := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { _, _ = w.Write([]byte("[]")) }))
	for index, kind := range kinds {
		o := occurrence()
		o.Event = kind
		o.OccurrenceID = "occurrence-" + kind
		require.NoError(t, r.record(t.Context(), o))
		calls := n.recorded()
		require.Len(t, calls, index+1, "kind %s was not forwarded", kind)
		call := calls[index]
		require.Equal(t, "owner", call.userID, "recipient must be the instance owner, not a locally resolved owner")
		require.Equal(t, "remote", call.originInstanceID)
		require.Equal(t, "Remote", call.originInstanceName)
		require.Equal(t, kind, call.eventType)
		require.Equal(t, "task", call.taskID)
		require.Equal(t, "occurrence-"+kind, call.occurrenceID)
		require.Equal(t, o.Title, call.title)
		require.Equal(t, o.Body, call.body)
	}
	require.Len(t, s.rows, len(kinds))
}

// TestRelayIgnoresDisallowedAndUnidentifiedOccurrences pins the closed set:
// an event kind outside the relayed v2 list, and an occurrence with no
// identity, are ignored before the store or the notifier sees anything.
func TestRelayIgnoresDisallowedAndUnidentifiedOccurrences(t *testing.T) {
	r, s, n := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { _, _ = w.Write([]byte("[]")) }))
	disallowed := occurrence()
	disallowed.Event = "system.update_available"
	require.NoError(t, r.record(t.Context(), disallowed))
	unidentified := occurrence()
	unidentified.OccurrenceID = ""
	require.NoError(t, r.record(t.Context(), unidentified))
	require.Empty(t, n.recorded())
	require.Empty(t, s.rows)
}

// TestRelayDedupAcrossLiveAndReconcile pins at-most-once delivery: the same
// occurrence observed by the live subscription and again by the
// reconciliation poll reaches the notifier exactly once.
func TestRelayDedupAcrossLiveAndReconcile(t *testing.T) {
	o := occurrence()
	r, s, n := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode([]Occurrence{o}))
	}))
	require.NoError(t, r.record(t.Context(), o))
	require.NoError(t, r.reconcile(t.Context()))
	require.Len(t, s.rows, 1)
	require.Len(t, n.recorded(), 1)
}

func TestParkedDeferralInherited(t *testing.T) {
	r, s, n := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { _, _ = w.Write([]byte("[]")) }))
	require.NoError(t, r.reconcile(t.Context()))
	require.Empty(t, s.rows)
	require.Empty(t, n.recorded())
	require.NoError(t, r.record(t.Context(), occurrence()))
	require.Len(t, s.rows, 1)
	require.Len(t, n.recorded(), 1)
}

// TestRelayForwardsLiveOccurrenceOnceToNotifier drives the live socket: a
// replayed frame, an unrelated event, an identity-less frame, and a frame a
// peer already relayed from another instance yield exactly one notifier call,
// and nothing is broadcast under a remote-only notification action anymore
// (the pipeline owns delivery).
func TestRelayForwardsLiveOccurrenceOnceToNotifier(t *testing.T) {
	done := make(chan struct{})
	r, s, n := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
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
		relayed := occurrence()
		relayed.OccurrenceID = "already-relayed"
		relayed.OriginInstanceID = "origin"
		relayedFrame, _ := ws.NewNotification(relayed.Event, relayed)
		_ = c.WriteJSON(relayedFrame)
		close(done)
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	r.Start(t.Context())
	require.Eventually(t, func() bool { return len(n.recorded()) == 1 }, 5*time.Second, 10*time.Millisecond)
	call := n.recorded()[0]
	require.Equal(t, "owner", call.userID)
	require.Equal(t, "remote", call.originInstanceID)
	require.Equal(t, "Remote", call.originInstanceName)
	require.Equal(t, "session.turn_finished", call.eventType)
	require.Equal(t, "turn", call.occurrenceID)
	<-done
	r.Stop()
	require.Len(t, s.rows, 1)
	require.Len(t, n.recorded(), 1)
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
