package orchestrator

import (
	"context"
	"errors"
	"expvar"

	agentruntime "github.com/kandev/kandev/internal/agent/runtime"
)

// expvar maps for the provider-stall settlement surface, exposed via the
// stdlib /debug/vars handler. The label model is "key=value;key=value..." (the
// shared officeStallLabel) so a Prometheus translation layer can split on `;`
// and `=` later, mirroring office_stall_metrics.go.
//
// Counters only: they record terminal settlement and auto-resume events, not
// in-flight state. All label sets are closed; no task, session, execution,
// submission, or payload identity is ever a label.
var (
	// providerStallSettlementTotal counts the terminal outcome of one
	// never-started stall settlement, plus the advisory partial-output branch
	// that keeps the legacy non-terminal handling. The reason label is set
	// only for blocked outcomes.
	providerStallSettlementTotal = expvar.NewMap("provider_stall_settlement_total")

	// providerStallAutoResumeTotal counts the settlement's single auto-resume
	// attempt by outcome.
	providerStallAutoResumeTotal = expvar.NewMap("provider_stall_auto_resume_total")
)

// Label keys shared by both maps.
const (
	providerStallLabelOutcome = "outcome"
	providerStallLabelReason  = "reason"
)

// Outcomes for providerStallSettlementTotal.
const (
	providerStallOutcomeSettled        = "settled"
	providerStallOutcomeAlreadySettled = "already_settled"
	providerStallOutcomeBlocked        = "blocked"
	providerStallOutcomePartialOutput  = "partial_output"
)

// Blocked reasons for providerStallSettlementTotal. Closed set.
const (
	// providerStallBlockedIdentityMissing covers the settlement's identity
	// validation: the delivery identity cannot be observed (a missing seam or
	// an unreadable row) or the observed row does not match the stall payload.
	providerStallBlockedIdentityMissing = "identity_missing"
	// providerStallBlockedOwnerAlive: retirement proved the delivery owner is
	// still live or no longer matches the stall's owner.
	providerStallBlockedOwnerAlive = "owner_alive"
	// providerStallBlockedJournalConflict: the retained journal's evidence
	// conflicts with, or is ambiguous about, the stall identity.
	providerStallBlockedJournalConflict = "journal_conflict"
	// providerStallBlockedJournalUnavailable: every other journal retirement
	// failure, including a bare recovery-blocked refusal and journal I/O.
	providerStallBlockedJournalUnavailable = "journal_unavailable"
	// providerStallBlockedSQLFailed: the SQL settlement did not commit.
	providerStallBlockedSQLFailed = "sql_failed"
	// providerStallBlockedSettlementTimeout: the bounded settlement window
	// expired before journal retirement or SQL settlement completed.
	providerStallBlockedSettlementTimeout = "settlement_timeout"
)

// Outcomes for providerStallAutoResumeTotal.
const (
	providerStallResumeAttempted = "attempted"
	providerStallResumeSucceeded = "succeeded"
	providerStallResumeFailed    = "failed"
	providerStallResumeSkipped   = "skipped"
)

// providerStallSettled records one committed settlement outcome; a replay
// that converged on an already-committed effect is distinguished so a
// duplicate settlement is visible without a second journal write.
func providerStallSettled(alreadySettled bool) {
	outcome := providerStallOutcomeSettled
	if alreadySettled {
		outcome = providerStallOutcomeAlreadySettled
	}
	providerStallSettlementTotal.Add(officeStallLabel(providerStallLabelOutcome, outcome), 1)
}

// providerStallBlocked records one fail-closed settlement outcome with its
// classified reason.
func providerStallBlocked(reason string) {
	providerStallSettlementTotal.Add(officeStallLabel(
		providerStallLabelOutcome, providerStallOutcomeBlocked,
		providerStallLabelReason, reason), 1)
}

// providerStallPartialOutput records one advisory partial-output stall: the
// session stays non-terminal and the user keeps the recovery affordance.
func providerStallPartialOutput() {
	providerStallSettlementTotal.Add(
		officeStallLabel(providerStallLabelOutcome, providerStallOutcomePartialOutput), 1)
}

// providerStallAutoResume records the settlement's single auto-resume attempt
// by outcome.
func providerStallAutoResume(outcome string) {
	providerStallAutoResumeTotal.Add(officeStallLabel(providerStallLabelOutcome, outcome), 1)
}

// providerStallRetirementBlockedReason classifies a journal retirement error
// into the closed blocked-reason set: the bounded settlement window expiring,
// a still-live or mismatched owner, conflicting or ambiguous journal
// evidence, and everything else the journal could not read or write.
func providerStallRetirementBlockedReason(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return providerStallBlockedSettlementTimeout
	case errors.Is(err, agentruntime.ErrDeliveryOwnerMismatch):
		return providerStallBlockedOwnerAlive
	case errors.Is(err, agentruntime.ErrDeliveryRecordEvidenceConflict),
		errors.Is(err, agentruntime.ErrDeliveryRecordEvidenceAmbiguous):
		return providerStallBlockedJournalConflict
	default:
		return providerStallBlockedJournalUnavailable
	}
}

// providerStallSQLBlockedReason classifies a SQL settlement failure: the
// bounded settlement window expiring is distinguished from every other
// storage failure.
func providerStallSQLBlockedReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return providerStallBlockedSettlementTimeout
	}
	return providerStallBlockedSQLFailed
}
