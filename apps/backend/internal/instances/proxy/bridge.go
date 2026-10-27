package proxy

import (
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

func (s *Server) bridge(run *serverRun, target *url.URL, w http.ResponseWriter, r *http.Request) {
	if !allowedOrigin(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	run.mu.Lock()
	if run.ctx.Err() != nil {
		run.mu.Unlock()
		return
	}
	run.wg.Add(1)
	run.mu.Unlock()
	defer run.wg.Done()
	header := make(http.Header)
	if err := s.upstreamHeaders(header); err != nil {
		s.remoteError(w)
		return
	}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, Subprotocols: websocket.Subprotocols(r)}
	remote, response, err := dialer.DialContext(run.ctx, wsTarget(target), header)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		s.remoteError(w)
		return
	}
	defer func() { _ = remote.Close() }()
	upgrader := websocket.Upgrader{CheckOrigin: allowedOrigin}
	if protocol := remote.Subprotocol(); protocol != "" {
		upgrader.Subprotocols = []string{protocol}
	}
	local, err := upgrader.Upgrade(w, r, w.Header())
	if err != nil {
		return
	}
	defer func() { _ = local.Close() }()
	if !run.trackConnections(local, remote) {
		return
	}
	defer func() {
		run.mu.Lock()
		delete(run.connections, local)
		delete(run.connections, remote)
		run.mu.Unlock()
	}()
	done := make(chan struct{})
	run.wg.Add(1)
	go func() {
		defer run.wg.Done()
		defer close(done)
		copyFrames(local, remote)
		_ = local.Close()
		_ = remote.Close()
	}()
	copyFrames(remote, local)
	_ = local.Close()
	_ = remote.Close()
	<-done
}

func (r *serverRun) trackConnections(local, remote *websocket.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ctx.Err() != nil {
		return false
	}
	r.connections[local] = struct{}{}
	r.connections[remote] = struct{}{}
	return true
}

func copyFrames(destination, source *websocket.Conn) {
	for {
		kind, reader, err := source.NextReader()
		if err != nil {
			if closeErr, ok := err.(*websocket.CloseError); ok {
				_ = destination.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(closeErr.Code, closeErr.Text), time.Now().Add(time.Second))
			}
			return
		}
		writer, err := destination.NextWriter(kind)
		if err != nil {
			return
		}
		_, copyErr := io.Copy(writer, reader)
		closeErr := writer.Close()
		if copyErr != nil || closeErr != nil {
			return
		}
	}
}
