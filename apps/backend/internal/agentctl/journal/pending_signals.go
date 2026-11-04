package journal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	// MaxPendingSignals bounds retained captured signals, including already
	// delivered records until their TTL elapses.
	MaxPendingSignals = 256
	// PendingSignalTTL bounds how long a captured signal stays replayable.
	// Records older than this are pruned by Put and never returned by List.
	PendingSignalTTL = 24 * time.Hour
)

var (
	ErrPendingSignalStoreFull = errors.New("pending signal store is full")
	ErrPendingSignalNotFound  = errors.New("pending signal not found")
	ErrPendingSignalInvalid   = errors.New("invalid pending signal")
)

// PendingSignal is an idempotent MCP signal captured before publication so it
// can be replayed after a disconnected or restarted agent stream.
type PendingSignal struct {
	ID          string          `json:"id"`
	Action      string          `json:"action"`
	SessionID   string          `json:"session_id"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	DeliveredAt *time.Time      `json:"delivered_at,omitempty"`
	Attempts    int             `json:"attempts"`
}

// PendingSignalStore is the bounded capture surface used by signal capture and
// replay. List returns only undelivered, unexpired records ordered by creation
// time; Put is idempotent per record ID.
type PendingSignalStore interface {
	PutPendingSignal(ctx context.Context, signal PendingSignal) error
	ListPendingSignals(ctx context.Context, limit int) ([]PendingSignal, error)
	MarkPendingSignalDelivered(ctx context.Context, id string) error
	DeletePendingSignal(ctx context.Context, id string) error
}

func (j *Journal) PutPendingSignal(ctx context.Context, signal PendingSignal) error {
	if err := validatePendingSignal(signal); err != nil {
		return err
	}
	now := time.Now().UTC()
	if signal.CreatedAt.IsZero() {
		signal.CreatedAt = now
	}
	signal.CreatedAt = signal.CreatedAt.UTC()
	if now.Sub(signal.CreatedAt) > PendingSignalTTL {
		return fmt.Errorf("%w: created_at is older than %s", ErrPendingSignalInvalid, PendingSignalTTL)
	}
	encoded, err := json.Marshal(signal)
	if err != nil {
		return fmt.Errorf("encode pending signal: %w", err)
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.updateLocked(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		bucket, err := tx.CreateBucketIfNotExists(bucketPendingSignals)
		if err != nil {
			return err
		}
		retained, err := pruneExpiredPendingSignals(bucket, now)
		if err != nil {
			return err
		}
		if bucket.Get([]byte(signal.ID)) != nil {
			return nil
		}
		if retained >= MaxPendingSignals {
			return ErrPendingSignalStoreFull
		}
		return bucket.Put([]byte(signal.ID), encoded)
	})
}

func (j *Journal) ListPendingSignals(ctx context.Context, limit int) ([]PendingSignal, error) {
	if limit <= 0 || limit > MaxPendingSignals {
		return nil, fmt.Errorf("%w: limit must be between 1 and %d", ErrPendingSignalInvalid, MaxPendingSignals)
	}
	now := time.Now().UTC()
	j.mu.RLock()
	defer j.mu.RUnlock()
	var signals []PendingSignal
	err := j.viewLocked(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		bucket := tx.Bucket(bucketPendingSignals)
		if bucket == nil {
			return nil
		}
		return bucket.ForEach(func(_, raw []byte) error {
			if raw == nil {
				return nil
			}
			signal, err := decodePendingSignal(raw)
			if err != nil {
				return err
			}
			if signal.DeliveredAt != nil || pendingSignalExpired(signal, now) {
				return nil
			}
			signals = append(signals, signal)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(signals, func(i, j int) bool {
		if signals[i].CreatedAt.Equal(signals[j].CreatedAt) {
			return signals[i].ID < signals[j].ID
		}
		return signals[i].CreatedAt.Before(signals[j].CreatedAt)
	})
	if len(signals) > limit {
		signals = signals[:limit]
	}
	return signals, nil
}

func (j *Journal) MarkPendingSignalDelivered(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrPendingSignalInvalid)
	}
	now := time.Now().UTC()
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.updateLocked(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		bucket := tx.Bucket(bucketPendingSignals)
		if bucket == nil {
			return ErrPendingSignalNotFound
		}
		raw := bucket.Get([]byte(id))
		if raw == nil {
			return ErrPendingSignalNotFound
		}
		signal, err := decodePendingSignal(raw)
		if err != nil {
			return err
		}
		if signal.DeliveredAt != nil {
			return nil
		}
		signal.DeliveredAt = &now
		encoded, err := json.Marshal(signal)
		if err != nil {
			return fmt.Errorf("encode pending signal: %w", err)
		}
		return bucket.Put([]byte(id), encoded)
	})
}

func (j *Journal) DeletePendingSignal(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrPendingSignalInvalid)
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.updateLocked(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		bucket := tx.Bucket(bucketPendingSignals)
		if bucket == nil || bucket.Get([]byte(id)) == nil {
			return ErrPendingSignalNotFound
		}
		return bucket.Delete([]byte(id))
	})
}

// IncrementPendingSignalAttempts records one delivery attempt. It is separate
// from PendingSignalStore because only the replay path needs it.
func (j *Journal) IncrementPendingSignalAttempts(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrPendingSignalInvalid)
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.updateLocked(func(tx *bolt.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		bucket := tx.Bucket(bucketPendingSignals)
		if bucket == nil {
			return ErrPendingSignalNotFound
		}
		raw := bucket.Get([]byte(id))
		if raw == nil {
			return ErrPendingSignalNotFound
		}
		signal, err := decodePendingSignal(raw)
		if err != nil {
			return err
		}
		signal.Attempts++
		encoded, err := json.Marshal(signal)
		if err != nil {
			return fmt.Errorf("encode pending signal: %w", err)
		}
		return bucket.Put([]byte(id), encoded)
	})
}

func validatePendingSignal(signal PendingSignal) error {
	if signal.ID == "" {
		return fmt.Errorf("%w: id is required", ErrPendingSignalInvalid)
	}
	if signal.Action == "" {
		return fmt.Errorf("%w: action is required", ErrPendingSignalInvalid)
	}
	if signal.Attempts < 0 {
		return fmt.Errorf("%w: attempts must not be negative", ErrPendingSignalInvalid)
	}
	if len(signal.Payload) > 0 && !json.Valid(signal.Payload) {
		return fmt.Errorf("%w: payload is not valid JSON", ErrPendingSignalInvalid)
	}
	return nil
}

func decodePendingSignal(raw []byte) (PendingSignal, error) {
	var signal PendingSignal
	if err := json.Unmarshal(raw, &signal); err != nil {
		return PendingSignal{}, ErrJournalCorrupt
	}
	return signal, nil
}

func pendingSignalExpired(signal PendingSignal, now time.Time) bool {
	return now.Sub(signal.CreatedAt) > PendingSignalTTL
}

// pruneExpiredPendingSignals deletes records past their TTL and returns the
// number of retained records. Put rejects new records once the retained count
// reaches MaxPendingSignals, so capacity is never reclaimed by dropping a
// record that is still within its TTL.
func pruneExpiredPendingSignals(bucket *bolt.Bucket, now time.Time) (int, error) {
	retained := 0
	var expired [][]byte
	if err := bucket.ForEach(func(key, raw []byte) error {
		if raw == nil {
			return nil
		}
		signal, err := decodePendingSignal(raw)
		if err != nil {
			return err
		}
		if pendingSignalExpired(signal, now) {
			expired = append(expired, append([]byte(nil), key...))
			return nil
		}
		retained++
		return nil
	}); err != nil {
		return 0, err
	}
	for _, key := range expired {
		if err := bucket.Delete(key); err != nil {
			return 0, err
		}
	}
	return retained, nil
}
