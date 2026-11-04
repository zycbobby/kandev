package journal

import (
	"context"
	"errors"
	"testing"
)

func TestPendingSignalNoticeMarksOnce(t *testing.T) {
	j := openPendingSignalJournal(t)
	ctx := context.Background()
	mustPutPendingSignal(t, j, PendingSignal{ID: "signal-notice", Action: "step_complete"})

	marked, err := j.MarkPendingSignalNotified(ctx, "signal-notice")
	if err != nil || !marked {
		t.Fatalf("first notice mark = %v, %v; want true, nil", marked, err)
	}
	marked, err = j.MarkPendingSignalNotified(ctx, "signal-notice")
	if err != nil || marked {
		t.Fatalf("second notice mark = %v, %v; want false, nil", marked, err)
	}

	raw, found := readPendingSignalRaw(t, j, "signal-notice")
	if !found || raw.NotifiedAt == nil {
		t.Fatalf("stored signal after notice = %#v, found=%v", raw, found)
	}
}

func TestPendingSignalNoticeDoesNotSuppressReplay(t *testing.T) {
	j := openPendingSignalJournal(t)
	ctx := context.Background()
	mustPutPendingSignal(t, j, PendingSignal{ID: "signal-replay", Action: "step_complete"})
	if _, err := j.MarkPendingSignalNotified(ctx, "signal-replay"); err != nil {
		t.Fatal(err)
	}

	signals, err := j.ListPendingSignals(ctx, MaxPendingSignals)
	if err != nil {
		t.Fatal(err)
	}
	if len(signals) != 1 || signals[0].ID != "signal-replay" {
		t.Fatalf("notified but undelivered signals = %v, want the captured record", pendingSignalIDs(signals))
	}
}

func TestPendingSignalNoticeSkipsDeliveredRecord(t *testing.T) {
	j := openPendingSignalJournal(t)
	ctx := context.Background()
	mustPutPendingSignal(t, j, PendingSignal{ID: "signal-done", Action: "step_complete"})
	if err := j.MarkPendingSignalDelivered(ctx, "signal-done"); err != nil {
		t.Fatal(err)
	}

	marked, err := j.MarkPendingSignalNotified(ctx, "signal-done")
	if err != nil || marked {
		t.Fatalf("notice mark for delivered record = %v, %v; want false, nil", marked, err)
	}
}

func TestPendingSignalNoticeMissingRecord(t *testing.T) {
	j := openPendingSignalJournal(t)
	if _, err := j.MarkPendingSignalNotified(context.Background(), "missing"); !errors.Is(err, ErrPendingSignalNotFound) {
		t.Fatalf("notice mark error = %v, want ErrPendingSignalNotFound", err)
	}
}

func TestPendingSignalNoticeSurvivesCompaction(t *testing.T) {
	j := openPendingSignalJournal(t)
	ctx := context.Background()
	mustPutPendingSignal(t, j, PendingSignal{ID: "signal-notice", Action: "step_complete"})
	if _, err := j.MarkPendingSignalNotified(ctx, "signal-notice"); err != nil {
		t.Fatal(err)
	}
	if err := j.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	raw, found := readPendingSignalRaw(t, j, "signal-notice")
	if !found || raw.NotifiedAt == nil {
		t.Fatalf("notice marker after compaction = %#v, found=%v", raw, found)
	}
}
