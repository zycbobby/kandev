package backendapp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/auth/authn"
	userstore "github.com/kandev/kandev/internal/user/store"
	"github.com/kandev/kandev/internal/webapp"
)

func instanceRouteRequest(method, path, body string) *http.Request {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	return httptest.NewRequest(method, path, reader)
}

func TestInstanceRoutesCRUDAndHealth(t *testing.T) {
	services, cfg := newInstanceShellFixture(t)
	router := gin.New()
	registerInstanceRoutes(routeParams{router: router, services: services, features: cfg.Features})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, instanceRouteRequest(http.MethodGet, "/api/v1/instances", ""))
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, "[]", rec.Body.String())

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, instanceRouteRequest(http.MethodPost, "/api/v1/instances",
		`{"name":"GPU box","base_url":"http://localhost:1"}`))
	require.Equal(t, http.StatusCreated, rec.Code)
	var created instanceAPI
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.NotEmpty(t, created.ID)
	require.Equal(t, "GPU box", created.Name)
	require.Equal(t, "http://localhost:1", created.BaseURL)
	// Enabled defaults to true: a created instance is usable immediately.
	require.True(t, created.Enabled)
	require.NotZero(t, created.CreatedAt)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, instanceRouteRequest(http.MethodGet, "/api/v1/instances", ""))
	var listed []instanceAPI
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed))
	require.Len(t, listed, 1)
	require.Equal(t, created.ID, listed[0].ID)

	// Partial update: omitted fields keep their stored values.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, instanceRouteRequest(http.MethodPatch, "/api/v1/instances/"+created.ID,
		`{"name":"GPU box 2"}`))
	require.Equal(t, http.StatusOK, rec.Code)
	var updated instanceAPI
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
	require.Equal(t, "GPU box 2", updated.Name)
	require.Equal(t, "http://localhost:1", updated.BaseURL)
	require.True(t, updated.Enabled)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, instanceRouteRequest(http.MethodPatch, "/api/v1/instances/"+created.ID,
		`{"name":"  "}`))
	require.Equal(t, http.StatusBadRequest, rec.Code)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, instanceRouteRequest(http.MethodGet, "/api/v1/instances/missing/health", ""))
	require.Equal(t, http.StatusNotFound, rec.Code)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, instanceRouteRequest(http.MethodGet, "/api/v1/instances/"+created.ID+"/health", ""))
	require.Equal(t, http.StatusOK, rec.Code)
	var health instanceHealthAPI
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &health))
	require.False(t, health.Reachable)
	require.False(t, health.Supported)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, instanceRouteRequest(http.MethodDelete, "/api/v1/instances/"+created.ID, ""))
	require.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, instanceRouteRequest(http.MethodDelete, "/api/v1/instances/"+created.ID, ""))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestSelectSetsInstanceAndBridgeCookies pins the switch contract: select
// writes the shell selection cookie, mints the instance bridge capability
// through the live proxy server, and returns the reload target.
func TestSelectSetsInstanceAndBridgeCookies(t *testing.T) {
	services, cfg := newInstanceShellFixture(t)
	item, err := services.Instances.CreateInstance(
		t.Context(), userstore.DefaultUserID, "GPU box", "http://localhost:1", "", true)
	require.NoError(t, err)

	router := gin.New()
	registerInstanceRoutes(routeParams{router: router, services: services, features: cfg.Features})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, instanceRouteRequest(http.MethodPost, "/api/v1/instances/"+item.ID+"/select", ""))
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"href":"/"}`, rec.Body.String())

	var selection, bridge string
	for _, cookie := range rec.Result().Cookies() {
		switch cookie.Name {
		case webapp.InstanceCookieName:
			selection = cookie.Value
		case "kandev_bridge_" + item.ID:
			bridge = cookie.Value
		}
	}
	require.Equal(t, item.ID, selection)
	require.NotEmpty(t, bridge)

	// A foreign or missing instance selects nothing: 404, no cookies.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, instanceRouteRequest(http.MethodPost, "/api/v1/instances/missing/select", ""))
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Empty(t, rec.Result().Cookies())

	// Selecting a disabled instance records the selection but mints no
	// bridge capability: there is no live proxy server to mint through.
	inert, err := services.Instances.CreateInstance(
		t.Context(), userstore.DefaultUserID, "Offline box", "http://localhost:1", "", false)
	require.NoError(t, err)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, instanceRouteRequest(http.MethodPost, "/api/v1/instances/"+inert.ID+"/select", ""))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, rec.Result().Cookies(), 1)
	require.Equal(t, webapp.InstanceCookieName, rec.Result().Cookies()[0].Name)
	require.Equal(t, inert.ID, rec.Result().Cookies()[0].Value)
}

// TestSelectLocalClearsSelection pins the switch-back contract: the local
// sentinel id is not a registry row, so selecting it deletes the selection
// cookie (same attributes, Max-Age=0) and mints no bridge capability, and a
// query-less reload boots the local instance.
func TestSelectLocalClearsSelection(t *testing.T) {
	services, cfg := newInstanceShellFixture(t)
	item, err := services.Instances.CreateInstance(
		t.Context(), userstore.DefaultUserID, "GPU box", "http://localhost:1", "", true)
	require.NoError(t, err)

	router := gin.New()
	registerInstanceRoutes(routeParams{router: router, services: services, features: cfg.Features})

	// Seed a remote selection, then switch back to local.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, instanceRouteRequest(http.MethodPost, "/api/v1/instances/"+item.ID+"/select", ""))
	require.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, instanceRouteRequest(http.MethodPost, "/api/v1/instances/local/select", ""))
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"href":"/"}`, rec.Body.String())

	headers := rec.Header().Values("Set-Cookie")
	require.Len(t, headers, 1)
	require.Contains(t, headers[0], webapp.InstanceCookieName+"=")
	require.Contains(t, headers[0], "Max-Age=0")
	require.Contains(t, headers[0], "Path=/")
	require.Contains(t, headers[0], "HttpOnly")

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	require.Equal(t, webapp.InstanceCookieName, cookies[0].Name)
	require.Empty(t, cookies[0].Value)
	require.Negative(t, cookies[0].MaxAge)
	for _, cookie := range cookies {
		require.NotContains(t, cookie.Name, "kandev_bridge_")
	}
}

func TestInstanceRoutesFlagOffGone(t *testing.T) {
	router := gin.New()
	registerInstanceRoutes(routeParams{router: router, services: &Services{}})
	paths := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/instances"},
		{http.MethodPost, "/api/v1/instances"},
		{http.MethodPatch, "/api/v1/instances/x"},
		{http.MethodDelete, "/api/v1/instances/x"},
		{http.MethodGet, "/api/v1/instances/x/health"},
		{http.MethodPost, "/api/v1/instances/x/select"},
	}
	for _, route := range paths {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, instanceRouteRequest(route.method, route.path, ""))
		require.Equal(t, http.StatusNotFound, rec.Code, route.path)
	}
}

func TestInstanceRoutesOwnerScoped(t *testing.T) {
	services, cfg := newInstanceShellFixture(t)
	item, err := services.Instances.CreateInstance(
		t.Context(), "owner-a", "A box", "http://localhost:1", "", false)
	require.NoError(t, err)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		authn.SetOnGin(c, authn.Identity{UserID: "owner-b", Role: authn.RoleMember})
	})
	registerInstanceRoutes(routeParams{router: router, services: services, features: cfg.Features})

	// owner-b's list shows only owner-b's instances.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, instanceRouteRequest(http.MethodGet, "/api/v1/instances", ""))
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, "[]", rec.Body.String())

	// owner-a's instance is invisible to owner-b on every entry point.
	foreign := []struct{ method, path, body string }{
		{http.MethodPatch, "/api/v1/instances/" + item.ID, `{"name":"stolen"}`},
		{http.MethodDelete, "/api/v1/instances/" + item.ID, ""},
		{http.MethodGet, "/api/v1/instances/" + item.ID + "/health", ""},
		{http.MethodPost, "/api/v1/instances/" + item.ID + "/select", ""},
	}
	for _, route := range foreign {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, instanceRouteRequest(route.method, route.path, route.body))
		require.Equal(t, http.StatusNotFound, rec.Code, route.path)
	}
}
