package journal

import (
	"bytes"
	"context"
	"errors"

	"github.com/kandev/kandev/internal/common/processidentity"
)

// RetainedRetirementTarget names one exact unresolved submission whose owner
// process is proven dead. State is required so a live or terminal record can
// never be retired by a stale summary.
type RetainedRetirementTarget struct {
	ID                string          `json:"id"`
	StreamID          string          `json:"stream_id"`
	Hash              string          `json:"hash"`
	HarnessGeneration uint64          `json:"harness_generation"`
	State             SubmissionState `json:"state"`
}

// RetainedRetirementRequest retires the complete unresolved inventory of one
// retained owner journal. The backend must enumerate every unresolved
// submission; a partial inventory is rejected so no peer keeps admission
// blocked.
type RetainedRetirementRequest struct {
	Root              string                     `json:"root"`
	SessionID         string                     `json:"session_id"`
	ExecutionID       string                     `json:"execution_id"`
	IncarnationID     string                     `json:"incarnation_id"`
	HarnessGeneration uint64                     `json:"harness_generation"`
	Submissions       []RetainedRetirementTarget `json:"submissions"`
	OriginalRuntime   processidentity.Identity   `json:"original_runtime"`
}

// RetainedRetirementResponse reports the post-retirement descriptor and the
// preserved records. Payloads never cross this boundary.
type RetainedRetirementResponse struct {
	Descriptor        RecoveryDescriptor `json:"descriptor"`
	Submissions       []Submission       `json:"submissions"`
	ProcessTerminated bool               `json:"process_terminated"`
}

// RetireRetainedRecovery marks abandoned submissions retired in an existing
// owner journal without deleting them, sealing the current stream, or
// fabricating a terminal outcome. The owner process must be proven terminated,
// and every unresolved submission in the descriptor must be named by an exact
// target; anything else fails closed.
func RetireRetainedRecovery(ctx context.Context, request RetainedRetirementRequest) (*RetainedRetirementResponse, error) {
	if !validRetainedRetirementRequest(request) {
		return nil, ErrOwnerMismatch
	}
	retained, err := openRetainedJournal(RetainedRecoveryRequest{Root: request.Root, SessionID: request.SessionID}, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = retained.Close() }()

	descriptor, err := retained.RecoveryDescriptor(ctx, request.SessionID, request.IncarnationID, request.HarnessGeneration, "")
	if err != nil {
		return nil, err
	}
	pending, err := validateRetainedRetirement(ctx, retained, request, descriptor)
	if err != nil {
		return nil, err
	}
	if err := proveRetainedRetirementOwnerTerminated(request.OriginalRuntime); err != nil {
		return nil, err
	}
	for _, submission := range pending {
		updated, err := retained.RetireSubmission(ctx, submission.ID, request.HarnessGeneration)
		if err != nil {
			return nil, err
		}
		if !updated.Retired {
			return nil, ErrSubmissionConflict
		}
	}
	descriptor, submissions, err := confirmRetainedRetirement(ctx, retained, request, pending)
	if err != nil {
		return nil, err
	}
	return &RetainedRetirementResponse{
		Descriptor: descriptor, Submissions: submissions, ProcessTerminated: true,
	}, nil
}

func validRetainedRetirementRequest(request RetainedRetirementRequest) bool {
	return request.SessionID != "" && request.ExecutionID != "" && request.IncarnationID != "" &&
		request.HarnessGeneration > 0 && len(request.Submissions) > 0 &&
		request.OriginalRuntime.Validate() == nil
}

// validateRetainedRetirement verifies every target against the stored record
// and requires the targets to cover the descriptor's complete unresolved
// inventory. An already-retired target is accepted so a retry after a crashed
// settlement can finish the same effect.
func validateRetainedRetirement(
	ctx context.Context,
	retained *Journal,
	request RetainedRetirementRequest,
	descriptor RecoveryDescriptor,
) ([]Submission, error) {
	if descriptor.SubmissionsTruncated || descriptor.SessionID != request.SessionID ||
		descriptor.IncarnationID != request.IncarnationID || descriptor.HarnessGeneration != request.HarnessGeneration {
		return nil, ErrSubmissionConflict
	}
	inventory, err := retainedRetirementInventory(descriptor)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(request.Submissions))
	pending := make([]Submission, 0, len(request.Submissions))
	for _, target := range request.Submissions {
		submission, err := readRetainedRetirementTarget(ctx, retained, request, target, seen)
		if err != nil {
			return nil, err
		}
		if !submission.Retired {
			summary, listed := inventory[target.ID]
			if !listed || !SubmissionSummariesEqual(summary, submissionSummary(submission)) {
				return nil, ErrSubmissionConflict
			}
			delete(inventory, target.ID)
		}
		pending = append(pending, submission)
	}
	if len(inventory) != 0 {
		return nil, ErrSubmissionConflict
	}
	return pending, nil
}

func retainedRetirementInventory(descriptor RecoveryDescriptor) (map[string]SubmissionSummary, error) {
	inventory := make(map[string]SubmissionSummary, len(descriptor.Submissions))
	for _, summary := range descriptor.Submissions {
		if _, duplicate := inventory[summary.ID]; duplicate {
			return nil, ErrSubmissionConflict
		}
		inventory[summary.ID] = summary
	}
	return inventory, nil
}

func readRetainedRetirementTarget(
	ctx context.Context,
	retained *Journal,
	request RetainedRetirementRequest,
	target RetainedRetirementTarget,
	seen map[string]struct{},
) (Submission, error) {
	if !validRetainedRetirementTarget(target, request.HarnessGeneration) {
		return Submission{}, ErrSubmissionState
	}
	if _, duplicate := seen[target.ID]; duplicate {
		return Submission{}, ErrSubmissionConflict
	}
	seen[target.ID] = struct{}{}
	submission, err := retained.GetSubmission(ctx, target.ID)
	if err != nil {
		if errors.Is(err, ErrSubmissionNotFound) {
			return Submission{}, ErrSubmissionConflict
		}
		return Submission{}, err
	}
	if err := validateRetainedRetirementSubmission(submission, target, request.SessionID); err != nil {
		return Submission{}, err
	}
	return submission, nil
}

func validRetainedRetirementTarget(target RetainedRetirementTarget, requestGeneration uint64) bool {
	return target.ID != "" && target.StreamID != "" && target.Hash != "" &&
		target.HarnessGeneration > 0 && target.HarnessGeneration <= requestGeneration &&
		target.State == SubmissionInterruptedUnknown
}

func validateRetainedRetirementSubmission(submission Submission, target RetainedRetirementTarget, sessionID string) error {
	if submission.SessionID != sessionID {
		return ErrOwnerMismatch
	}
	if submission.StreamID != target.StreamID || submission.Hash != target.Hash ||
		submission.HarnessGeneration != target.HarnessGeneration {
		return ErrSubmissionConflict
	}
	if submission.State != target.State || submission.State != SubmissionInterruptedUnknown {
		return ErrSubmissionState
	}
	if submission.TerminalEventRetained || submission.TerminalSequence != 0 {
		return ErrSubmissionState
	}
	return nil
}

func proveRetainedRetirementOwnerTerminated(identity processidentity.Identity) error {
	if err := identity.Validate(); err != nil {
		return ErrOwnerMismatch
	}
	terminated, err := processidentity.OwnedSessionTerminated(identity)
	if err != nil || !terminated {
		return ErrOwnerMismatch
	}
	return nil
}

// confirmRetainedRetirement re-reads every record and the descriptor so the
// caller learns the committed state rather than an in-memory projection.
func confirmRetainedRetirement(
	ctx context.Context,
	retained *Journal,
	request RetainedRetirementRequest,
	before []Submission,
) (RecoveryDescriptor, []Submission, error) {
	submissions := make([]Submission, 0, len(before))
	for _, original := range before {
		stored, err := retained.GetSubmission(ctx, original.ID)
		if err != nil {
			return RecoveryDescriptor{}, nil, err
		}
		if !retainedRetirementPreserved(original, stored) {
			return RecoveryDescriptor{}, nil, ErrSubmissionConflict
		}
		stored.Payload = nil
		submissions = append(submissions, stored)
	}
	descriptor, err := retained.RecoveryDescriptor(ctx, request.SessionID, request.IncarnationID, request.HarnessGeneration, "")
	if err != nil {
		return RecoveryDescriptor{}, nil, err
	}
	if descriptor.SubmissionsTruncated || descriptor.SubmissionCount != 0 || len(descriptor.Submissions) != 0 {
		return RecoveryDescriptor{}, nil, ErrSubmissionConflict
	}
	return descriptor, submissions, nil
}

func retainedRetirementPreserved(before, after Submission) bool {
	return after.Retired && after.State == before.State && after.StreamID == before.StreamID &&
		after.Hash == before.Hash && after.HarnessGeneration == before.HarnessGeneration &&
		bytes.Equal(after.Payload, before.Payload)
}
