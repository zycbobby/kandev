package journal

import (
	"context"
	"errors"
	"testing"
)

func retainedAbsenceFixture(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	path := CheckStorage(root, "session").Path
	j, err := Open(Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"text":"retained history"}`)
	if _, err := j.PutSubmission(ctx, Submission{
		ID: "prompt:present", SessionID: "session", IncarnationID: "incarnation-current",
		HarnessGeneration: 7, StreamID: "stream-current", Hash: SubmissionHash(payload),
		Payload: payload, State: SubmissionCompleted, TerminalEventRetained: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestReadRetainedReconstructionEvidenceReportsMissingSubmission(t *testing.T) {
	ctx := context.Background()
	evidence, err := ReadRetainedReconstructionEvidence(ctx, RetainedReconstructionEvidenceRequest{
		Root: retainedAbsenceFixture(t), SessionID: "session",
		IncarnationID: "incarnation-current", HarnessGeneration: 7,
		AbsentSubmissionID: "prompt:never-journaled",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !evidence.SubmissionAbsent {
		t.Fatal("missing submission was not reported as absent")
	}
	if evidence.StoredSubmission != nil {
		t.Fatalf("absence probe returned a stored submission: %+v", evidence.StoredSubmission)
	}
}

func TestReadRetainedReconstructionEvidenceReportsPresentSubmission(t *testing.T) {
	ctx := context.Background()
	evidence, err := ReadRetainedReconstructionEvidence(ctx, RetainedReconstructionEvidenceRequest{
		Root: retainedAbsenceFixture(t), SessionID: "session",
		IncarnationID: "incarnation-current", HarnessGeneration: 7,
		AbsentSubmissionID: "prompt:present",
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.SubmissionAbsent {
		t.Fatal("retained submission was reported as absent")
	}
}

func TestReadRetainedReconstructionEvidenceRejectsForeignAbsenceCandidate(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := CheckStorage(root, "session").Path
	j, err := Open(Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"text":"another generation"}`)
	if _, err := j.PutSubmission(ctx, Submission{
		ID: "prompt:foreign", SessionID: "session", IncarnationID: "incarnation-old",
		HarnessGeneration: 6, StreamID: "stream-old", Hash: SubmissionHash(payload),
		Payload: payload, State: SubmissionCompleted, TerminalEventRetained: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = ReadRetainedReconstructionEvidence(ctx, RetainedReconstructionEvidenceRequest{
		Root: root, SessionID: "session", IncarnationID: "incarnation-current",
		HarnessGeneration: 7, AbsentSubmissionID: "prompt:foreign",
	})
	if !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("foreign absence candidate error = %v, want owner mismatch", err)
	}
}
