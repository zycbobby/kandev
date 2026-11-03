package journal

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestAppendBatchOversizedEventReturnsEventTooLarge(t *testing.T) {
	j, err := Open(Config{Path: filepath.Join(t.TempDir(), "delivery.bbolt"), MaxEventBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	_, err = j.AppendBatch(context.Background(), []Event{{
		SessionID: "session", IncarnationID: "incarnation", HarnessGeneration: 1,
		StreamID: "stream", Type: "message", Payload: make([]byte, 9),
	}})
	if !errors.Is(err, ErrStreamFull) {
		t.Fatalf("oversized event error = %v, want ErrStreamFull", err)
	}
	if !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("oversized event error = %v, want ErrEventTooLarge", err)
	}
}

func TestAdmissionRejectedMetricUsesBoundedLabels(t *testing.T) {
	for _, label := range []string{"storage_pressure", "storage_error", "event_too_large", "queue_too_large"} {
		if got := boundedDeliveryLabel(label); got != label {
			t.Fatalf("bounded label %q = %q", label, got)
		}
	}
	if got := boundedDeliveryLabel("caller-defined-label"); got != "unknown" {
		t.Fatalf("unbounded label = %q, want unknown", got)
	}
	RecordAdmissionRejected("storage_error")
	RecordEventRejected("queue_too_large")
	if deliveryAdmissionRejectedTotal.Get("storage_error") == nil {
		t.Fatal("admission rejection metric did not record the bounded reason")
	}
	if deliveryEventRejectedTotal.Get("queue_too_large") == nil {
		t.Fatal("event rejection metric did not record the bounded reason")
	}
	if deliveryAdmissionRejectedTotal.Get("caller-defined-label") != nil {
		t.Fatal("admission rejection metric accepted an unbounded label")
	}
}
