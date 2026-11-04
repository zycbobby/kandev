package journal

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/common/processidentity"
)

const (
	retirementSessionID     = "session-retirement"
	retirementIncarnationID = "incarnation-retirement"
	retirementStreamID      = "stream-retirement"
	retirementSubmissionID  = "prompt:retirement"
	retirementGeneration    = uint64(3)
	zeroEventSubmissionID   = "prompt:zero-event"
	zeroEventAuthorizedID   = "prompt:zero-event-authorized"
	zeroEventPeerID         = "prompt:zero-event-peer"
)

// deadRetirementOwner spawns an isolated session leader, captures its identity,
// and reaps it so the journal can prove the historical owner terminated.
func deadRetirementOwner(t *testing.T) processidentity.Identity {
	t.Helper()
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid is unavailable; an isolated owner session cannot be created")
	}
	command := exec.Command("setsid", "/bin/sleep", "60")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	identity := awaitSessionLeader(t, command.Process.Pid)
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	return identity
}

// awaitSessionLeader waits for the exec'd setsid process to establish its own
// session before the identity is captured.
func awaitSessionLeader(t *testing.T, pid int) processidentity.Identity {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		identity, err := processidentity.Capture(pid)
		if err == nil && identity.SessionID == identity.PID && identity.GroupID == identity.PID {
			return identity
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d did not become a session leader: %+v, %v", pid, identity, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func retirementPayload() ([]byte, string) {
	payload := []byte(`{"text":"abandoned instruction"}`)
	return payload, SubmissionHash(payload)
}

func seedRetirementJournal(t *testing.T, extra []Submission) (string, StorageCapability, []Submission) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	capability := CheckStorage(root, retirementSessionID)
	payload, hash := retirementPayload()
	journal, err := Open(Config{Path: capability.Path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = journal.PutSubmission(ctx, Submission{
		ID: retirementSubmissionID, SessionID: retirementSessionID, IncarnationID: retirementIncarnationID,
		HarnessGeneration: retirementGeneration, StreamID: retirementStreamID, Hash: hash, Payload: payload,
		State: SubmissionInterruptedUnknown,
	}); err != nil {
		t.Fatal(err)
	}
	for _, submission := range extra {
		if _, err = journal.PutSubmission(ctx, submission); err != nil {
			t.Fatal(err)
		}
	}
	if err = journal.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Open(Config{Path: capability.Path, ExistingOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	seeded, err := snapshot.ListSubmissions(ctx, retirementSessionID)
	if err != nil {
		t.Fatal(err)
	}
	return root, capability, seeded
}

func retirementRequest(root string, owner processidentity.Identity) RetainedRetirementRequest {
	_, hash := retirementPayload()
	return RetainedRetirementRequest{
		Root: root, SessionID: retirementSessionID, ExecutionID: "execution-retirement",
		IncarnationID: retirementIncarnationID, HarnessGeneration: retirementGeneration,
		Submissions: []RetainedRetirementTarget{{
			ID: retirementSubmissionID, StreamID: retirementStreamID, Hash: hash,
			HarnessGeneration: retirementGeneration, State: SubmissionInterruptedUnknown,
		}},
		OriginalRuntime: owner,
	}
}

// requireRetirementUnchanged re-reads the journal and asserts every retained
// record kept its identity, state, payload, and update time.
func requireRetirementUnchanged(t *testing.T, capability StorageCapability, before []Submission) {
	t.Helper()
	ctx := context.Background()
	reopened, err := Open(Config{Path: capability.Path, ExistingOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	after, err := reopened.ListSubmissions(ctx, retirementSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("a rejected retirement changed the submission set: %d -> %d", len(before), len(after))
	}
	expected := make(map[string]Submission, len(before))
	for _, submission := range before {
		expected[submission.ID] = submission
	}
	for _, submission := range after {
		original, ok := expected[submission.ID]
		if !ok || submission.Retired != original.Retired || submission.State != original.State ||
			submission.Hash != original.Hash || submission.StreamID != original.StreamID ||
			submission.HarnessGeneration != original.HarnessGeneration ||
			!bytes.Equal(submission.Payload, original.Payload) || !submission.UpdatedAt.Equal(original.UpdatedAt) {
			t.Fatalf("a rejected retirement changed %s", submission.ID)
		}
	}
}

// zeroEventSubmission builds a never-started retirement fixture: a prompt
// submission with its payload hash, owned by the retirement session.
func zeroEventSubmission(id, streamID, payloadText string, state SubmissionState) Submission {
	payload := []byte(payloadText)
	return Submission{
		ID: id, SessionID: retirementSessionID, IncarnationID: retirementIncarnationID,
		HarnessGeneration: retirementGeneration, StreamID: streamID,
		Hash: SubmissionHash(payload), Payload: payload, State: state,
	}
}

// seedZeroEventRetirementJournal stores never-started submissions so an
// explicit zero-event authorization has exact targets.
func seedZeroEventRetirementJournal(t *testing.T, submissions []Submission) (string, StorageCapability, []Submission) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	capability := CheckStorage(root, retirementSessionID)
	store, err := Open(Config{Path: capability.Path})
	if err != nil {
		t.Fatal(err)
	}
	for _, submission := range submissions {
		if _, err = store.PutSubmission(ctx, submission); err != nil {
			t.Fatal(err)
		}
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Open(Config{Path: capability.Path, ExistingOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	seeded, err := snapshot.ListSubmissions(ctx, retirementSessionID)
	if err != nil {
		t.Fatal(err)
	}
	return root, capability, seeded
}

func zeroEventRetirementRequest(root string, owner processidentity.Identity, submissions []Submission, neverStartedID string) RetainedRetirementRequest {
	targets := make([]RetainedRetirementTarget, 0, len(submissions))
	for _, submission := range submissions {
		targets = append(targets, RetainedRetirementTarget{
			ID: submission.ID, StreamID: submission.StreamID, Hash: submission.Hash,
			HarnessGeneration: submission.HarnessGeneration, State: submission.State,
		})
	}
	return RetainedRetirementRequest{
		Root: root, SessionID: retirementSessionID, ExecutionID: "execution-retirement",
		IncarnationID: retirementIncarnationID, HarnessGeneration: retirementGeneration,
		Submissions: targets, OriginalRuntime: owner, NeverStartedSubmissionID: neverStartedID,
	}
}

// TestRetainedRetirement_ZeroEventRetiresDispatchingTarget proves the explicit
// never-started authorization retires a dispatching target with no terminal
// evidence while the payload, hash, and post-retirement descriptor are
// preserved.
func TestRetainedRetirement_ZeroEventRetiresDispatchingTarget(t *testing.T) {
	ctx := context.Background()
	target := zeroEventSubmission(zeroEventSubmissionID, retirementStreamID, `{"text":"never started instruction"}`, SubmissionDispatching)
	root, capability, _ := seedZeroEventRetirementJournal(t, []Submission{target})

	response, err := RetireRetainedRecovery(ctx, zeroEventRetirementRequest(root, deadRetirementOwner(t), []Submission{target}, target.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !response.ProcessTerminated {
		t.Fatal("a proven-dead owner must be reported as terminated")
	}
	if response.Descriptor.Unresolved || response.Descriptor.SubmissionCount != 0 || len(response.Descriptor.Submissions) != 0 {
		t.Fatalf("descriptor still reports retained work: %+v", response.Descriptor)
	}
	if len(response.Submissions) != 1 || !response.Submissions[0].Retired || response.Submissions[0].State != SubmissionInterruptedUnknown {
		t.Fatalf("zero-event retirement = %+v", response.Submissions)
	}
	if response.Submissions[0].Payload != nil {
		t.Fatal("zero-event retirement must not return the retained payload")
	}

	reopened, err := Open(Config{Path: capability.Path, ExistingOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	stored, err := reopened.GetSubmission(ctx, target.ID)
	if err != nil {
		t.Fatalf("zero-event retirement must not delete the retained record: %v", err)
	}
	if !stored.Retired || stored.State != SubmissionInterruptedUnknown {
		t.Fatalf("stored zero-event retirement = %+v", stored)
	}
	if stored.Hash != target.Hash || !bytes.Equal(stored.Payload, target.Payload) {
		t.Fatal("zero-event retirement must preserve the retained payload and hash")
	}
}

// TestRetainedRetirement_WithoutZeroEventEvidenceRejectsDispatching keeps the
// default contract: a dispatching target with no terminal evidence stays
// non-retireable without the explicit never-started authorization.
func TestRetainedRetirement_WithoutZeroEventEvidenceRejectsDispatching(t *testing.T) {
	ctx := context.Background()
	target := zeroEventSubmission(zeroEventSubmissionID, retirementStreamID, `{"text":"never started instruction"}`, SubmissionDispatching)
	root, capability, seeded := seedZeroEventRetirementJournal(t, []Submission{target})

	_, err := RetireRetainedRecovery(ctx, zeroEventRetirementRequest(root, deadRetirementOwner(t), []Submission{target}, ""))
	if !errors.Is(err, ErrSubmissionState) {
		t.Fatalf("unauthorized dispatching retirement error = %v, want %v", err, ErrSubmissionState)
	}
	requireRetirementUnchanged(t, capability, seeded)
}

// TestRetainedRetirement_ZeroEventAuthorizationIsTargetBound proves the
// authorization relaxes only the exact submission ID it names; every other
// dispatching target keeps the interrupted_unknown requirement.
func TestRetainedRetirement_ZeroEventAuthorizationIsTargetBound(t *testing.T) {
	ctx := context.Background()
	authorized := zeroEventSubmission(zeroEventAuthorizedID, "stream-zero-event-authorized", `{"text":"authorized instruction"}`, SubmissionDispatching)
	peer := zeroEventSubmission(zeroEventPeerID, "stream-zero-event-peer", `{"text":"peer instruction"}`, SubmissionDispatching)
	targets := []Submission{authorized, peer}
	root, capability, seeded := seedZeroEventRetirementJournal(t, targets)

	_, err := RetireRetainedRecovery(ctx, zeroEventRetirementRequest(root, deadRetirementOwner(t), targets, authorized.ID))
	if !errors.Is(err, ErrSubmissionState) {
		t.Fatalf("authorization bound to another target error = %v, want %v", err, ErrSubmissionState)
	}
	requireRetirementUnchanged(t, capability, seeded)
}

func TestRetireRetainedRecoveryRetiresDeadOwnerEvidence(t *testing.T) {
	ctx := context.Background()
	root, capability, _ := seedRetirementJournal(t, nil)
	payload, hash := retirementPayload()
	retirementJournal, err := Open(Config{Path: capability.Path, ExistingOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = retirementJournal.Append(ctx, Event{
		SessionID: retirementSessionID, IncarnationID: retirementIncarnationID,
		HarnessGeneration: retirementGeneration, StreamID: retirementStreamID,
		SubmissionID: retirementSubmissionID, Type: "message", Payload: []byte(`{"text":"abandoned output"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err = retirementJournal.Close(); err != nil {
		t.Fatal(err)
	}

	response, err := RetireRetainedRecovery(ctx, retirementRequest(root, deadRetirementOwner(t)))
	if err != nil {
		t.Fatal(err)
	}
	if !response.ProcessTerminated {
		t.Fatal("a proven-dead owner must be reported as terminated")
	}
	if response.Descriptor.Unresolved || response.Descriptor.SubmissionCount != 0 || len(response.Descriptor.Submissions) != 0 {
		t.Fatalf("descriptor still reports retained work: %+v", response.Descriptor)
	}
	if len(response.Submissions) != 1 || !response.Submissions[0].Retired {
		t.Fatalf("retired submissions = %+v", response.Submissions)
	}
	if response.Submissions[0].Payload != nil {
		t.Fatal("retirement must not return the retained payload")
	}

	reopened, err := Open(Config{Path: capability.Path, ExistingOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	stored, err := reopened.GetSubmission(ctx, retirementSubmissionID)
	if err != nil {
		t.Fatalf("retirement must not delete the retained record: %v", err)
	}
	if !stored.Retired || stored.State != SubmissionInterruptedUnknown {
		t.Fatalf("stored retirement = %+v", stored)
	}
	if !bytes.Equal(stored.Payload, payload) || stored.Hash != hash {
		t.Fatal("retirement must preserve the retained payload and hash")
	}
	_, stream, err := reopened.Replay(ctx, retirementStreamID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if stream.Sealed {
		t.Fatal("same-generation retirement must not seal the current stream")
	}
}

func TestRetireRetainedRecoveryRejectsLiveOwner(t *testing.T) {
	ctx := context.Background()
	root, capability, seeded := seedRetirementJournal(t, nil)
	owner, err := processidentity.Capture(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}

	_, err = RetireRetainedRecovery(ctx, retirementRequest(root, owner))
	if !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("live owner retirement: %v", err)
	}
	requireRetirementUnchanged(t, capability, seeded)
}

// TestRetireRetainedRecoveryRejectsUnprovableTargets covers every rejection
// that must leave the journal byte-identical.
func TestRetireRetainedRecoveryRejectsUnprovableTargets(t *testing.T) {
	cases := map[string]struct {
		extra   []Submission
		mutate  func(*RetainedRetirementRequest)
		wantErr error
	}{
		"hash mismatch": {
			mutate: func(request *RetainedRetirementRequest) {
				request.Submissions[0].Hash = "wrong-hash"
			},
			wantErr: ErrSubmissionConflict,
		},
		"terminal retained": {
			extra: []Submission{{
				ID: "prompt:terminal", SessionID: retirementSessionID, IncarnationID: retirementIncarnationID,
				HarnessGeneration: retirementGeneration, StreamID: "stream-terminal", Hash: "terminal-hash",
				Payload: []byte(`{"text":"terminal"}`), State: SubmissionInterruptedUnknown,
				TerminalEventRetained: true, TerminalSequence: 1,
			}},
			mutate: func(request *RetainedRetirementRequest) {
				request.Submissions = append(request.Submissions, RetainedRetirementTarget{
					ID: "prompt:terminal", StreamID: "stream-terminal", Hash: "terminal-hash",
					HarnessGeneration: retirementGeneration, State: SubmissionInterruptedUnknown,
				})
			},
			wantErr: ErrSubmissionState,
		},
		"unlisted unresolved peer": {
			extra: []Submission{{
				ID: "prompt:peer", SessionID: retirementSessionID, IncarnationID: retirementIncarnationID,
				HarnessGeneration: retirementGeneration, StreamID: "stream-peer", Hash: "peer-hash",
				Payload: []byte(`{"text":"peer"}`), State: SubmissionInterruptedUnknown,
			}},
			wantErr: ErrSubmissionConflict,
		},
		"authorization without a matching target": {
			mutate: func(request *RetainedRetirementRequest) {
				request.NeverStartedSubmissionID = "prompt:unlisted"
			},
			wantErr: ErrOwnerMismatch,
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			root, capability, seeded := seedRetirementJournal(t, testCase.extra)
			request := retirementRequest(root, deadRetirementOwner(t))
			if testCase.mutate != nil {
				testCase.mutate(&request)
			}

			_, err := RetireRetainedRecovery(ctx, request)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("retirement error = %v, want %v", err, testCase.wantErr)
			}
			requireRetirementUnchanged(t, capability, seeded)
		})
	}
}
