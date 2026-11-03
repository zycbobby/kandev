package process

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agentctl/journal"
)

type fakeSubmissionJournal struct {
	submission    journal.Submission
	putErr        error
	transitionErr error
	puts          int
	transitions   []journal.SubmissionState
}

func (f *fakeSubmissionJournal) PutSubmission(_ context.Context, submission journal.Submission) (journal.Submission, error) {
	f.puts++
	if f.putErr != nil {
		return journal.Submission{}, f.putErr
	}
	if f.submission.ID == "" {
		submission.State = journal.SubmissionPrepared
		f.submission = submission
	}
	return f.submission, nil
}

func (f *fakeSubmissionJournal) TransitionSubmission(_ context.Context, _ string, next journal.SubmissionState, _ time.Time) (journal.Submission, error) {
	f.transitions = append(f.transitions, next)
	if f.transitionErr != nil {
		return journal.Submission{}, f.transitionErr
	}
	f.submission.State = next
	return f.submission, nil
}

func (f *fakeSubmissionJournal) GetSubmission(_ context.Context, _ string) (journal.Submission, error) {
	return f.submission, nil
}

func TestSubmissionDeliveryAdmitDistinguishesPreWriteRejection(t *testing.T) {
	fake := &fakeSubmissionJournal{putErr: fmt.Errorf("%w: test", journal.ErrJournalFull)}
	delivery := &SubmissionDelivery{Journal: fake}
	_, err := delivery.Admit(context.Background(), journal.Submission{ID: "submission", Hash: "hash"})
	if !errors.Is(err, ErrDeliveryAdmissionRejected) {
		t.Fatalf("pre-write rejection = %v, want ErrDeliveryAdmissionRejected", err)
	}
	if !errors.Is(err, journal.ErrJournalFull) {
		t.Fatalf("pre-write rejection lost the journal cause: %v", err)
	}
	if errors.Is(err, ErrDeliveryAdmissionUncertain) {
		t.Fatalf("pre-write rejection was classified as uncertain: %v", err)
	}
	if len(fake.transitions) != 0 {
		t.Fatalf("pre-write rejection attempted a transition: %v", fake.transitions)
	}
}

func TestSubmissionDeliveryAdmitDistinguishesPostWriteUncertainty(t *testing.T) {
	fake := &fakeSubmissionJournal{transitionErr: journal.ErrJournalCorrupt}
	delivery := &SubmissionDelivery{Journal: fake}
	_, err := delivery.Admit(context.Background(), journal.Submission{ID: "submission", Hash: "hash"})
	if !errors.Is(err, ErrDeliveryAdmissionUncertain) {
		t.Fatalf("post-write failure = %v, want ErrDeliveryAdmissionUncertain", err)
	}
	if errors.Is(err, ErrDeliveryAdmissionRejected) {
		t.Fatalf("post-write failure was classified as rejected: %v", err)
	}
	if fake.puts != 1 {
		t.Fatalf("post-write failure put %d submissions, want 1", fake.puts)
	}
}

func TestAdmitDeliverySubmissionReportsPressureAsRejection(t *testing.T) {
	m, j := pressureTestManager(t)
	fillPressureJournal(t, j)
	if err := m.refreshDeliveryPressure(context.Background(), j, ""); err != nil {
		t.Fatal(err)
	}
	_, err := m.AdmitDeliverySubmission(context.Background(), journal.Submission{ID: "next", Hash: "next"})
	if !errors.Is(err, ErrDeliveryAdmissionRejected) {
		t.Fatalf("pressure admission = %v, want ErrDeliveryAdmissionRejected", err)
	}
	if !errors.Is(err, errDeliveryPressure) {
		t.Fatalf("pressure admission lost the pressure cause: %v", err)
	}
	if reason := DeliveryAdmissionRejectionReason(err); reason != "storage_pressure" {
		t.Fatalf("pressure admission reason = %q", reason)
	}
}
