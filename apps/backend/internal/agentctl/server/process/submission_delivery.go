package process

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/kandev/kandev/internal/agentctl/journal"
)

var (
	ErrSubmissionUncertain = errors.New("agent delivery submission outcome is uncertain")
	// ErrDeliveryAdmissionRejected means the submission was definitively not
	// written to the journal, so the backend may settle it without recovery.
	ErrDeliveryAdmissionRejected = errors.New("durable delivery admission was rejected before journal intake")
	// ErrDeliveryAdmissionUncertain means the submission may already be in the
	// journal. The backend must keep the existing recovery path.
	ErrDeliveryAdmissionUncertain = errors.New("durable delivery admission outcome is uncertain after journal intake")
)

// submissionJournal is the narrow durable store used by admission. Keeping it
// small lets tests force pre-write and post-write failures deterministically.
type submissionJournal interface {
	PutSubmission(context.Context, journal.Submission) (journal.Submission, error)
	TransitionSubmission(context.Context, string, journal.SubmissionState, time.Time) (journal.Submission, error)
	GetSubmission(context.Context, string) (journal.Submission, error)
}

// DeliveryAdmissionRejectionReason maps a rejected admission to the bounded
// diagnostic reason set. It never carries storage paths or payload data.
func DeliveryAdmissionRejectionReason(err error) string {
	switch {
	case errors.Is(err, errDeliveryPressure):
		return "storage_pressure"
	case errors.Is(err, journal.ErrJournalFull), errors.Is(err, journal.ErrStreamFull):
		return "journal_full"
	case errors.Is(err, journal.ErrJournalCorrupt):
		return "journal_corrupt"
	case errors.Is(err, journal.ErrOwnerMismatch):
		return "owner_mismatch"
	default:
		return "unknown"
	}
}

type deterministicPromptFailure interface {
	DeterministicPromptFailure() bool
}

func isKnownSubmissionFailure(err error) bool {
	var requestErr *acp.RequestError
	if errors.As(err, &requestErr) {
		return true
	}
	return isDeterministicProviderFailure(err)
}

func isDeterministicProviderFailure(err error) bool {
	var providerFailure deterministicPromptFailure
	return errors.As(err, &providerFailure) && providerFailure.DeterministicPromptFailure()
}

// SubmissionDelivery coordinates the agentctl-side immutable submission
// record. It is deliberately independent of the ACP prompt call so a caller
// can reconcile durable state before deciding whether a harness call is safe.
type SubmissionDelivery struct {
	Journal  submissionJournal
	Now      func() time.Time
	Stopping *atomic.Bool
}

func (d *SubmissionDelivery) Admit(ctx context.Context, submission journal.Submission) (journal.Submission, error) {
	if d == nil || d.Journal == nil {
		return journal.Submission{}, fmt.Errorf("submission journal is required")
	}
	if submission.State == "" {
		submission.State = journal.SubmissionPrepared
	}
	stored, err := d.Journal.PutSubmission(ctx, submission)
	if err != nil {
		return journal.Submission{}, fmt.Errorf("%w: %w", ErrDeliveryAdmissionRejected, err)
	}
	if stored.State == journal.SubmissionPrepared {
		stored, err = d.Journal.TransitionSubmission(ctx, stored.ID, journal.SubmissionAccepted, d.now())
		if err != nil {
			return journal.Submission{}, fmt.Errorf("%w: %w", ErrDeliveryAdmissionUncertain, err)
		}
	}
	journal.RecordSubmission(string(stored.State))
	return stored, nil
}

// Dispatch moves accepted to dispatching before invoking the harness. An
// existing dispatching record is never retried automatically because the
// external call may already have happened.
func (d *SubmissionDelivery) Dispatch(ctx context.Context, id string, call func(context.Context) error) (journal.Submission, error) {
	if d == nil || d.Journal == nil || call == nil {
		return journal.Submission{}, fmt.Errorf("submission journal and dispatch callback are required")
	}
	submission, shouldDispatch, err := d.prepareDispatch(ctx, id)
	if err != nil {
		return journal.Submission{}, err
	}
	if !shouldDispatch {
		return submission, nil
	}
	if err := call(ctx); err != nil {
		return d.handleDispatchError(ctx, id, err)
	}
	completed, err := d.Journal.TransitionSubmission(ctx, id, journal.SubmissionCompleted, d.now())
	if err != nil {
		return journal.Submission{}, err
	}
	return completed, nil
}

func (d *SubmissionDelivery) prepareDispatch(ctx context.Context, id string) (journal.Submission, bool, error) {
	submission, err := d.Journal.GetSubmission(ctx, id)
	if err != nil {
		return journal.Submission{}, false, err
	}
	if submission.Retired {
		journal.RecordUncertainSubmission("retired_submission")
		return submission, false, ErrSubmissionUncertain
	}
	switch submission.State {
	case journal.SubmissionCompleted, journal.SubmissionFailed, journal.SubmissionCancelled:
		return submission, false, nil
	case journal.SubmissionInterruptedUnknown, journal.SubmissionDispatching:
		journal.RecordUncertainSubmission("preexisting_uncertain")
		return submission, false, ErrSubmissionUncertain
	case journal.SubmissionAccepted:
		_, err := d.Journal.TransitionSubmission(ctx, id, journal.SubmissionDispatching, d.now())
		return submission, true, err
	default:
		return submission, false, fmt.Errorf("submission %s is not accepted: %s", id, submission.State)
	}
}

func (d *SubmissionDelivery) handleDispatchError(ctx context.Context, id string, dispatchErr error) (journal.Submission, error) {
	interrupted := ctx.Err() != nil
	if d.Stopping != nil && d.Stopping.Load() {
		interrupted = true
	}
	// Settling an external call survives cancellation of the request that made it.
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	submission, err := d.Journal.GetSubmission(settleCtx, id)
	if err != nil {
		return journal.Submission{}, errors.Join(ErrSubmissionUncertain, err)
	}
	if submission.State == journal.SubmissionCompleted && submission.TerminalEventRetained {
		return submission, nil
	}
	if submission.TerminalEventRetained && (submission.State == journal.SubmissionFailed || submission.State == journal.SubmissionCancelled) {
		return submission, dispatchErr
	}
	if isDeterministicProviderFailure(dispatchErr) || (!interrupted && isKnownSubmissionFailure(dispatchErr)) {
		failed, transitionErr := d.Journal.TransitionSubmission(settleCtx, id, journal.SubmissionFailed, d.now())
		if transitionErr != nil {
			return journal.Submission{}, errors.Join(ErrSubmissionUncertain, transitionErr)
		}
		return failed, dispatchErr
	}
	unknown, transitionErr := d.Journal.TransitionSubmission(settleCtx, id, journal.SubmissionInterruptedUnknown, d.now())
	journal.RecordUncertainSubmission("dispatch_error")
	if transitionErr != nil {
		return journal.Submission{}, errors.Join(ErrSubmissionUncertain, transitionErr)
	}
	return unknown, fmt.Errorf("dispatch outcome is unknown: %w", dispatchErr)
}

func (d *SubmissionDelivery) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}
