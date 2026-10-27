package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBridgeCORSAndOriginPolicy(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	t.Cleanup(remote.Close)
	s := startProxy(t, remote.URL, true, "")
	for _, origin := range []string{"http://127.0.0.1:3000", "https://evil.invalid"} {
		request, err := http.NewRequest(http.MethodOptions, proxyURL(s)+"/api/v1/tasks", nil)
		require.NoError(t, err)
		request.Header.Set("Origin", origin)
		request.Header.Set("Access-Control-Request-Method", "POST")
		request.Header.Set("Access-Control-Request-Headers", "content-type")
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		if origin == "https://evil.invalid" {
			require.Equal(t, 403, response.StatusCode)
			continue
		}
		require.Equal(t, 204, response.StatusCode)
		require.Equal(t, origin, response.Header.Get("Access-Control-Allow-Origin"))
		require.Equal(t, "true", response.Header.Get("Access-Control-Allow-Credentials"))
	}
}

// A preflight naming every custom request header the SPA actually sends on
// /api/v1 calls must pass; the browser rejects the real request otherwise.
func TestBridgeCORSPreflightAllowsSpaRequestHeaders(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(204)
	}))
	t.Cleanup(remote.Close)
	s := startProxy(t, remote.URL, false, "")

	request, err := http.NewRequest(http.MethodOptions, proxyURL(s)+"/api/v1/tasks", nil)
	require.NoError(t, err)
	request.Header.Set("Origin", "http://127.0.0.1:3000")
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set(
		"Access-Control-Request-Headers",
		"content-type, x-kandev-interim-settings-interlock, x-kandev-task-delete-confirmation, if-none-match",
	)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, 204, response.StatusCode)
	require.Equal(
		t,
		"Content-Type, Authorization, X-Kandev-Interim-Settings-Interlock, X-Kandev-Task-Delete-Confirmation, If-None-Match",
		response.Header.Get("Access-Control-Allow-Headers"),
	)
}
