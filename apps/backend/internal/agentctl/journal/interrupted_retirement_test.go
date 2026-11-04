package journal

import (
	"context"
	"errors"
	"testing"
)

// @covers AC-PLATFORM-DURABLE-AGENT-DELIVERY-006.17
func TestInterruptedRetirementPreservesEvidenceAndReleasesAdmission(t *testing.T) {
	j, err := Open(Config{Path: t.TempDir() + "/journal"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	ctx := context.Background()
	input := Submission{ID: "old", SessionID: "session", IncarnationID: "session", HarnessGeneration: 1, Hash: "hash", Payload: []byte("old instruction"), State: SubmissionInterruptedUnknown}
	if _, err = j.PutSubmission(ctx, input); err != nil {
		t.Fatal(err)
	}
	retired, err := j.RetireSubmission(ctx, "old", 2)
	if err != nil {
		t.Fatal(err)
	}
	if retired.State != SubmissionInterruptedUnknown || string(retired.Payload) != string(input.Payload) || !retired.Retired {
		t.Fatalf("retirement changed uncertainty or evidence: %+v", retired)
	}
	unresolved, err := j.HasUnresolvedSubmissions(ctx)
	if err != nil || unresolved {
		t.Fatalf("retired submission fenced successor: %v, %v", unresolved, err)
	}
	descriptor, err := j.RecoveryDescriptor(ctx, "session", "session", 2, "session:g2")
	if err != nil || descriptor.Unresolved || len(descriptor.Submissions) != 0 {
		t.Fatalf("retired descriptor = %+v, %v", descriptor, err)
	}
}

func TestSuccessorRetirementPreservesUnfinishedEvidence(t *testing.T) {
	for _, state := range []SubmissionState{SubmissionPrepared, SubmissionAccepted, SubmissionDispatching} {
		t.Run(string(state), func(t *testing.T) {
			ctx := context.Background()
			j, event := terminalSubmissionFixture(t, state, false)
			retired, err := j.RetireSubmission(ctx, event.SubmissionID, event.HarnessGeneration+1)
			if err != nil {
				t.Fatal(err)
			}
			if !retired.Retired || retired.State != SubmissionInterruptedUnknown || string(retired.Payload) != "prompt" {
				t.Fatalf("retirement discarded unfinished evidence: %+v", retired)
			}
			unresolved, err := j.HasUnresolvedWork(ctx)
			if err != nil || unresolved {
				t.Fatalf("retirement retained admission fence: %v, %v", unresolved, err)
			}
		})
	}
}

// TestZeroEventAuthorizationRetiresSameGenerationDispatching pins the journal
// primitive: same-generation dispatching work stays fenced without the exact
// zero-event authorization and retires only for the authorized ID.
func TestZeroEventAuthorizationRetiresSameGenerationDispatching(t *testing.T) {
	ctx := context.Background()
	j, err := Open(Config{Path: t.TempDir() + "/journal"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	input := Submission{
		ID: "prompt:dispatched", SessionID: "session", IncarnationID: "session", HarnessGeneration: 2,
		StreamID: "stream", Hash: "hash", Payload: []byte("never started"), State: SubmissionDispatching,
	}
	if _, err = j.PutSubmission(ctx, input); err != nil {
		t.Fatal(err)
	}
	if _, err = j.RetireSubmissionWithEvidence(ctx, input.ID, input.HarnessGeneration, ""); !errors.Is(err, ErrSubmissionGeneration) {
		t.Fatalf("same-generation dispatching retirement without authorization = %v, want %v", err, ErrSubmissionGeneration)
	}
	if _, err = j.RetireSubmissionWithEvidence(ctx, input.ID, input.HarnessGeneration, "prompt:other"); !errors.Is(err, ErrSubmissionGeneration) {
		t.Fatalf("authorization bound to another submission = %v, want %v", err, ErrSubmissionGeneration)
	}
	retired, err := j.RetireSubmissionWithEvidence(ctx, input.ID, input.HarnessGeneration, input.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !retired.Retired || retired.State != SubmissionInterruptedUnknown || string(retired.Payload) != string(input.Payload) {
		t.Fatalf("authorized zero-event retirement = %+v", retired)
	}
}
