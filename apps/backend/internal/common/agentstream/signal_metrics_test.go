package agentstream

import (
	"expvar"
	"testing"
)

func signalCounterValue(t *testing.T, metric *expvar.Map, label string) int64 {
	t.Helper()
	entry := metric.Get(label)
	if entry == nil {
		return 0
	}
	counter, ok := entry.(*expvar.Int)
	if !ok {
		t.Fatalf("metric label %q has type %T", label, entry)
	}
	return counter.Value()
}

func TestSignalMetricsArePublishedAndBounded(t *testing.T) {
	for _, name := range []string{
		"workflow_step_signal_capture_total",
		"workflow_step_signal_replay_total",
		"workflow_step_signal_delivery_failed_total",
	} {
		if expvar.Get(name) == nil {
			t.Fatalf("metric %q is not published", name)
		}
	}

	RecordSignalCapture("bogus")
	RecordSignalReplay("bogus")
	RecordSignalDeliveryFailed("bogus")
	if signalCaptureTotal.Get("bogus") != nil {
		t.Fatal("capture metric accepted a label outside the closed set")
	}
	if signalReplayTotal.Get("bogus") != nil {
		t.Fatal("replay metric accepted a label outside the closed set")
	}
	if signalDeliveryFailedTotal.Get("bogus") != nil {
		t.Fatal("delivery-failed metric accepted a label outside the closed set")
	}
}

// TestSignalMetricsDistinguishTransportFromCaptureFailure pins the operational
// split Form C needs: writer contention and transport loss show up under the
// agent_stream_* counters, while capture-store outcomes never increment them.
func TestSignalMetricsDistinguishTransportFromCaptureFailure(t *testing.T) {
	captureBefore := signalCounterValue(t, signalCaptureTotal, "captured")
	transportBefore := signalCounterValue(t, mcpRequestFailuresTotal, string(AgentStreamReasonWriteTimeout))

	RecordSignalCapture("captured")
	if got := signalCounterValue(t, signalCaptureTotal, "captured"); got != captureBefore+1 {
		t.Fatalf("capture counter = %d, want %d", got, captureBefore+1)
	}
	if got := signalCounterValue(t, mcpRequestFailuresTotal, string(AgentStreamReasonWriteTimeout)); got != transportBefore {
		t.Fatalf("capture success changed the transport failure counter: %d -> %d", transportBefore, got)
	}

	RecordMCPRequestFailure(AgentStreamReasonWriteTimeout)
	if got := signalCounterValue(t, mcpRequestFailuresTotal, string(AgentStreamReasonWriteTimeout)); got != transportBefore+1 {
		t.Fatalf("transport counter = %d, want %d", got, transportBefore+1)
	}
	if got := signalCounterValue(t, signalCaptureTotal, "captured"); got != captureBefore+1 {
		t.Fatalf("transport failure changed the capture counter: %d", got)
	}

	replayBefore := signalCounterValue(t, signalReplayTotal, "replayed")
	RecordSignalReplay("replayed")
	if got := signalCounterValue(t, signalReplayTotal, "replayed"); got != replayBefore+1 {
		t.Fatalf("replay counter = %d, want %d", got, replayBefore+1)
	}

	failedBefore := signalCounterValue(t, signalDeliveryFailedTotal, "delivery_window_exceeded")
	RecordSignalDeliveryFailed("delivery_window_exceeded")
	if got := signalCounterValue(t, signalDeliveryFailedTotal, "delivery_window_exceeded"); got != failedBefore+1 {
		t.Fatalf("delivery-failed counter = %d, want %d", got, failedBefore+1)
	}
}
