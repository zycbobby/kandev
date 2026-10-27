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
