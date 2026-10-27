// Package relay consumes dispatched remote notification occurrences.
package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/instances"
	ws "github.com/kandev/kandev/pkg/websocket"
	"go.uber.org/zap"
)

// Occurrence carries both the remote identity and the locally persisted state.
type Occurrence struct {
	ID           string    `json:"id"`
	InstanceID   string    `json:"instance_id"`
	Event        string    `json:"event"`
	OccurrenceID string    `json:"occurrence_id"`
	TaskID       string    `json:"task_id"`
	SessionID    string    `json:"session_id"`
	Title        string    `json:"title"`
	Body         string    `json:"body"`
	CreatedAt    time.Time `json:"created_at"`
	Read         bool      `json:"read"`
	Dismissed    bool      `json:"dismissed"`
}

// OccurrenceID is shared by persisted rows and live payloads so read/dismiss
// actions address the same local notification without an additional lookup.
func OccurrenceID(instanceID, event, occurrenceID string) string {
	key, _ := json.Marshal([]string{instanceID, event, occurrenceID})
	return uuid.NewSHA1(uuid.NameSpaceOID, key).String()
}

type Store interface {
	Record(context.Context, string, string, string, string, string, string, string, string, time.Time) (bool, error)
	LatestOccurrenceTime(context.Context, string) (time.Time, error)
}
type Broadcaster interface {
	BroadcastToUser(string, *ws.Message) bool
}

type Relay struct {
	inst        instances.Instance
	credential  string
	store       Store
	hub         Broadcaster
	log         *logger.Logger
	client      *http.Client
	lifecycleMu sync.Mutex
	mu          sync.Mutex
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	conn        *websocket.Conn
	cursor      time.Time
	cursorReady bool
}

func NewRelay(inst instances.Instance, credential string, store Store, hub Broadcaster, log *logger.Logger) *Relay {
	return &Relay{inst: inst, credential: credential, store: store, hub: hub, log: log, client: &http.Client{Timeout: 20 * time.Second}}
}
func (r *Relay) Start(ctx context.Context) {
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return
	}
	ctx, r.cancel = context.WithCancel(ctx)
	r.cursorReady = false
	r.wg.Add(2)
	go func() { defer r.wg.Done(); r.liveLoop(ctx) }()
	go func() { defer r.wg.Done(); r.reconcileLoop(ctx) }()
}
func (r *Relay) Stop() {
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel == nil {
		return
	}
	r.cancel()
	if r.conn != nil {
		_ = r.conn.Close()
	}
	r.mu.Unlock()
	r.wg.Wait()
	r.mu.Lock()
	r.cancel = nil
}
func wait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func (r *Relay) liveLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if r.connect(ctx) {
			backoff = time.Second
		}
		if !wait(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}
func (r *Relay) connect(ctx context.Context) bool {
	u, err := url.Parse(r.inst.BaseURL)
	if err != nil {
		return false
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = "/ws"
	headers := http.Header{}
	r.authorize(headers)
	conn, response, err := websocket.DefaultDialer.DialContext(ctx, u.String(), headers)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	r.mu.Lock()
	if ctx.Err() != nil {
		r.mu.Unlock()
		return false
	}
	r.conn = conn
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.conn = nil; r.mu.Unlock() }()
	subscription, _ := ws.NewRequest("relay-user", ws.ActionUserSubscribe, map[string]string{})
	if conn.WriteJSON(subscription) != nil {
		return false
	}
	for ctx.Err() == nil {
		var msg ws.Message
		if conn.ReadJSON(&msg) != nil {
			return true
		}
		if msg.Type != ws.MessageTypeNotification {
			continue
		}
		var o Occurrence
		if json.Unmarshal(msg.Payload, &o) != nil {
			continue
		}
		o.Event = msg.Action
		if o.CreatedAt.IsZero() {
			o.CreatedAt = msg.Timestamp
		}
		if err := r.record(ctx, o); err != nil {
			r.log.Warn("remote notification record failed", zap.String("instance", r.inst.Name), zap.Error(err))
		}
	}
	return true
}
func (r *Relay) authorize(header http.Header) {
	if r.credential != "" {
		header.Set("Authorization", "Bearer "+r.credential)
	}
}
func allowed(event string) bool {
	return event == "session.turn_finished" || event == "session.clarification_requested" || event == "office.inbox_item"
}
func (r *Relay) record(ctx context.Context, o Occurrence) error {
	if !allowed(o.Event) {
		return nil
	}
	if o.OccurrenceID == "" {
		r.log.Debug("remote notification has no occurrence ID", zap.String("instance", r.inst.Name))
		return nil
	}
	inserted, err := r.store.Record(ctx, r.inst.OwnerUserID, r.inst.ID, o.Event, o.OccurrenceID, o.TaskID, o.SessionID, o.Title, o.Body, o.CreatedAt)
	if err != nil || !inserted {
		return err
	}
	o.InstanceID = r.inst.ID
	o.ID = OccurrenceID(r.inst.ID, o.Event, o.OccurrenceID)
	msg, err := ws.NewNotification(ws.ActionRemoteNotification, o)
	if err != nil {
		return err
	}
	r.hub.BroadcastToUser(r.inst.OwnerUserID, msg)
	return nil
}
func (r *Relay) reconcileLoop(ctx context.Context) {
	for ctx.Err() == nil {
		if err := r.reconcile(ctx); err != nil {
			r.log.Warn("remote notification reconciliation failed", zap.String("instance", r.inst.Name), zap.Error(err))
		}
		if !wait(ctx, 60*time.Second) {
			return
		}
	}
}
func (r *Relay) reconcile(ctx context.Context) error {
	if !r.cursorReady {
		cursor, err := r.store.LatestOccurrenceTime(ctx, r.inst.ID)
		if err != nil {
			return err
		}
		r.cursor = cursor
		r.cursorReady = true
	}
	for page := 0; page < 10; page++ {
		rows, err := r.fetch(ctx)
		if err != nil {
			return err
		}
		next := r.cursor
		for _, o := range rows {
			if err := r.record(ctx, o); err != nil {
				return err
			}
			if o.CreatedAt.After(next) {
				next = o.CreatedAt
			}
		}
		if len(rows) == 200 && !next.After(r.cursor) {
			r.log.Warn("earlier events not synced", zap.String("instance", r.inst.Name), zap.Time("boundary", r.cursor))
			r.cursor = r.cursor.Add(time.Second)
			return nil
		}
		r.cursor = next
		if len(rows) < 200 {
			return nil
		}
	}
	r.log.Warn("earlier events not synced", zap.String("instance", r.inst.Name))
	return nil
}
func (r *Relay) fetch(ctx context.Context) ([]Occurrence, error) {
	u, err := url.Parse(r.inst.BaseURL)
	if err != nil {
		return nil, err
	}
	u.Path = "/api/v1/notifications/occurrences"
	q := u.Query()
	q.Set("since", r.cursor.UTC().Format(time.RFC3339))
	q.Set("limit", "200")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	r.authorize(req.Header)
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remote occurrences returned status %d", resp.StatusCode)
	}
	var rows []Occurrence
	err = json.NewDecoder(resp.Body).Decode(&rows)
	return rows, err
}
