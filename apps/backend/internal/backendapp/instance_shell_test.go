package backendapp

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/kandev/kandev/internal/auth/authn"
	authhttpmw "github.com/kandev/kandev/internal/auth/httpmw"
	"github.com/kandev/kandev/internal/common/config"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/instances"
	instancestore "github.com/kandev/kandev/internal/instances/store/sqlite"
	userstore "github.com/kandev/kandev/internal/user/store"
	"github.com/kandev/kandev/internal/webapp"
)

// newInstanceShellFixture builds the flag-gated instance assembly (service +
// proxy manager) over a fresh SQLite registry, mirroring the production
// initInstanceProxies wiring. The proxy stop is registered on t.Cleanup.
func newInstanceShellFixture(t *testing.T) (*Services, *config.Config) {
	t.Helper()
	raw, err := db.OpenSQLite(filepath.Join(t.TempDir(), "instances.db"))
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
	stop := initInstanceProxies(t.Context(), cfg, &Repositories{Instances: repo}, services, log)
	t.Cleanup(stop)
	return services, cfg
}

func instanceShellRequest(t *testing.T, target, instanceID string, identity *authn.Identity) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if instanceID != "" {
		req.AddCookie(&http.Cookie{Name: webapp.InstanceCookieName, Value: instanceID})
	}
	if identity != nil {
		req = req.WithContext(authn.WithIdentity(req.Context(), *identity))
	}
	return req
}

// TestShellInstanceParamIgnoredWhenFlagOff pins the default-off scenario: with
// features.multiInstance off, instance cookies and query parameters produce
// exactly the payload a plain request produces.
func TestShellInstanceParamIgnoredWhenFlagOff(t *testing.T) {
	p := routeParams{services: &Services{}}
	route := webapp.ClassifyRoute("/")

	plain := httptest.NewRequest(http.MethodGet, "/", nil)
	selected := httptest.NewRequest(http.MethodGet, "/?kandev_instance=inst-1", nil)
	selected.AddCookie(&http.Cookie{Name: webapp.InstanceCookieName, Value: "inst-1"})

	want := shellPayloadFor(plain.Context(), plain, p, route)
	got := shellPayloadFor(selected.Context(), selected, p, route)
	require.Equal(t, want, got)
	require.False(t, got.OmitBootData)
	require.Zero(t, got.APIPort)
}

func TestRemoteInstanceShellPayloadResolution(t *testing.T) {
	services, cfg := newInstanceShellFixture(t)
	item, err := services.Instances.CreateInstance(
		t.Context(), userstore.DefaultUserID, "GPU box", "http://localhost:1", "", true)
	require.NoError(t, err)
	p := routeParams{services: services, features: cfg.Features}
	route := webapp.ClassifyRoute("/t/task-9")

	synthetic := authhttpmw.SyntheticIdentity()
	req := instanceShellRequest(t, "/t/task-9", item.ID, &synthetic)
	payload, ok := remoteInstanceShellPayload(req.Context(), req, p, route)
	require.True(t, ok)
	require.True(t, payload.OmitBootData)
	require.Equal(t, services.InstanceProxies.Server(item.ID).Port(), payload.APIPort)
	require.Equal(t, item.ID, payload.InstanceID)
	require.Equal(t, "GPU box", payload.Runtime.TitlePrefix)

	// Query parameter selection overrides the cookie and rewrites the ID.
	other, err := services.Instances.CreateInstance(
		t.Context(), userstore.DefaultUserID, "Second box", "http://localhost:1", "", true)
	require.NoError(t, err)
	req = instanceShellRequest(t, "/?kandev_instance="+other.ID, item.ID, &synthetic)
	payload, ok = remoteInstanceShellPayload(req.Context(), req, p, route)
	require.True(t, ok)
	require.Equal(t, other.ID, payload.InstanceID)
	require.Equal(t, "Second box", payload.Runtime.TitlePrefix)

	// A foreign owner sees the instance exactly like a missing one: local boot.
	member := authn.Identity{UserID: "owner-b", Role: authn.RoleMember}
	req = instanceShellRequest(t, "/t/task-9", item.ID, &member)
	_, ok = remoteInstanceShellPayload(req.Context(), req, p, route)
	require.False(t, ok)

	// Unauthenticated shell visitors (auth on, SPA path deferred) never boot
	// a remote instance: no identity means local boot, not the synthetic owner.
	req = instanceShellRequest(t, "/t/task-9", item.ID, nil)
	_, ok = remoteInstanceShellPayload(req.Context(), req, p, route)
	require.False(t, ok)

	// No live bridge (instance disabled): local boot.
	disabled := false
	_, err = services.Instances.UpdateInstance(
		t.Context(), userstore.DefaultUserID, item.ID, instances.InstanceUpdate{Enabled: &disabled})
	require.NoError(t, err)
	req = instanceShellRequest(t, "/t/task-9", item.ID, &synthetic)
	_, ok = remoteInstanceShellPayload(req.Context(), req, p, route)
	require.False(t, ok)

	// No selection at all: local boot.
	req = instanceShellRequest(t, "/t/task-9", "", &synthetic)
	_, ok = remoteInstanceShellPayload(req.Context(), req, p, route)
	require.False(t, ok)
}

func TestRemoteInstanceShellHTML(t *testing.T) {
	services, cfg := newInstanceShellFixture(t)
	item, err := services.Instances.CreateInstance(
		t.Context(), userstore.DefaultUserID, "GPU box", "http://localhost:1", "", true)
	require.NoError(t, err)

	assets := fstest.MapFS{
		"index.html": {
			Data: []byte(`<!doctype html><html lang="en"><head><title>Kandev</title></head><body></body></html>`),
		},
	}
	p := routeParams{services: services, features: cfg.Features}
	handler := webapp.NewHandler(assets, webAppHandlerOptions(p)...)

	synthetic := authhttpmw.SyntheticIdentity()
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, instanceShellRequest(t, "/", item.ID, &synthetic))
	got := resp.Body.String()
	require.Equal(t, http.StatusOK, resp.Code)
	require.Contains(t, got, "window.__KANDEV_API_PORT=")
	require.Contains(t, got, `window.__KANDEV_INSTANCE_ID="`+item.ID+`"`)
	require.NotContains(t, got, "__KANDEV_BOOT_PAYLOAD__")
	require.Contains(t, got, "<title>GPU box Kandev</title>")

	resp = httptest.NewRecorder()
	handler.ServeHTTP(resp, instanceShellRequest(t, "/", "", &synthetic))
	require.Contains(t, resp.Body.String(), "__KANDEV_BOOT_PAYLOAD__")
	require.NotContains(t, resp.Body.String(), "window.__KANDEV_API_PORT=")
	require.NotContains(t, resp.Body.String(), "window.__KANDEV_INSTANCE_ID=")
}

// TestStartInstanceHealthPollerRecordsHealth drives the wiring helper end to
// end: a registered instance is probed by the immediate first pass and the
// report becomes owner-visible through the service.
func TestStartInstanceHealthPollerRecordsHealth(t *testing.T) {
	services, _ := newInstanceShellFixture(t)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/system/info" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"v` + instances.MinRemoteVersion + `"}`))
	}))
	t.Cleanup(remote.Close)
	item, err := services.Instances.CreateInstance(
		t.Context(), userstore.DefaultUserID, "GPU box", remote.URL, "", true)
	require.NoError(t, err)

	log, err := logger.NewFromZap(zap.NewNop())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	startInstanceHealthPoller(ctx, services, log, func(fn func() error) {
		t.Cleanup(func() { require.NoError(t, fn()) })
	})

	require.Eventually(t, func() bool {
		report, err := services.Instances.GetHealth(ctx, userstore.DefaultUserID, item.ID)
		return err == nil && report.Reachable && report.Supported
	}, 5*time.Second, 10*time.Millisecond)

	// Flag off (no instance service): the wiring helper is a no-op and
	// registers no cleanup.
	startInstanceHealthPoller(ctx, &Services{}, log, func(func() error) {
		t.Fatal("cleanup registered for an inert instance registry")
	})
}

// TestStartServicesUsesInstanceHealthPollerWiringHelper mirrors
// TestStartAgentInfrastructureUsesSSHReachabilityPollerWiringHelper: the
// composition root must delegate to the narrow wiring helper exactly once
// rather than inlining the poller construction.
func TestStartServicesUsesInstanceHealthPollerWiringHelper(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	require.NoError(t, err)

	var target *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "startServices" {
			target = fn
			break
		}
	}
	if target == nil {
		t.Fatal("startServices not found in main.go")
	}

	var helperCalls int
	ast.Inspect(target, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name, ok := call.Fun.(*ast.Ident); ok && name.Name == "startInstanceHealthPoller" {
			helperCalls++
		}
		return true
	})
	if helperCalls != 1 {
		t.Fatalf("startInstanceHealthPoller calls = %d, want exactly one composition call", helperCalls)
	}
}
