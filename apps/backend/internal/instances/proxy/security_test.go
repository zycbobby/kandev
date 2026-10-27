package proxy

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/kandev/kandev/internal/instances"
	"github.com/stretchr/testify/require"
)

func TestAuthenticatedWebSocketAndUntrustedOrigin(t *testing.T) {
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
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
	s := startProxy(t, remote.URL, true, "")
	mint := httptest.NewRecorder()
	s.SetCapabilityCookie(mint, httptest.NewRequest(http.MethodPost, "http://localhost/select", nil))
	cookie := mint.Result().Cookies()[0]
	address := "ws" + strings.TrimPrefix(proxyURL(s), "http") + "/ws"
	for _, origin := range []string{"https://evil.invalid", "http://127.0.0.1:3000"} {
		header := http.Header{"Cookie": []string{cookie.String()}, "Origin": []string{origin}}
		c, response, err := websocket.DefaultDialer.Dial(address, header)
		if response != nil {
			require.NoError(t, response.Body.Close())
		}
		if origin == "https://evil.invalid" {
			require.Error(t, err)
			require.Equal(t, 403, response.StatusCode)
			require.Zero(t, calls.Load())
			continue
		}
		require.NoError(t, err)
		require.NoError(t, c.WriteMessage(websocket.TextMessage, []byte("authorized")))
		_, body, err := c.ReadMessage()
		require.NoError(t, err)
		require.Equal(t, "authorized", string(body))
		require.NoError(t, c.Close())
		require.EqualValues(t, 1, calls.Load())
	}
}

func TestCredentialFailureDoesNotForwardOrExposeSecret(t *testing.T) {
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	t.Cleanup(remote.Close)
	s := NewServer(instances.Instance{ID: "one", Name: "One", BaseURL: remote.URL}, func() (string, error) { return "", errors.New("secret-sensitive-error") }, false, testLog(t))
	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(s.Stop)
	for _, path := range []string{"/api/v1/tasks", "/ws"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		// Exercise the listener, not a stand-in handler.
		response, err := http.Get(proxyURL(s) + request.URL.Path)
		require.NoError(t, err)
		_, err = io.Copy(recorder, response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		require.Equal(t, 502, response.StatusCode)
		require.NotContains(t, recorder.Body.String(), "secret-sensitive-error")
	}
	require.Zero(t, calls.Load())
}
