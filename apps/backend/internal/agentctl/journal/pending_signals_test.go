package journal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

var _ PendingSignalStore = (*Journal)(nil)

func openPendingSignalJournal(t *testing.T) *Journal {
	t.Helper()
	j, err := Open(Config{Path: filepath.Join(t.TempDir(), "delivery.bbolt")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	return j
}

func mustPutPendingSignal(t *testing.T, j *Journal, signal PendingSignal) {
	t.Helper()
	if err := j.PutPendingSignal(context.Background(), signal); err != nil {
		t.Fatalf("put pending signal %q: %v", signal.ID, err)
	}
}

func pendingSignalIDs(signals []PendingSignal) []string {
	ids := make([]string, len(signals))
	for i, signal := range signals {
		ids[i] = signal.ID
	}
	return ids
}

func putPendingSignalRaw(t *testing.T, j *Journal, signal PendingSignal) {
	t.Helper()
	encoded, err := json.Marshal(signal)
	if err != nil {
		t.Fatal(err)
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	db, err := j.dbLocked()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(bucketPendingSignals)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(signal.ID), encoded)
	}); err != nil {
		t.Fatal(err)
	}
}

func readPendingSignalRaw(t *testing.T, j *Journal, id string) (PendingSignal, bool) {
	t.Helper()
	j.mu.RLock()
	defer j.mu.RUnlock()
	db, err := j.dbLocked()
	if err != nil {
		t.Fatal(err)
	}
	var signal PendingSignal
	found := false
	if err := db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketPendingSignals)
		if bucket == nil {
			return nil
		}
		raw := bucket.Get([]byte(id))
		if raw == nil {
			return nil
		}
		found = true
		return json.Unmarshal(raw, &signal)
	}); err != nil {
		t.Fatal(err)
	}
	return signal, found
}

func deletePendingSignalBucket(t *testing.T, j *Journal) {
	t.Helper()
	j.mu.RLock()
	defer j.mu.RUnlock()
	db, err := j.dbLocked()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		if tx.Bucket(bucketPendingSignals) == nil {
			return nil
		}
		return tx.DeleteBucket(bucketPendingSignals)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPendingSignalPutListMarkDelete(t *testing.T) {
	j := openPendingSignalJournal(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)

	mustPutPendingSignal(t, j, PendingSignal{
		ID: "signal-3", Action: "step_complete", SessionID: "session-1",
		Payload: json.RawMessage(`{"b":2}`), CreatedAt: now.Add(-time.Minute),
	})
	mustPutPendingSignal(t, j, PendingSignal{
		ID: "signal-1", Action: "step_complete", SessionID: "session-1",
		Payload: json.RawMessage(`{"a":1}`), CreatedAt: now.Add(-3 * time.Minute),
	})
	mustPutPendingSignal(t, j, PendingSignal{
		ID: "signal-2", Action: "step_complete", SessionID: "session-2",
		Payload: json.RawMessage(`{"c":3}`), CreatedAt: now.Add(-2 * time.Minute),
	})

	signals, err := j.ListPendingSignals(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pendingSignalIDs(signals), []string{"signal-1", "signal-2", "signal-3"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("pending signal order = %v, want %v", got, want)
	}
	if signals[0].Action != "step_complete" || signals[0].SessionID != "session-1" ||
		string(signals[0].Payload) != `{"a":1}` || signals[0].Attempts != 0 || signals[0].DeliveredAt != nil {
		t.Fatalf("pending signal = %#v", signals[0])
	}

	mustPutPendingSignal(t, j, PendingSignal{
		ID: "signal-2", Action: "step_complete", SessionID: "session-2",
		Payload: json.RawMessage(`{"ignored":true}`), CreatedAt: now,
	})
	signals, err = j.ListPendingSignals(ctx, 10)
	if err != nil || len(signals) != 3 || string(signals[1].Payload) != `{"c":3}` {
		t.Fatalf("duplicate put changed pending signals: %#v, err=%v", signals, err)
	}

	if err := j.MarkPendingSignalDelivered(ctx, "signal-2"); err != nil {
		t.Fatal(err)
	}
	if err := j.MarkPendingSignalDelivered(ctx, "signal-2"); err != nil {
		t.Fatalf("repeat mark delivered: %v", err)
	}
	signals, err = j.ListPendingSignals(ctx, 10)
	if err != nil || fmt.Sprint(pendingSignalIDs(signals)) != fmt.Sprint([]string{"signal-1", "signal-3"}) {
		t.Fatalf("pending signals after mark delivered = %v, err=%v", pendingSignalIDs(signals), err)
	}
	delivered, found := readPendingSignalRaw(t, j, "signal-2")
	if !found || delivered.DeliveredAt == nil {
		t.Fatalf("delivered record = %#v, found=%v", delivered, found)
	}

	if err := j.DeletePendingSignal(ctx, "signal-1"); err != nil {
		t.Fatal(err)
	}
	signals, err = j.ListPendingSignals(ctx, 10)
	if err != nil || fmt.Sprint(pendingSignalIDs(signals)) != fmt.Sprint([]string{"signal-3"}) {
		t.Fatalf("pending signals after delete = %v, err=%v", pendingSignalIDs(signals), err)
	}
}

func TestPendingSignalValidationAndNotFound(t *testing.T) {
	j := openPendingSignalJournal(t)
	ctx := context.Background()

	if err := j.PutPendingSignal(ctx, PendingSignal{Action: "step_complete"}); !errors.Is(err, ErrPendingSignalInvalid) {
		t.Fatalf("put without id error = %v, want ErrPendingSignalInvalid", err)
	}
	if err := j.PutPendingSignal(ctx, PendingSignal{ID: "signal-1"}); !errors.Is(err, ErrPendingSignalInvalid) {
		t.Fatalf("put without action error = %v, want ErrPendingSignalInvalid", err)
	}
	if err := j.PutPendingSignal(ctx, PendingSignal{
		ID: "signal-old", Action: "step_complete", CreatedAt: time.Now().UTC().Add(-PendingSignalTTL - time.Minute),
	}); !errors.Is(err, ErrPendingSignalInvalid) {
		t.Fatalf("put expired signal error = %v, want ErrPendingSignalInvalid", err)
	}
	if _, err := j.ListPendingSignals(ctx, 0); !errors.Is(err, ErrPendingSignalInvalid) {
		t.Fatalf("list with zero limit error = %v, want ErrPendingSignalInvalid", err)
	}
	if _, err := j.ListPendingSignals(ctx, MaxPendingSignals+1); !errors.Is(err, ErrPendingSignalInvalid) {
		t.Fatalf("list with oversized limit error = %v, want ErrPendingSignalInvalid", err)
	}
	if err := j.MarkPendingSignalDelivered(ctx, "missing"); !errors.Is(err, ErrPendingSignalNotFound) {
		t.Fatalf("mark missing error = %v, want ErrPendingSignalNotFound", err)
	}
	if err := j.DeletePendingSignal(ctx, "missing"); !errors.Is(err, ErrPendingSignalNotFound) {
		t.Fatalf("delete missing error = %v, want ErrPendingSignalNotFound", err)
	}
	if err := j.IncrementPendingSignalAttempts(ctx, "missing"); !errors.Is(err, ErrPendingSignalNotFound) {
		t.Fatalf("increment missing error = %v, want ErrPendingSignalNotFound", err)
	}
}

func TestPendingSignalStoreRejectsWhenFull(t *testing.T) {
	j := openPendingSignalJournal(t)
	ctx := context.Background()
	now := time.Now().UTC()

	for i := 0; i < MaxPendingSignals; i++ {
		mustPutPendingSignal(t, j, PendingSignal{
			ID: fmt.Sprintf("signal-%03d", i), Action: "step_complete",
			CreatedAt: now.Add(time.Duration(i) * time.Millisecond),
		})
	}
	overflow := PendingSignal{ID: "signal-overflow", Action: "step_complete", CreatedAt: now.Add(time.Hour)}
	if err := j.PutPendingSignal(ctx, overflow); !errors.Is(err, ErrPendingSignalStoreFull) {
		t.Fatalf("put when full error = %v, want ErrPendingSignalStoreFull", err)
	}
	signals, err := j.ListPendingSignals(ctx, MaxPendingSignals)
	if err != nil || len(signals) != MaxPendingSignals {
		t.Fatalf("pending signals after overflow = %d, err=%v", len(signals), err)
	}
	for _, signal := range signals {
		if signal.ID == overflow.ID {
			t.Fatalf("overflow signal was silently stored")
		}
	}

	if err := j.DeletePendingSignal(ctx, "signal-000"); err != nil {
		t.Fatal(err)
	}
	mustPutPendingSignal(t, j, overflow)
	signals, err = j.ListPendingSignals(ctx, MaxPendingSignals)
	if err != nil || len(signals) != MaxPendingSignals {
		t.Fatalf("pending signals after freeing a slot = %d, err=%v", len(signals), err)
	}
	if signals[len(signals)-1].ID != overflow.ID {
		t.Fatalf("last pending signal = %q, want %q", signals[len(signals)-1].ID, overflow.ID)
	}
}

func TestPendingSignalTTLExpiry(t *testing.T) {
	j := openPendingSignalJournal(t)
	ctx := context.Background()
	now := time.Now().UTC()

	putPendingSignalRaw(t, j, PendingSignal{
		ID: "signal-fresh", Action: "step_complete", CreatedAt: now.Add(-23 * time.Hour),
	})
	putPendingSignalRaw(t, j, PendingSignal{
		ID: "signal-expired", Action: "step_complete", CreatedAt: now.Add(-25 * time.Hour),
	})
	signals, err := j.ListPendingSignals(ctx, MaxPendingSignals)
	if err != nil || fmt.Sprint(pendingSignalIDs(signals)) != fmt.Sprint([]string{"signal-fresh"}) {
		t.Fatalf("pending signals with ttl = %v, err=%v", pendingSignalIDs(signals), err)
	}

	mustPutPendingSignal(t, j, PendingSignal{ID: "signal-new", Action: "step_complete"})
	if _, found := readPendingSignalRaw(t, j, "signal-expired"); found {
		t.Fatalf("expired signal was not pruned by put")
	}
	if _, found := readPendingSignalRaw(t, j, "signal-fresh"); !found {
		t.Fatalf("signal was deleted before its ttl elapsed")
	}
}

func TestPendingSignalTTLExpiryFreesCapacity(t *testing.T) {
	j := openPendingSignalJournal(t)
	ctx := context.Background()
	expiredAt := time.Now().UTC().Add(-PendingSignalTTL - time.Hour)

	for i := 0; i < MaxPendingSignals; i++ {
		putPendingSignalRaw(t, j, PendingSignal{
			ID: fmt.Sprintf("signal-expired-%03d", i), Action: "step_complete", CreatedAt: expiredAt,
		})
	}
	mustPutPendingSignal(t, j, PendingSignal{ID: "signal-new", Action: "step_complete"})
	signals, err := j.ListPendingSignals(ctx, MaxPendingSignals)
	if err != nil || fmt.Sprint(pendingSignalIDs(signals)) != fmt.Sprint([]string{"signal-new"}) {
		t.Fatalf("pending signals after ttl pruning = %v, err=%v", pendingSignalIDs(signals), err)
	}
}

func TestPendingSignalAttemptsIncrement(t *testing.T) {
	j := openPendingSignalJournal(t)
	ctx := context.Background()

	mustPutPendingSignal(t, j, PendingSignal{ID: "signal-1", Action: "step_complete"})
	for i := 1; i <= 2; i++ {
		if err := j.IncrementPendingSignalAttempts(ctx, "signal-1"); err != nil {
			t.Fatal(err)
		}
		signals, err := j.ListPendingSignals(ctx, MaxPendingSignals)
		if err != nil || len(signals) != 1 || signals[0].Attempts != i {
			t.Fatalf("attempts after increment %d = %#v, err=%v", i, signals, err)
		}
	}
	if err := j.MarkPendingSignalDelivered(ctx, "signal-1"); err != nil {
		t.Fatal(err)
	}
	delivered, found := readPendingSignalRaw(t, j, "signal-1")
	if !found || delivered.Attempts != 2 || delivered.DeliveredAt == nil {
		t.Fatalf("delivered signal = %#v, found=%v", delivered, found)
	}
}

func TestPendingSignalStoreSurvivesCompaction(t *testing.T) {
	j := openPendingSignalJournal(t)
	ctx := context.Background()

	mustPutPendingSignal(t, j, PendingSignal{
		ID: "signal-pending", Action: "step_complete", SessionID: "session-1",
		Payload: json.RawMessage(`{"keep":true}`), CreatedAt: time.Now().UTC().Add(-time.Minute),
	})
	mustPutPendingSignal(t, j, PendingSignal{ID: "signal-delivered", Action: "step_complete"})
	if err := j.MarkPendingSignalDelivered(ctx, "signal-delivered"); err != nil {
		t.Fatal(err)
	}

	if err := j.Compact(ctx); err != nil {
		t.Fatal(err)
	}

	signals, err := j.ListPendingSignals(ctx, MaxPendingSignals)
	if err != nil || len(signals) != 1 || signals[0].ID != "signal-pending" ||
		signals[0].SessionID != "session-1" || string(signals[0].Payload) != `{"keep":true}` {
		t.Fatalf("pending signals after compaction = %#v, err=%v", signals, err)
	}
	delivered, found := readPendingSignalRaw(t, j, "signal-delivered")
	if !found || delivered.DeliveredAt == nil {
		t.Fatalf("delivered signal after compaction = %#v, found=%v", delivered, found)
	}
}

func TestPendingSignalListOnJournalsWithoutBucket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delivery.bbolt")
	j, err := Open(Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	deletePendingSignalBucket(t, j)
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}

	readOnly, err := Open(Config{Path: path, ReadOnly: true})
	if err != nil {
		t.Fatalf("open journal without pending signals bucket: %v", err)
	}
	t.Cleanup(func() { _ = readOnly.Close() })
	signals, err := readOnly.ListPendingSignals(context.Background(), MaxPendingSignals)
	if err != nil || len(signals) != 0 {
		t.Fatalf("pending signals without bucket = %v, err=%v", signals, err)
	}
}
