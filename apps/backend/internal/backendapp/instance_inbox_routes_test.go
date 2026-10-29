package backendapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kandev/kandev/internal/instances"
	"github.com/kandev/kandev/internal/instances/remoteinbox"
	userstore "github.com/kandev/kandev/internal/user/store"
	"github.com/stretchr/testify/require"
)

// fakeInstanceInboxForwarder stands in for the remote fan-out client so route
// tests never touch the network.
type fakeInstanceInboxForwarder struct {
	mu                 sync.Mutex
	inboxResult        remoteinbox.ReadResult
	hiddenResult       remoteinbox.ReadResult
	needsYouResult     remoteinbox.NeedsYouReadResult
	needsYouHidden     remoteinbox.NeedsYouReadResult
	readInboxCalls     int
	readHiddenCalls    int
	readNeedsYouCalls  int
	needsYouHiddenRead int
	respondCalls       []string
	respondStatus      int
	respondBody        []byte
	respondErr         error
	upsertCalls        []string
	upsertBody         string
	upsertStatus       int
	upsertErr          error
	deleteCalls        []string
	deleteStatus       int
	deleteErr          error
}

func (f *fakeInstanceInboxForwarder) ReadInbox(ctx context.Context, limit int) remoteinbox.ReadResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readInboxCalls++
	return f.inboxResult
}

func (f *fakeInstanceInboxForwarder) ReadHidden(ctx context.Context, limit int) remoteinbox.ReadResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readHiddenCalls++
	return f.hiddenResult
}

func (f *fakeInstanceInboxForwarder) ReadNeedsYou(ctx context.Context, limit int) remoteinbox.NeedsYouReadResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readNeedsYouCalls++
	return f.needsYouResult
}

func (f *fakeInstanceInboxForwarder) ReadNeedsYouHidden(ctx context.Context, limit int) remoteinbox.NeedsYouReadResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.needsYouHiddenRead++
	return f.needsYouHidden
}

func (f *fakeInstanceInboxForwarder) Respond(ctx context.Context, pendingID string, body []byte) (int, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.respondCalls = append(f.respondCalls, pendingID)
	return f.respondStatus, f.respondBody, f.respondErr
}

func (f *fakeInstanceInboxForwarder) UpsertSidecar(ctx context.Context, pendingID, state, duration string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upsertCalls = append(f.upsertCalls, pendingID)
	f.upsertBody = state + "|" + duration
	return f.upsertStatus, f.upsertErr
}

func (f *fakeInstanceInboxForwarder) DeleteSidecar(ctx context.Context, pendingID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls = append(f.deleteCalls, pendingID)
	return f.deleteStatus, f.deleteErr
}

func (f *fakeInstanceInboxForwarder) calls() (responds, upserts, deletes []string, upsertBody string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.respondCalls...), append([]string(nil), f.upsertCalls...),
		append([]string(nil), f.deleteCalls...), f.upsertBody
}

func (f *fakeInstanceInboxForwarder) readCounts() (inbox, hidden int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.readInboxCalls, f.readHiddenCalls
}

func (f *fakeInstanceInboxForwarder) needsYouReadCounts() (page, hidden int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.readNeedsYouCalls, f.needsYouHiddenRead
}

// inboxForwarderFixture wires the routes over per-instance fakes. The
// returned register function binds a fake to an instance ID; requests to an
// unbound ID hit a default fake.
func inboxForwarderFixture(t *testing.T) (*gin.Engine, *Services, func(id string, fake *fakeInstanceInboxForwarder)) {
	t.Helper()
	services, cfg := newInstanceShellFixture(t)
	cfg.Features.NeedsYouInbox = true
	fakes := map[string]*fakeInstanceInboxForwarder{}
	router := gin.New()
	registerInstanceInboxRoutesWithForwarder(
		routeParams{router: router, services: services, features: cfg.Features},
		func(inst instances.Instance) remoteInboxForwarder {
			if fake, ok := fakes[inst.ID]; ok {
				return fake
			}
			return &fakeInstanceInboxForwarder{}
		},
	)
	return router, services, func(id string, fake *fakeInstanceInboxForwarder) { fakes[id] = fake }
}

func createOwnedInstance(t *testing.T, services *Services, name, baseURL string, enabled bool) *instances.Instance {
	t.Helper()
	item, err := services.Instances.CreateInstance(t.Context(), userstore.DefaultUserID, name, baseURL, "", enabled)
	require.NoError(t, err)
	return item
}

func TestInstanceInboxRoutesGatedByNeedsYouInboxFlag(t *testing.T) {
	services, cfg := newInstanceShellFixture(t)
	cfg.Features.NeedsYouInbox = false
	router := gin.New()
	registerInstanceInboxRoutesWithForwarder(
		routeParams{router: router, services: services, features: cfg.Features},
		func(inst instances.Instance) remoteInboxForwarder { return &fakeInstanceInboxForwarder{} },
	)

	paths := []struct {
		method, path string
	}{
		{http.MethodGet, "/api/v1/instances/x/clarification-inbox"},
		{http.MethodGet, "/api/v1/instances/x/clarification-inbox/hidden"},
		{http.MethodGet, "/api/v1/instances/x/needs-you-inbox"},
		{http.MethodGet, "/api/v1/instances/x/needs-you-inbox/hidden"},
		{http.MethodPost, "/api/v1/instances/x/clarification/p-1/respond"},
		{http.MethodPut, "/api/v1/instances/x/clarification-inbox/sidecar/p-1"},
		{http.MethodDelete, "/api/v1/instances/x/clarification-inbox/sidecar/p-1"},
	}
	for _, p := range paths {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(p.method, p.path, nil))
		require.Equal(t, http.StatusNotFound, rec.Code, p.path)
	}
}

func TestInstanceInboxForeignInstanceIs404(t *testing.T) {
	router, services, register := inboxForwarderFixture(t)
	watched := &fakeInstanceInboxForwarder{}
	register("watched", watched)

	foreign, err := services.Instances.CreateInstance(t.Context(), "user-other", "Foreign box", "http://localhost:1", "", true)
	require.NoError(t, err)
	disabled := createOwnedInstance(t, services, "Disabled box", "http://localhost:2", false)

	for _, id := range []string{foreign.ID, disabled.ID, "missing-id"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instances/"+id+"/clarification-inbox", nil))
		require.Equal(t, http.StatusNotFound, rec.Code, id)
		require.JSONEq(t, `{"error":"instance not found"}`, rec.Body.String())
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instances/"+id+"/needs-you-inbox", nil))
		require.Equal(t, http.StatusNotFound, rec.Code, id)
	}
	responds, _, _, _ := watched.calls()
	require.Empty(t, responds)
}

func TestInstanceInboxPageEnvelope(t *testing.T) {
	t.Run("ok page carries instance identity and counts", func(t *testing.T) {
		router, services, register := inboxForwarderFixture(t)
		item := createOwnedInstance(t, services, "Remote A", "http://localhost:1", true)
		register(item.ID, &fakeInstanceInboxForwarder{inboxResult: remoteinbox.ReadResult{Page: &remoteinbox.Page{
			Bundles: []remoteinbox.Bundle{{
				PendingID: "p-1", WorkspaceID: "ws-9",
				CreatedAt: "2026-10-28T10:00:00Z", Messages: []byte(`[{"id":"m-1"}]`),
			}},
			Count: 1, HiddenCount: 2, HasMore: true,
		}}})

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instances/"+item.ID+"/clarification-inbox", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{
			"instance_id": "`+item.ID+`",
			"instance_name": "Remote A",
			"status": "ok",
			"bundles": [{"pending_id":"p-1","task_id":"","session_id":"","session_state":"","task_title":"","created_at":"2026-10-28T10:00:00Z","context":"","messages":[{"id":"m-1"}],"instance_id":"`+item.ID+`","workspace_id":"ws-9"}],
			"count": 1,
			"hidden_count": 2,
			"next_snooze_expiry": null,
			"has_more": true
		}`, rec.Body.String())
	})

	t.Run("degraded page is 200 with reason and empty bundles", func(t *testing.T) {
		router, services, register := inboxForwarderFixture(t)
		item := createOwnedInstance(t, services, "Remote A", "http://localhost:1", true)
		register(item.ID, &fakeInstanceInboxForwarder{inboxResult: remoteinbox.ReadResult{Degraded: remoteinbox.ReasonUnreachable}})

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instances/"+item.ID+"/clarification-inbox", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{
			"instance_id": "`+item.ID+`",
			"instance_name": "Remote A",
			"status": "degraded",
			"degraded_reason": "unreachable",
			"bundles": [],
			"count": 0,
			"hidden_count": 0,
			"next_snooze_expiry": null,
			"has_more": false
		}`, rec.Body.String())
	})

	t.Run("hidden page adds total and next_snooze_expiry", func(t *testing.T) {
		router, services, register := inboxForwarderFixture(t)
		item := createOwnedInstance(t, services, "Remote A", "http://localhost:1", true)
		expiry := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
		register(item.ID, &fakeInstanceInboxForwarder{hiddenResult: remoteinbox.ReadResult{Page: &remoteinbox.Page{
			Bundles: []remoteinbox.Bundle{}, Total: 7, HiddenCount: 7, NextSnoozeExpiry: &expiry,
		}}})

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instances/"+item.ID+"/clarification-inbox/hidden", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{
			"instance_id": "`+item.ID+`",
			"instance_name": "Remote A",
			"status": "ok",
			"bundles": [],
			"count": 0,
			"hidden_count": 7,
			"total": 7,
			"next_snooze_expiry": "2026-10-28T12:00:00Z",
			"has_more": false
		}`, rec.Body.String())
	})

	t.Run("invalid limit is 400", func(t *testing.T) {
		router, services, register := inboxForwarderFixture(t)
		item := createOwnedInstance(t, services, "Remote A", "http://localhost:1", true)
		register(item.ID, &fakeInstanceInboxForwarder{})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
			"/api/v1/instances/"+item.ID+"/clarification-inbox?limit=zero", nil))
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestInstanceInboxPageRouteIssuesSingleFanOut(t *testing.T) {
	router, services, register := inboxForwarderFixture(t)
	item := createOwnedInstance(t, services, "Remote A", "http://localhost:1", true)
	fake := &fakeInstanceInboxForwarder{}
	register(item.ID, fake)

	get := func(path string) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, rec.Code)
	}

	get("/api/v1/instances/" + item.ID + "/clarification-inbox")
	inbox, hidden := fake.readCounts()
	require.Equal(t, 1, inbox)
	require.Equal(t, 0, hidden)

	get("/api/v1/instances/" + item.ID + "/clarification-inbox/hidden")
	inbox, hidden = fake.readCounts()
	require.Equal(t, 1, inbox)
	require.Equal(t, 1, hidden)
}

// needsYouQuestionRow builds one merged question row as the remote fan-out
// decodes it; instance identity is left blank for the route to stamp.
func needsYouQuestionRow() remoteinbox.NeedsYouRow {
	return remoteinbox.NeedsYouRow{
		Kind:         remoteinbox.RowKindQuestion,
		PendingID:    "p-1",
		TaskID:       "t-1",
		SessionID:    "s-1",
		SessionState: "WAITING_FOR_INPUT",
		TaskTitle:    "Fix the deploy",
		CreatedAt:    "2026-10-28T10:00:00Z",
		Messages:     []byte(`[{"id":"m-1"}]`),
		WorkspaceID:  "ws-9",
	}
}

// needsYouWaitingRow builds one merged manual-step waiting row.
func needsYouWaitingRow() remoteinbox.NeedsYouRow {
	return remoteinbox.NeedsYouRow{
		Kind:          remoteinbox.RowKindWaiting,
		ID:            "mw:t2:step-gate:7",
		TaskID:        "t-2",
		TaskTitle:     "Parked release",
		StepID:        "step-gate",
		StepName:      "Manual gate",
		ReferenceTime: "2026-10-28T09:00:00Z",
		WorkspaceID:   "ws-9",
	}
}

func TestInstanceNeedsYouPageEnvelope(t *testing.T) {
	t.Run("ok page carries kind-tagged rows stamped with instance identity", func(t *testing.T) {
		router, services, register := inboxForwarderFixture(t)
		item := createOwnedInstance(t, services, "Remote A", "http://localhost:1", true)
		register(item.ID, &fakeInstanceInboxForwarder{needsYouResult: remoteinbox.NeedsYouReadResult{
			Page: &remoteinbox.NeedsYouPage{
				Rows:  []remoteinbox.NeedsYouRow{needsYouQuestionRow(), needsYouWaitingRow()},
				Count: 2, HiddenCount: 2, HasMore: true,
			},
		}})

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instances/"+item.ID+"/needs-you-inbox", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{
			"instance_id": "`+item.ID+`",
			"instance_name": "Remote A",
			"status": "ok",
			"rows": [
				{"kind":"question","pending_id":"p-1","task_id":"t-1","session_id":"s-1","session_state":"WAITING_FOR_INPUT","task_title":"Fix the deploy","created_at":"2026-10-28T10:00:00Z","context":"","messages":[{"id":"m-1"}],"id":"","step_id":"","step_name":"","reference_time":"","instance_id":"`+item.ID+`","workspace_id":"ws-9"},
				{"kind":"waiting","pending_id":"","task_id":"t-2","session_id":"","session_state":"","task_title":"Parked release","created_at":"","context":"","messages":null,"id":"mw:t2:step-gate:7","step_id":"step-gate","step_name":"Manual gate","reference_time":"2026-10-28T09:00:00Z","instance_id":"`+item.ID+`","workspace_id":"ws-9"}
			],
			"count": 2,
			"hidden_count": 2,
			"next_snooze_expiry": null,
			"has_more": true
		}`, rec.Body.String())
	})

	t.Run("degraded page is 200 with reason and empty rows", func(t *testing.T) {
		router, services, register := inboxForwarderFixture(t)
		item := createOwnedInstance(t, services, "Remote A", "http://localhost:1", true)
		register(item.ID, &fakeInstanceInboxForwarder{needsYouResult: remoteinbox.NeedsYouReadResult{
			Degraded: remoteinbox.ReasonInboxDisabled,
		}})

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instances/"+item.ID+"/needs-you-inbox", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{
			"instance_id": "`+item.ID+`",
			"instance_name": "Remote A",
			"status": "degraded",
			"degraded_reason": "inbox_disabled",
			"rows": [],
			"count": 0,
			"hidden_count": 0,
			"next_snooze_expiry": null,
			"has_more": false
		}`, rec.Body.String())
	})

	t.Run("hidden page adds total and carries sidecar state", func(t *testing.T) {
		router, services, register := inboxForwarderFixture(t)
		item := createOwnedInstance(t, services, "Remote A", "http://localhost:1", true)
		expiry := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
		until := "2026-10-28T12:00:00Z"
		hiddenWaiting := needsYouWaitingRow()
		hiddenWaiting.State = "snoozed"
		hiddenWaiting.SnoozeUntil = &until
		register(item.ID, &fakeInstanceInboxForwarder{needsYouHidden: remoteinbox.NeedsYouReadResult{
			Page: &remoteinbox.NeedsYouPage{
				Rows: []remoteinbox.NeedsYouRow{hiddenWaiting}, Count: 1, Total: 7,
				NextSnoozeExpiry: &expiry,
			},
		}})

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instances/"+item.ID+"/needs-you-inbox/hidden", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{
			"instance_id": "`+item.ID+`",
			"instance_name": "Remote A",
			"status": "ok",
			"rows": [
				{"kind":"waiting","pending_id":"","task_id":"t-2","session_id":"","session_state":"","task_title":"Parked release","created_at":"","context":"","messages":null,"id":"mw:t2:step-gate:7","step_id":"step-gate","step_name":"Manual gate","reference_time":"2026-10-28T09:00:00Z","state":"snoozed","snooze_until":"2026-10-28T12:00:00Z","instance_id":"`+item.ID+`","workspace_id":"ws-9"}
			],
			"count": 1,
			"hidden_count": 0,
			"total": 7,
			"next_snooze_expiry": "2026-10-28T12:00:00Z",
			"has_more": false
		}`, rec.Body.String())
	})

	t.Run("invalid limit is 400", func(t *testing.T) {
		router, services, register := inboxForwarderFixture(t)
		item := createOwnedInstance(t, services, "Remote A", "http://localhost:1", true)
		register(item.ID, &fakeInstanceInboxForwarder{})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
			"/api/v1/instances/"+item.ID+"/needs-you-inbox?limit=zero", nil))
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestInstanceNeedsYouPageRouteIssuesSingleFanOut(t *testing.T) {
	router, services, register := inboxForwarderFixture(t)
	item := createOwnedInstance(t, services, "Remote A", "http://localhost:1", true)
	fake := &fakeInstanceInboxForwarder{}
	register(item.ID, fake)

	get := func(path string) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, rec.Code)
	}

	get("/api/v1/instances/" + item.ID + "/needs-you-inbox")
	page, hidden := fake.needsYouReadCounts()
	require.Equal(t, 1, page)
	require.Equal(t, 0, hidden)
	inbox, clarificationHidden := fake.readCounts()
	require.Equal(t, 0, inbox)
	require.Equal(t, 0, clarificationHidden)

	get("/api/v1/instances/" + item.ID + "/needs-you-inbox/hidden")
	page, hidden = fake.needsYouReadCounts()
	require.Equal(t, 1, page)
	require.Equal(t, 1, hidden)
	inbox, clarificationHidden = fake.readCounts()
	require.Equal(t, 0, inbox)
	require.Equal(t, 0, clarificationHidden)
}

func TestInstanceInboxRespondForwarding(t *testing.T) {
	t.Run("remote 200 body passes through verbatim", func(t *testing.T) {
		router, services, register := inboxForwarderFixture(t)
		item := createOwnedInstance(t, services, "Remote A", "http://localhost:1", true)
		fake := &fakeInstanceInboxForwarder{respondStatus: http.StatusOK, respondBody: []byte(`{"outcome":"win"}`)}
		register(item.ID, fake)

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
			"/api/v1/instances/"+item.ID+"/clarification/p-1/respond", strings.NewReader(`{"answers":[]}`)))
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{"outcome":"win"}`, rec.Body.String())
		responds, _, _, _ := fake.calls()
		require.Equal(t, []string{"p-1"}, responds)
	})

	t.Run("remote 409 passes through", func(t *testing.T) {
		router, services, register := inboxForwarderFixture(t)
		item := createOwnedInstance(t, services, "Remote A", "http://localhost:1", true)
		register(item.ID, &fakeInstanceInboxForwarder{respondStatus: http.StatusConflict, respondBody: []byte(`{"error":"not_active"}`)})

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
			"/api/v1/instances/"+item.ID+"/clarification/p-1/respond", strings.NewReader(`{}`)))
		require.Equal(t, http.StatusConflict, rec.Code)
		require.JSONEq(t, `{"error":"not_active"}`, rec.Body.String())
	})

	t.Run("transport failure is a structured 502", func(t *testing.T) {
		router, services, register := inboxForwarderFixture(t)
		item := createOwnedInstance(t, services, "Remote A", "http://localhost:1", true)
		register(item.ID, &fakeInstanceInboxForwarder{respondErr: context.DeadlineExceeded})

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
			"/api/v1/instances/"+item.ID+"/clarification/p-1/respond", strings.NewReader(`{}`)))
		require.Equal(t, http.StatusBadGateway, rec.Code)
		require.JSONEq(t, `{"error":"remote_unreachable","instance":"Remote A"}`, rec.Body.String())
	})
}

func TestInstanceInboxSidecarForwarding(t *testing.T) {
	t.Run("put and delete pass statuses and bodies through", func(t *testing.T) {
		router, services, register := inboxForwarderFixture(t)
		item := createOwnedInstance(t, services, "Remote A", "http://localhost:1", true)
		fake := &fakeInstanceInboxForwarder{upsertStatus: http.StatusNoContent, deleteStatus: http.StatusNoContent}
		register(item.ID, fake)

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut,
			"/api/v1/instances/"+item.ID+"/clarification-inbox/sidecar/p-1",
			strings.NewReader(`{"state":"snoozed","snooze_duration":"1h"}`)))
		require.Equal(t, http.StatusNoContent, rec.Code)

		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete,
			"/api/v1/instances/"+item.ID+"/clarification-inbox/sidecar/p-1", nil))
		require.Equal(t, http.StatusNoContent, rec.Code)

		_, upserts, deletes, upsertBody := fake.calls()
		require.Equal(t, []string{"p-1"}, upserts)
		require.Equal(t, []string{"p-1"}, deletes)
		require.Equal(t, "snoozed|1h", upsertBody)
	})

	t.Run("remote 404 passes through, malformed body is 400", func(t *testing.T) {
		router, services, register := inboxForwarderFixture(t)
		item := createOwnedInstance(t, services, "Remote A", "http://localhost:1", true)
		register(item.ID, &fakeInstanceInboxForwarder{upsertStatus: http.StatusNotFound, deleteStatus: http.StatusNotFound})

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut,
			"/api/v1/instances/"+item.ID+"/clarification-inbox/sidecar/p-1",
			strings.NewReader(`{"state":"dismissed"}`)))
		require.Equal(t, http.StatusNotFound, rec.Code)

		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut,
			"/api/v1/instances/"+item.ID+"/clarification-inbox/sidecar/p-1", strings.NewReader(`not-json`)))
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestInstanceInboxTwoInstancesSamePendingIDIsolated(t *testing.T) {
	router, services, register := inboxForwarderFixture(t)
	itemA := createOwnedInstance(t, services, "Remote A", "http://localhost:1", true)
	itemB := createOwnedInstance(t, services, "Remote B", "http://localhost:2", true)
	fakeA := &fakeInstanceInboxForwarder{respondStatus: http.StatusOK, respondBody: []byte(`{"outcome":"win"}`)}
	fakeB := &fakeInstanceInboxForwarder{respondStatus: http.StatusOK, respondBody: []byte(`{"outcome":"win"}`)}
	register(itemA.ID, fakeA)
	register(itemB.ID, fakeB)

	// Address B only; A's forwarder must not see the pending ID.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/v1/instances/"+itemB.ID+"/clarification/same-pending/respond", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusOK, rec.Code)

	respondsA, _, _, _ := fakeA.calls()
	respondsB, _, _, _ := fakeB.calls()
	require.Empty(t, respondsA)
	require.Equal(t, []string{"same-pending"}, respondsB)
}
