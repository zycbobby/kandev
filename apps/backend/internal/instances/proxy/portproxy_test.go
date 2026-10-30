package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestProxyBridgesPortProxyHttp(t *testing.T) {
	for _, pat := range []string{"remote-pat", ""} {
		t.Run(pat, func(t *testing.T) {
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				expected := ""
				if pat != "" {
					expected = "Bearer " + pat
				}
				if r.Header.Get("Authorization") != expected || r.URL.RequestURI() != "/port-proxy/s1/49152/index.html?kandev_cap=x" {
					w.WriteHeader(400)
					return
				}
				if strings.Contains(r.Header.Get("Cookie"), "kandev_bridge_") || r.Header.Get("Origin") != "" {
					w.WriteHeader(400)
					return
				}
				w.WriteHeader(200)
				_, _ = w.Write([]byte("remote-preview"))
			}))
			t.Cleanup(remote.Close)
			s := startProxy(t, remote.URL, false, pat)
			req, err := http.NewRequest(http.MethodGet, proxyURL(s)+"/port-proxy/s1/49152/index.html?kandev_cap=x", nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer browser-credential")
			req.Header.Set("Cookie", "kandev_bridge_remote-one=private; app=value")
			req.Header.Set("Origin", "http://localhost:3000")
			res, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer func() { _ = res.Body.Close() }()
			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.Equal(t, 200, res.StatusCode)
			require.Equal(t, "remote-preview", string(body))
		})
	}
}

func TestPortProxyUnknownPathStill404(t *testing.T) {
	var calls atomic.Int64
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(204)
	}))
	t.Cleanup(remote.Close)
	s := startProxy(t, remote.URL, false, "")
	for _, path := range []string{"/nope", "/port-proxyx"} {
		res, err := http.Get(proxyURL(s) + path)
		require.NoError(t, err)
		_ = res.Body.Close()
		require.Equal(t, 404, res.StatusCode)
	}
	require.Zero(t, calls.Load())
}

func TestPortProxyRequiresBridgeCapability(t *testing.T) {
	var calls atomic.Int64
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(204)
	}))
	t.Cleanup(remote.Close)
	s := startProxy(t, remote.URL, true, "")
	res, err := http.Get(proxyURL(s) + "/port-proxy/s1/49152/index.html")
	require.NoError(t, err)
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	require.NoError(t, err)
	require.Equal(t, 401, res.StatusCode)
	require.JSONEq(t, `{"error":"bridge_capability_required"}`, string(body))
	require.Zero(t, calls.Load())
}

func TestPortProxyWsBridgePreservesPathAndQuery(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/port-proxy/s1/49152/" || r.URL.RawQuery != "hmr=1" || r.Header.Get("Authorization") != "Bearer remote-pat" {
			w.WriteHeader(401)
			return
		}
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		for {
			kind, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			if c.WriteMessage(kind, msg) != nil {
				return
			}
		}
	}))
	t.Cleanup(remote.Close)
	s := startProxy(t, remote.URL, false, "remote-pat")
	c, res, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(proxyURL(s), "http")+"/port-proxy/s1/49152/?hmr=1", nil)
	if res != nil {
		defer func() { _ = res.Body.Close() }()
	}
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	require.NoError(t, c.SetReadDeadline(time.Now().Add(5*time.Second)))
	require.NoError(t, c.WriteMessage(websocket.TextMessage, []byte("round-trip")))
	got, msg, err := c.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, got)
	require.Equal(t, "round-trip", string(msg))
}
