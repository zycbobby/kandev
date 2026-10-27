package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/instances"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"go.uber.org/zap"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func testLog(t *testing.T) *logger.Logger {
	t.Helper()
	log, err := logger.NewFromZap(zap.NewNop())
	require.NoError(t, err)
	return log
}

func startProxy(t *testing.T, remote string, auth bool, credential string) *Server {
	t.Helper()
	s := NewServer(instances.Instance{ID: "remote-one", Name: "Remote One", BaseURL: remote}, func() (string, error) { return credential, nil }, auth, testLog(t))
	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(s.Stop)
	return s
}

func proxyURL(s *Server) string { return "http://127.0.0.1:" + strconv.Itoa(s.Port()) }

func TestProxyForwardsApiAndInjectsCredential(t *testing.T) {
	for _, pat := range []string{"remote-pat", ""} {
		t.Run(pat, func(t *testing.T) {
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				expected := ""
				if pat != "" {
					expected = "Bearer " + pat
				}
				if r.Header.Get("Authorization") != expected || r.URL.RequestURI() != "/api/v1/tasks?q=one" {
					w.WriteHeader(400)
					return
				}
				if strings.Contains(r.Header.Get("Cookie"), "kandev_bridge_") || r.Header.Get("Origin") != "" {
					w.WriteHeader(400)
					return
				}
				w.WriteHeader(201)
				_, _ = w.Write([]byte("remote-body"))
			}))
			t.Cleanup(remote.Close)
			s := startProxy(t, remote.URL, false, pat)
			req, err := http.NewRequest(http.MethodGet, proxyURL(s)+"/api/v1/tasks?q=one", nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer browser-credential")
			req.Header.Set("Cookie", "kandev_bridge_remote-one=private; app=value")
			req.Header.Set("Origin", "http://localhost:3000")
			res, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer func() { _ = res.Body.Close() }()
			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.Equal(t, 201, res.StatusCode)
			require.Equal(t, "remote-body", string(body))
		})
	}
}

func TestProxyStripsUpstreamCorsHeaders(t *testing.T) {
	// A real remote never sees the browser's Origin (the proxy strips it), so
	// its global CORS middleware answers with the no-Origin shape, including
	// Access-Control-Allow-Origin: *. Forwarding that alongside the bridge's
	// own CORS headers duplicates the header and the browser rejects every
	// cross-origin response.
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Expose-Headers", "WWW-Authenticate")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("remote-body"))
	}))
	t.Cleanup(remote.Close)
	s := startProxy(t, remote.URL, false, "")
	req, err := http.NewRequest(http.MethodGet, proxyURL(s)+"/api/v1/workspaces", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "http://localhost:3000")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	require.Equal(t, 200, res.StatusCode)
	require.Equal(t, []string{"http://localhost:3000"}, res.Header.Values("Access-Control-Allow-Origin"))
	require.Equal(t, []string{"true"}, res.Header.Values("Access-Control-Allow-Credentials"))
	require.Empty(t, res.Header.Values("Access-Control-Expose-Headers"))
}

func TestWsBridgeBidirectionalFrames(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws" || r.Header.Get("Authorization") != "Bearer remote-pat" {
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
	c, res, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(proxyURL(s), "http")+"/ws", nil)
	if res != nil {
		defer func() { _ = res.Body.Close() }()
	}
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	require.NoError(t, c.SetReadDeadline(time.Now().Add(5*time.Second)))
	for _, kind := range []int{websocket.TextMessage, websocket.BinaryMessage} {
		require.NoError(t, c.WriteMessage(kind, []byte("round-trip")))
		got, msg, err := c.ReadMessage()
		require.NoError(t, err)
		require.Equal(t, kind, got)
		require.Equal(t, "round-trip", string(msg))
	}
	s.Stop()
	_, _, err = c.ReadMessage()
	require.Error(t, err)
}

func TestRemoteUnreachableReturnsStructuredError(t *testing.T) {
	remote := httptest.NewServer(http.NotFoundHandler())
	address := remote.URL
	remote.Close()
	s := startProxy(t, address, false, "")
	for _, path := range []string{"/api/v1/tasks", "/ws"} {
		res, err := http.Get(proxyURL(s) + path)
		require.NoError(t, err)
		var body map[string]string
		err = json.NewDecoder(res.Body).Decode(&body)
		_ = res.Body.Close()
		require.NoError(t, err)
		require.Equal(t, 502, res.StatusCode)
		require.Equal(t, "remote_unreachable", body["error"])
		require.Equal(t, "Remote One", body["instance"])
	}
}

func TestProxyStartStopNoGoroutineLeak(t *testing.T) {
	remote := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(remote.Close)
	s := startProxy(t, remote.URL, false, "")
	for range 3 {
		s.Stop()
		s.Stop()
		require.NoError(t, s.Start(context.Background()))
		require.NoError(t, s.Start(context.Background()))
	}
	s.Stop()
}

func TestProxyPrefersPersistedPortAndReallocatesWhenOccupied(t *testing.T) {
	listener, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	s := NewServer(instances.Instance{ID: "one", BaseURL: "http://localhost", ProxyPort: port}, nil, false, testLog(t))
	t.Cleanup(s.Stop)
	require.NoError(t, s.Start(t.Context()))
	require.Equal(t, port, s.Port())
	other := NewServer(instances.Instance{ID: "two", BaseURL: "http://localhost", ProxyPort: port}, nil, false, testLog(t))
	t.Cleanup(other.Stop)
	require.NoError(t, other.Start(t.Context()))
	require.NotEqual(t, port, other.Port())
}
