package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBridgeRequiresCapabilityWhenAuthOn(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	t.Cleanup(remote.Close)
	s := startProxy(t, remote.URL, true, "")
	for _, path := range []string{"/api/v1/tasks", "/ws"} {
		res, err := http.Get(proxyURL(s) + path)
		require.NoError(t, err)
		_ = res.Body.Close()
		require.Equal(t, 401, res.StatusCode)
	}
	res, err := http.Get(proxyURL(s) + "/healthz")
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, 200, res.StatusCode)
}

func TestCapabilityMintVerifyRoundTrip(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	t.Cleanup(remote.Close)
	s := startProxy(t, remote.URL, true, "")
	recorder := httptest.NewRecorder()
	s.SetCapabilityCookie(recorder, httptest.NewRequest(http.MethodPost, "http://localhost/select", nil))
	cookies := recorder.Result().Cookies()
	require.Len(t, cookies, 1)
	cookie := cookies[0]
	require.Equal(t, "kandev_bridge_remote-one", cookie.Name)
	require.Equal(t, "/", cookie.Path)
	require.True(t, cookie.HttpOnly)
	require.Equal(t, 900, cookie.MaxAge)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	require.True(t, s.validCapability(cookie.Value))
	require.False(t, s.validCapability(cookie.Value+"tampered"))
	other := startProxy(t, remote.URL, true, "")
	require.False(t, other.validCapability(cookie.Value))
	for _, raw := range []string{s.mintCapability("other", time.Now().Add(time.Minute)), s.mintCapability("remote-one", time.Now())} {
		require.False(t, s.validCapability(raw))
	}
	request, err := http.NewRequest(http.MethodGet, proxyURL(s)+"/api/v1/tasks", nil)
	require.NoError(t, err)
	request.AddCookie(cookie)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	require.Equal(t, 204, response.StatusCode)
	require.NotEmpty(t, response.Cookies())
}
