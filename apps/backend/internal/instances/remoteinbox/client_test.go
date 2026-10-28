package remoteinbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/instances"
	"github.com/stretchr/testify/require"
)

const testInstanceID = "inst-1"

// fakeRemote is one scripted remote backend: a fixed workspace list plus
// per-endpoint handlers, recording the authorization headers and write bodies
// it saw so credential-injection and forwarding assertions can inspect them.
type fakeRemote struct {
	srv *httptest.Server

	mu          sync.Mutex
	authHeaders []string
	writeBodies map[string][]byte
}

func (f *fakeRemote) record(r *http.Request, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authHeaders = append(f.authHeaders, r.Header.Get("Authorization"))
	if body != nil {
		if f.writeBodies == nil {
			f.writeBodies = make(map[string][]byte)
		}
		f.writeBodies[r.Method+" "+r.URL.Path] = body
	}
}

func (f *fakeRemote) authHeaderCount() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.authHeaders...)
}

func (f *fakeRemote) recordedBody(method, path string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writeBodies[method+" "+path]
}

func startFakeRemote(t *testing.T, handle http.HandlerFunc) *fakeRemote {
	t.Helper()
	fake := &fakeRemote{}
	fake.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPut) {
			body, _ = io.ReadAll(r.Body)
		}
		fake.record(r, body)
		handle(w, r)
	}))
	t.Cleanup(fake.srv.Close)
	return fake
}

func (f *fakeRemote) client(token string, err error) *Client {
	return New(
		instances.Instance{ID: testInstanceID, Name: "remote-a", BaseURL: f.srv.URL},
		func() (string, error) { return token, err },
		f.srv.Client(),
	)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// fakeBundle builds one remote inbox row. messages defaults to a non-empty
// array so rows survive the local row renderer's empty-message filter.
func fakeBundle(pendingID, createdAt string) map[string]any {
	return map[string]any{
		"pending_id":    pendingID,
		"task_id":       "task-" + pendingID,
		"session_id":    "session-" + pendingID,
		"session_state": "WAITING_FOR_INPUT",
		"task_title":    "Task " + pendingID,
		"created_at":    createdAt,
		"context":       "ctx " + pendingID,
		"messages":      []any{map[string]any{"id": "m-" + pendingID, "role": "assistant"}},
	}
}

func inboxPage(bundles []map[string]any, hiddenCount int, nextCursor string) map[string]any {
	page := map[string]any{
		"bundles":            bundles,
		"count":              len(bundles),
		"hidden_count":       hiddenCount,
		"next_snooze_expiry": nil,
	}
	if nextCursor != "" {
		page["next_cursor"] = nextCursor
	}
	return page
}

// serveWorkspaces fixes GET /api/v1/workspaces to the given IDs.
func serveWorkspaces(ids ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workspaces" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		list := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			list = append(list, map[string]any{"id": id})
		}
		writeJSON(w, http.StatusOK, map[string]any{"workspaces": list, "total": len(list)})
	}
}

func TestReadInboxAggregatesAcrossWorkspaces(t *testing.T) {
	fake := startFakeRemote(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/workspaces":
			serveWorkspaces("ws-1", "ws-2").ServeHTTP(w, r)
		case r.URL.Path == "/api/v1/clarification-inbox" && r.URL.Query().Get("workspace_id") == "ws-1":
			writeJSON(w, http.StatusOK, inboxPage([]map[string]any{
				fakeBundle("p-2", "2026-10-28T10:01:00Z"),
				fakeBundle("p-1", "2026-10-28T10:00:00Z"),
			}, 1, ""))
		case r.URL.Path == "/api/v1/clarification-inbox" && r.URL.Query().Get("workspace_id") == "ws-2":
			writeJSON(w, http.StatusOK, inboxPage([]map[string]any{
				fakeBundle("p-3", "2026-10-28T10:00:30Z"),
				fakeBundle("p-0", "2026-10-28T10:00:00Z"),
			}, 2, ""))
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		}
	})

	result := fake.client("", nil).ReadInbox(context.Background(), 50)
	require.NotNil(t, result.Page)
	require.Empty(t, result.Degraded)

	page := result.Page
	require.Len(t, page.Bundles, 4)
	// Full order is created_at ascending with pending_id as the tie-break:
	// p-0 and p-1 share 10:00:00, so pending_id decides between them.
	var order []string
	for _, b := range page.Bundles {
		order = append(order, b.PendingID)
		require.Equal(t, testInstanceID, b.InstanceID)
	}
	require.Equal(t, []string{"p-0", "p-1", "p-3", "p-2"}, order)
	require.Equal(t, "ws-2", page.Bundles[0].WorkspaceID)
	require.Equal(t, "ws-1", page.Bundles[1].WorkspaceID)
	require.Equal(t, 4, page.Count)
	require.Equal(t, 3, page.HiddenCount)
	require.False(t, page.HasMore)
}

func TestReadInboxCapAndHasMore(t *testing.T) {
	t.Run("overflow truncates to limit and flags has_more", func(t *testing.T) {
		fake := startFakeRemote(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/workspaces":
				serveWorkspaces("ws-1").ServeHTTP(w, r)
			default:
				writeJSON(w, http.StatusOK, inboxPage([]map[string]any{
					fakeBundle("p-1", "2026-10-28T10:00:00Z"),
					fakeBundle("p-2", "2026-10-28T10:00:01Z"),
					fakeBundle("p-3", "2026-10-28T10:00:02Z"),
				}, 0, ""))
			}
		})
		result := fake.client("", nil).ReadInbox(context.Background(), 2)
		require.NotNil(t, result.Page)
		require.Len(t, result.Page.Bundles, 2)
		require.Equal(t, 2, result.Page.Count)
		require.True(t, result.Page.HasMore)
	})

	t.Run("any workspace next_cursor flags has_more", func(t *testing.T) {
		fake := startFakeRemote(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/v1/workspaces":
				serveWorkspaces("ws-1", "ws-2").ServeHTTP(w, r)
			case r.URL.Query().Get("workspace_id") == "ws-1":
				writeJSON(w, http.StatusOK, inboxPage([]map[string]any{
					fakeBundle("p-1", "2026-10-28T10:00:00Z"),
				}, 0, ""))
			default:
				writeJSON(w, http.StatusOK, inboxPage([]map[string]any{
					fakeBundle("p-2", "2026-10-28T10:00:01Z"),
				}, 0, "cursor-1"))
			}
		})
		result := fake.client("", nil).ReadInbox(context.Background(), 50)
		require.NotNil(t, result.Page)
		require.True(t, result.Page.HasMore)
	})
}

func TestReadInboxDegradedReasons(t *testing.T) {
	t.Run("dial failure is unreachable", func(t *testing.T) {
		dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		dead.Close()
		client := New(
			instances.Instance{ID: testInstanceID, Name: "dead", BaseURL: dead.URL},
			func() (string, error) { return "", nil },
			dead.Client(),
		)
		result := client.ReadInbox(context.Background(), 50)
		require.Nil(t, result.Page)
		require.Equal(t, ReasonUnreachable, result.Degraded)
	})

	t.Run("remote 401 on workspaces is unauthorized", func(t *testing.T) {
		fake := startFakeRemote(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		})
		result := fake.client("", nil).ReadInbox(context.Background(), 50)
		require.Equal(t, ReasonUnauthorized, result.Degraded)
	})

	t.Run("inbox route 404 is inbox_disabled", func(t *testing.T) {
		fake := startFakeRemote(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/workspaces" {
				serveWorkspaces("ws-1").ServeHTTP(w, r)
				return
			}
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		})
		result := fake.client("", nil).ReadInbox(context.Background(), 50)
		require.Equal(t, ReasonInboxDisabled, result.Degraded)
	})

	t.Run("remote 500 is error", func(t *testing.T) {
		fake := startFakeRemote(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/workspaces" {
				serveWorkspaces("ws-1").ServeHTTP(w, r)
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "boom"})
		})
		result := fake.client("", nil).ReadInbox(context.Background(), 50)
		require.Equal(t, ReasonError, result.Degraded)
	})

	t.Run("zero workspaces is an ok empty page", func(t *testing.T) {
		fake := startFakeRemote(t, func(w http.ResponseWriter, r *http.Request) {
			serveWorkspaces().ServeHTTP(w, r)
		})
		result := fake.client("", nil).ReadInbox(context.Background(), 50)
		require.Empty(t, result.Degraded)
		require.NotNil(t, result.Page)
		require.NotNil(t, result.Page.Bundles)
		require.Empty(t, result.Page.Bundles)
		require.Equal(t, 0, result.Page.Count)
	})
}

func TestReadHiddenAggregates(t *testing.T) {
	fake := startFakeRemote(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/workspaces":
			serveWorkspaces("ws-1", "ws-2").ServeHTTP(w, r)
		case r.URL.Path == "/api/v1/clarification-inbox/hidden" && r.URL.Query().Get("workspace_id") == "ws-1":
			until := "2026-10-28T12:00:00Z"
			writeJSON(w, http.StatusOK, hiddenPage([]map[string]any{
				hiddenBundle("p-1", "2026-10-28T10:00:00Z", "snoozed", &until),
			}, 5))
		case r.URL.Path == "/api/v1/clarification-inbox/hidden":
			writeJSON(w, http.StatusOK, hiddenPage([]map[string]any{
				hiddenBundle("p-2", "2026-10-28T09:00:00Z", "dismissed", nil),
			}, 2))
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		}
	})
	result := fake.client("", nil).ReadHidden(context.Background(), 50)
	require.Empty(t, result.Degraded)
	require.NotNil(t, result.Page)
	require.Len(t, result.Page.Bundles, 2)
	require.Equal(t, "p-2", result.Page.Bundles[0].PendingID)
	require.Equal(t, "dismissed", result.Page.Bundles[0].State)
	require.Equal(t, "snoozed", result.Page.Bundles[1].State)
	require.NotNil(t, result.Page.Bundles[1].SnoozeUntil)
	require.Equal(t, 7, result.Page.Total)
}

func hiddenBundle(pendingID, createdAt, state string, snoozeUntil *string) map[string]any {
	row := fakeBundle(pendingID, createdAt)
	row["state"] = state
	row["snooze_until"] = snoozeUntil
	return row
}

func hiddenPage(bundles []map[string]any, total int) map[string]any {
	page := inboxPage(bundles, 0, "")
	page["total"] = total
	return page
}

func TestRespondPassthrough(t *testing.T) {
	envelope := `{"outcome":"win","message_ids":["m-1"]}`
	conflict := `{"error":{"code":"not_active","detail":"already answered"}}`
	reply := "ok"
	fake := startFakeRemote(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clarification/p-1/respond" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		if reply == "conflict" {
			reply = "ok"
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(conflict))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(envelope))
	})
	client := fake.client("", nil)

	status, body, err := client.Respond(context.Background(), "p-1", []byte(`{"answers":[]}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, envelope, string(body))
	require.JSONEq(t, `{"answers":[]}`, string(fake.recordedBody(http.MethodPost, "/api/v1/clarification/p-1/respond")))

	reply = "conflict"
	status, body, err = client.Respond(context.Background(), "p-1", []byte(`{}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusConflict, status)
	require.JSONEq(t, conflict, string(body))
}

func TestSidecarForwarding(t *testing.T) {
	putReply := "ok"
	deleteReply := "ok"
	fake := startFakeRemote(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clarification-inbox/sidecar/p-1" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		reply := "ok"
		switch r.Method {
		case http.MethodPut:
			reply, putReply = putReply, "ok"
		case http.MethodDelete:
			reply, deleteReply = deleteReply, "ok"
		}
		switch reply {
		case "bad":
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad duration"})
		case "gone":
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown pending"})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	client := fake.client("", nil)

	status, err := client.UpsertSidecar(context.Background(), "p-1", "snoozed", "1h")
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, status)
	require.JSONEq(t, `{"state":"snoozed","snooze_duration":"1h"}`,
		string(fake.recordedBody(http.MethodPut, "/api/v1/clarification-inbox/sidecar/p-1")))

	status, err = client.UpsertSidecar(context.Background(), "p-1", "dismissed", "")
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, status)

	putReply = "bad"
	status, err = client.UpsertSidecar(context.Background(), "p-1", "snoozed", "2h")
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, status)

	putReply = "gone"
	status, err = client.UpsertSidecar(context.Background(), "p-1", "snoozed", "1h")
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, status)

	status, err = client.DeleteSidecar(context.Background(), "p-1")
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, status)

	deleteReply = "gone"
	status, err = client.DeleteSidecar(context.Background(), "p-1")
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, status)
}

func TestCredentialInjection(t *testing.T) {
	fake := startFakeRemote(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/workspaces" {
			serveWorkspaces("ws-1").ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusOK, inboxPage(nil, 0, ""))
	})

	t.Run("PAT dials as bearer", func(t *testing.T) {
		result := fake.client("pat-123", nil).ReadInbox(context.Background(), 50)
		require.Empty(t, result.Degraded)
		headers := fake.authHeaderCount()
		require.Len(t, headers, 2)
		for _, h := range headers {
			require.Equal(t, "Bearer pat-123", h)
		}
	})

	t.Run("no PAT still dials without an auth header", func(t *testing.T) {
		fake2 := startFakeRemote(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/workspaces" {
				serveWorkspaces("ws-1").ServeHTTP(w, r)
				return
			}
			writeJSON(w, http.StatusOK, inboxPage(nil, 0, ""))
		})
		result := fake2.client("", nil).ReadInbox(context.Background(), 50)
		require.Empty(t, result.Degraded)
		for _, h := range fake2.authHeaderCount() {
			require.Empty(t, h)
		}
	})
}

func TestReadDeadline(t *testing.T) {
	release := make(chan struct{})
	fake := startFakeRemote(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
	})
	// defer (not t.Cleanup) so the handlers unblock before startFakeRemote's
	// srv.Close cleanup waits on them.
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	result := fake.client("", nil).ReadInbox(ctx, 50)
	require.Nil(t, result.Page)
	require.Equal(t, ReasonUnreachable, result.Degraded)
}

func TestCredentialReadFailureIsUnauthorized(t *testing.T) {
	fake := startFakeRemote(t, func(w http.ResponseWriter, r *http.Request) {
		serveWorkspaces("ws-1").ServeHTTP(w, r)
	})
	result := fake.client("", fmt.Errorf("secret store unavailable")).ReadInbox(context.Background(), 50)
	require.Equal(t, ReasonUnauthorized, result.Degraded)
}

func TestURLPathHandling(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	var lastQuery string
	fake := startFakeRemote(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		lastQuery = r.URL.RawQuery
		mu.Unlock()
		if r.URL.Path == "/api/v1/workspaces" {
			serveWorkspaces("ws-1").ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusOK, inboxPage(nil, 0, ""))
	})
	client := New(
		instances.Instance{ID: testInstanceID, Name: "trailing", BaseURL: strings.TrimSuffix(fake.srv.URL, "/") + "/"},
		func() (string, error) { return "", nil },
		fake.srv.Client(),
	)
	result := client.ReadInbox(context.Background(), 7)
	require.Empty(t, result.Degraded)
	require.Equal(t, []string{"/api/v1/workspaces", "/api/v1/clarification-inbox"}, paths)
	require.Equal(t, "limit=7&workspace_id=ws-1", lastQuery)
}
