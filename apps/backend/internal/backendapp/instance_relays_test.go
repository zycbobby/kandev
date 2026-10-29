package backendapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/common/config"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/db"
	instancestore "github.com/kandev/kandev/internal/instances/store/sqlite"
	ws "github.com/kandev/kandev/pkg/websocket"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"go.uber.org/zap"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// relayedNotification is one HandleRelayedOccurrence call as the test
// notifier observed it.
type relayedNotification struct {
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

type channelNotifier struct {
	calls chan relayedNotification
}

func (n *channelNotifier) HandleRelayedOccurrence(_ context.Context, userID, originInstanceID, originInstanceName, eventType, taskID, sessionID, occurrenceID, title, body string) {
	n.calls <- relayedNotification{
		userID:             userID,
		originInstanceID:   originInstanceID,
		originInstanceName: originInstanceName,
		eventType:          eventType,
		taskID:             taskID,
		sessionID:          sessionID,
		occurrenceID:       occurrenceID,
		title:              title,
		body:               body,
	}
}

// fakeRemoteInstance serves the two surfaces a relay consumes: the live
// notification socket and the occurrence reconciliation feed. It dispatches
// one clarification occurrence over the socket once a relay subscribes.
func fakeRemoteInstance(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/ws" {
			_, _ = w.Write([]byte("[]"))
			return
		}
		c, err := (&websocket.Upgrader{}).Upgrade(w, req, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		var subscription ws.Message
		if c.ReadJSON(&subscription) != nil {
			return
		}
		occurrence := map[string]string{
			"occurrence_id": "pending-1",
			"task_id":       "remote-task",
			"session_id":    "remote-session",
			"title":         "Agent needs your answer",
			"body":          `The agent asked a question on "Remote task".`,
		}
		frame, err := ws.NewNotification("session.clarification_requested", occurrence)
		if err == nil {
			_ = c.WriteJSON(frame)
		}
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// TestInstanceRelaysWireTheNotifierThroughToDelivery covers the assembly
// seam: initInstanceRelays must hand the notification service to every
// enabled instance's relay, so a remote occurrence is delivered to the
// instance owner with the origin identity instead of a remote-only
// broadcast surface.
func TestInstanceRelaysWireTheNotifierThroughToDelivery(t *testing.T) {
	remote := fakeRemoteInstance(t)
	raw, err := db.OpenSQLite(filepath.Join(t.TempDir(), "relay.db"))
	require.NoError(t, err)
	database := sqlx.NewDb(raw, "sqlite3")
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	repo, err := instancestore.NewWithDB(t.Context(), database, database)
	require.NoError(t, err)
	log, err := logger.NewFromZap(zap.NewNop())
	require.NoError(t, err)
	cfg := &config.Config{}
	cfg.Features.MultiInstance = true
	services := &Services{}
	repos := &Repositories{Instances: repo}
	stopProxy := initInstanceProxies(t.Context(), cfg, repos, services, log)
	t.Cleanup(stopProxy)
	notifier := &channelNotifier{calls: make(chan relayedNotification, 4)}
	stop := initInstanceRelays(t.Context(), cfg, repos, services, notifier, log)
	t.Cleanup(stop)

	_, err = services.Instances.CreateInstance(t.Context(), "owner", "Remote Studio", remote.URL, "", true)
	require.NoError(t, err)
	require.Len(t, services.InstanceRelays.entries, 1)

	select {
	case call := <-notifier.calls:
		require.Equal(t, "owner", call.userID)
		require.NotEmpty(t, call.originInstanceID)
		require.Equal(t, "Remote Studio", call.originInstanceName)
		require.Equal(t, "session.clarification_requested", call.eventType)
		require.Equal(t, "remote-task", call.taskID)
		require.Equal(t, "remote-session", call.sessionID)
		require.Equal(t, "pending-1", call.occurrenceID)
		require.Equal(t, "Agent needs your answer", call.title)
		require.Equal(t, `The agent asked a question on "Remote task".`, call.body)
	case <-time.After(10 * time.Second):
		t.Fatal("relay did not deliver the remote occurrence to the notifier")
	}
}

func TestInstanceRelaysDisabledLeavesNoManager(t *testing.T) {
	services := &Services{}
	stop := initInstanceRelays(t.Context(), &config.Config{}, nil, services, nil, nil)
	stop()
	require.Nil(t, services.InstanceRelays)
}
