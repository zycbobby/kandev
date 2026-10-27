// Package proxy provides instance-scoped HTTP and WebSocket bridges.
package proxy

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kandev/kandev/internal/common/httpmw"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/instances"
)

// Server owns a local listener and the connections bridged through it.
type Server struct {
	instance   instances.Instance
	credential func() (string, error)
	authOn     bool
	secret     [32]byte
	mu         sync.Mutex
	run        *serverRun
	port       int
}

type serverRun struct {
	ctx         context.Context
	cancel      context.CancelFunc
	http        *http.Server
	transport   *http.Transport
	wg          sync.WaitGroup
	mu          sync.Mutex
	connections map[*websocket.Conn]struct{}
}

// NewServer generates a process-local capability key. Restart invalidates capabilities.
func NewServer(inst instances.Instance, credential func() (string, error), authOn bool, _ *logger.Logger) *Server {
	s := &Server{instance: inst, credential: credential, authOn: authOn, port: inst.ProxyPort}
	if _, err := rand.Read(s.secret[:]); err != nil {
		panic(fmt.Sprintf("instance proxy: generate capability secret: %v", err))
	}
	return s
}

// Start prefers the persisted port and falls back to a dynamically assigned port.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run != nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := url.Parse(s.instance.BaseURL)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") || target.User != nil {
		return fmt.Errorf("invalid instance base URL")
	}
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(s.port))
	if err != nil && s.port != 0 {
		listener, err = net.Listen("tcp", ":0")
	}
	if err != nil {
		return fmt.Errorf("listen on instance proxy port: %w", err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	run := &serverRun{ctx: runCtx, cancel: cancel, connections: make(map[*websocket.Conn]struct{}), transport: http.DefaultTransport.(*http.Transport).Clone()}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.Handle("/api/v1/", s.requireBridgeAuth(s.httpProxy(target, run.transport)))
	mux.Handle("/ws", s.requireBridgeAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.bridge(run, target, w, r) })))
	run.http = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return runCtx }}
	s.port = listener.Addr().(*net.TCPAddr).Port
	s.run = run
	run.wg.Add(2)
	go func() { defer run.wg.Done(); _ = run.http.Serve(listener) }()
	go func() { defer run.wg.Done(); <-runCtx.Done(); run.close() }()
	return nil
}

func (r *serverRun) close() {
	// Close admission before closing hijacked connections; no handler may Add after shutdown.
	r.mu.Lock()
	for conn := range r.connections {
		_ = conn.Close()
	}
	r.mu.Unlock()
	_ = r.http.Close()
	r.transport.CloseIdleConnections()
}

// Stop closes HTTP and hijacked WebSocket connections and joins all owned goroutines.
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run == nil {
		return
	}
	s.run.cancel()
	s.run.wg.Wait()
	s.run = nil
}

// Port returns the currently allocated local port.
func (s *Server) Port() int { s.mu.Lock(); defer s.mu.Unlock(); return s.port }

func (s *Server) upstreamHeaders(header http.Header) error {
	header.Del("Authorization")
	header.Del("Origin")
	header.Del("Referer")
	// Browser-local credentials must never be disclosed to a remote instance.
	header.Del("Cookie")
	if s.credential == nil {
		return nil
	}
	credential, err := s.credential()
	if err != nil {
		return err
	}
	if credential != "" {
		header.Set("Authorization", "Bearer "+credential)
	}
	return nil
}

func (s *Server) httpProxy(target *url.URL, transport http.RoundTripper) http.Handler {
	proxy := &httputil.ReverseProxy{
		Transport:      transport,
		Rewrite:        func(r *httputil.ProxyRequest) { r.SetURL(target); r.Out.Host = target.Host },
		ErrorHandler:   func(w http.ResponseWriter, _ *http.Request, _ error) { s.remoteError(w) },
		ModifyResponse: func(r *http.Response) error { r.Header.Del("Set-Cookie"); return nil },
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Resolve credentials before ReverseProxy so errors cannot forward unauthenticated requests.
		outbound := r.Clone(r.Context())
		outbound.Header = r.Header.Clone()
		if err := s.upstreamHeaders(outbound.Header); err != nil {
			s.remoteError(w)
			return
		}
		proxy.ServeHTTP(w, outbound)
	})
}

func (s *Server) remoteError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "remote_unreachable", "instance": s.instance.Name})
}

func allowedOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || httpmw.AllowedOrigin(origin, r.Host)
}

func wsTarget(target *url.URL) string {
	copyURL := *target
	copyURL.Scheme = "ws"
	if target.Scheme == "https" {
		copyURL.Scheme = "wss"
	}
	copyURL.Path = strings.TrimRight(target.Path, "/") + "/ws"
	copyURL.RawPath = ""
	copyURL.RawQuery = ""
	return copyURL.String()
}
