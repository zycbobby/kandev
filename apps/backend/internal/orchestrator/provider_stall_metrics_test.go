package orchestrator

import (
	"context"
	"errors"
	"expvar"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/agent/runtime/lifecycle"
	"github.com/kandev/kandev/internal/task/models"
)

// providerStallSettlementKey builds the expvar map key of one settlement
// outcome; reason is empty for every outcome except blocked.
func providerStallSettlementKey(outcome, reason string) string {
	if reason == "" {
		return officeStallLabel("outcome", outcome)
	}
	return officeStallLabel("outcome", outcome, "reason", reason)
}

// providerStallSettlementCount reads the accumulated count of one settlement
// outcome key.
func providerStallSettlementCount(outcome, reason string) int64 {
	return providerStallExpvarInt(providerStallSettlementTotal, providerStallSettlementKey(outcome, reason))
}

// providerStallAutoResumeCount reads the accumulated count of one auto-resume
// outcome key.
func providerStallAutoResumeCount(outcome string) int64 {
	return providerStallExpvarInt(providerStallAutoResumeTotal, officeStallLabel("outcome", outcome))
}

func providerStallExpvarInt(m *expvar.Map, key string) int64 {
	iv, ok := m.Get(key).(*expvar.Int)
	if !ok {
		return 0
	}
	return iv.Value()
}

// providerStallMapKeys snapshots every key the map has accumulated. The
// counters are process-global, so tests assert on deltas and on the grammar of
// the accumulated key set rather than on absolute values.
func providerStallMapKeys(m *expvar.Map) []string {
	keys := make([]string, 0, 8)
	m.Do(func(kv expvar.KeyValue) { keys = append(keys, kv.Key) })
	return keys
}

// TestNeverStartedStallAutoResumeOnce pins the one-launch budget of the
// settlement's auto-resume: an eligible recovered session launches exactly
// once, and a duplicate stall event delivered while the settlement is still
// in flight is deduped and produces neither a second launch nor a second
// auto-resume attempt.
func TestNeverStartedStallAutoResumeOnce(t *testing.T) {
	t.Run("eligible session launches once", func(t *testing.T) {
		f := newNeverStartedStallFixture(t, "resume-once", true)
		attemptedBefore := providerStallAutoResumeCount(providerStallResumeAttempted)
		succeededBefore := providerStallAutoResumeCount(providerStallResumeSucceeded)

		f.svc.handleAgentStalled(f.ctx, f.payload)
		f.awaitSettlementDrained(t)

		f.requireOneLaunch(t)
		require.Equal(t, int64(1),
			providerStallAutoResumeCount(providerStallResumeAttempted)-attemptedBefore,
			"one settlement must attempt exactly one auto-resume")
		require.Equal(t, int64(1),
			providerStallAutoResumeCount(providerStallResumeSucceeded)-succeededBefore)
	})

	t.Run("duplicate settlement does not launch again", func(t *testing.T) {
		f := newNeverStartedStallFixture(t, "resume-dup", true)
		attemptedBefore := providerStallAutoResumeCount(providerStallResumeAttempted)
		retireEntered := make(chan struct{})
		releaseRetirement := make(chan struct{})
		var retireEnteredOnce sync.Once
		f.manager.onRetire = func(*lifecycle.NeverStartedDeliveryRetirementRequest) {
			retireEnteredOnce.Do(func() { close(retireEntered) })
			<-releaseRetirement
		}

		f.svc.handleAgentStalled(f.ctx, f.payload)
		select {
		case <-retireEntered:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for the first settlement to reach retirement")
		}
		// The duplicate arrives while the settlement goroutine is still blocked
		// in retirement, so only the in-memory dedupe can suppress it.
		f.svc.handleAgentStalled(f.ctx, f.payload)
		close(releaseRetirement)
		f.awaitSettlementDrained(t)

		f.requireOneLaunch(t)
		require.Equal(t, int64(1),
			providerStallAutoResumeCount(providerStallResumeAttempted)-attemptedBefore,
			"a deduped duplicate settlement must not attempt a second auto-resume")
	})
}

// TestNeverStartedStallAutoResumeSkipped pins the auto-resume guard matrix: an
// archived task, an Office- or automation-owned task, a passthrough session,
// and a task whose passive auto-resume eligibility is refused (a coordinator
// task is message-only, so auto-start is disabled for it) all skip the launch
// and record the skip. The guards are defense in depth: settlement
// eligibility already excludes these states, so the helper is invoked
// directly to stage them.
func TestNeverStartedStallAutoResumeSkipped(t *testing.T) {
	mutators := map[string]func(t *testing.T, f *neverStartedStallFixture){
		"auto-start disabled (coordinator task)": func(t *testing.T, f *neverStartedStallFixture) {
			_, err := f.repo.DB().ExecContext(f.ctx,
				`UPDATE tasks SET origin = ? WHERE id = ?`, models.TaskOriginCoordinator, f.taskID)
			require.NoError(t, err)
		},
		"archived task": func(t *testing.T, f *neverStartedStallFixture) {
			_, err := f.repo.DB().ExecContext(f.ctx,
				`UPDATE tasks SET archived_at = ? WHERE id = ?`, time.Now().UTC(), f.taskID)
			require.NoError(t, err)
		},
		"office task": func(t *testing.T, f *neverStartedStallFixture) {
			_, err := f.repo.DB().ExecContext(f.ctx,
				`UPDATE tasks SET project_id = ? WHERE id = ?`, "office-project", f.taskID)
			require.NoError(t, err)
		},
		"automation task": func(t *testing.T, f *neverStartedStallFixture) {
			_, err := f.repo.DB().ExecContext(f.ctx,
				`UPDATE tasks SET origin = ? WHERE id = ?`, models.TaskOriginAutomationRun, f.taskID)
			require.NoError(t, err)
		},
		"passthrough session": func(t *testing.T, f *neverStartedStallFixture) {
			session := f.requireSession(t)
			session.IsPassthrough = true
			require.NoError(t, f.repo.UpdateTaskSession(f.ctx, session))
		},
	}
	for name, mutate := range mutators {
		t.Run(name, func(t *testing.T) {
			f := newNeverStartedStallFixture(t, "resume-skip", true)
			mutate(t, f)
			skippedBefore := providerStallAutoResumeCount(providerStallResumeSkipped)
			attemptedBefore := providerStallAutoResumeCount(providerStallResumeAttempted)

			f.svc.attemptNeverStartedStallAutoResume(f.ctx, f.taskID, f.sessionID)

			f.requireNoLaunch(t)
			require.Equal(t, int64(1),
				providerStallAutoResumeCount(providerStallResumeSkipped)-skippedBefore)
			require.Equal(t, int64(0),
				providerStallAutoResumeCount(providerStallResumeAttempted)-attemptedBefore,
				"a skipped auto-resume must not record an attempt")
		})
	}
}

// TestProviderStallMetricsBoundedLabels drives every settlement and
// auto-resume outcome at least once, then proves the accumulated expvar key
// sets stay within their closed outcome/reason sets and never embed a task,
// session, execution, submission, or stream identifier.
func TestProviderStallMetricsBoundedLabels(t *testing.T) {
	var identifiers []string
	remember := func(f *neverStartedStallFixture) {
		identifiers = append(identifiers,
			f.taskID, f.sessionID, f.executionID, f.submissionID, f.incarnationID, f.payload.DeliveryStreamID)
	}

	// settled + attempted + succeeded: one full recoverable settlement.
	settled := newNeverStartedStallFixture(t, "bounded-settled", true)
	remember(settled)
	beforeSettled := providerStallSettlementCount(providerStallOutcomeSettled, "")
	beforeAttempted := providerStallAutoResumeCount(providerStallResumeAttempted)
	beforeSucceeded := providerStallAutoResumeCount(providerStallResumeSucceeded)
	settled.svc.handleAgentStalled(settled.ctx, settled.payload)
	settled.awaitSettlementDrained(t)
	settled.requireOneLaunch(t)
	require.Equal(t, int64(1), providerStallSettlementCount(providerStallOutcomeSettled, "")-beforeSettled)
	require.Equal(t, int64(1), providerStallAutoResumeCount(providerStallResumeAttempted)-beforeAttempted)
	require.Equal(t, int64(1), providerStallAutoResumeCount(providerStallResumeSucceeded)-beforeSucceeded)

	// already_settled: a replayed settlement converges on the committed effect
	// and does not attempt another auto-resume.
	beforeAlready := providerStallSettlementCount(providerStallOutcomeAlreadySettled, "")
	attemptedAfterFirst := providerStallAutoResumeCount(providerStallResumeAttempted)
	outcome := settled.svc.settleNeverStartedStall(
		settled.ctx, settled.payload, settled.payload.OriginalRuntime, settled.turnID)
	require.True(t, outcome.settled, "a replayed settlement must still report settled")
	require.Equal(t, int64(1),
		providerStallSettlementCount(providerStallOutcomeAlreadySettled, "")-beforeAlready)
	require.Equal(t, int64(0),
		providerStallAutoResumeCount(providerStallResumeAttempted)-attemptedAfterFirst,
		"a replayed settlement must not attempt another auto-resume")

	// blocked / journal_conflict: retirement evidence conflicts.
	conflict := newNeverStartedStallFixture(t, "bounded-conflict", false)
	remember(conflict)
	conflict.manager.retireErr = errors.Join(
		lifecycle.ErrDeliveryRecordEvidenceConflict, lifecycle.ErrDeliveryRecoveryBlocked)
	beforeConflict := providerStallSettlementCount(providerStallOutcomeBlocked, providerStallBlockedJournalConflict)
	conflict.svc.handleAgentStalled(conflict.ctx, conflict.payload)
	conflict.awaitSettlementDrained(t)
	require.Equal(t, int64(1),
		providerStallSettlementCount(providerStallOutcomeBlocked, providerStallBlockedJournalConflict)-beforeConflict)

	// blocked / identity_missing: the SQL submission row is gone, so the
	// settlement cannot validate the delivery identity.
	missing := newNeverStartedStallFixture(t, "bounded-missing", false)
	remember(missing)
	_, err := missing.repo.DB().ExecContext(missing.ctx,
		`DELETE FROM agent_delivery_submissions WHERE id = ?`, missing.submissionID)
	require.NoError(t, err)
	beforeMissing := providerStallSettlementCount(providerStallOutcomeBlocked, providerStallBlockedIdentityMissing)
	missing.svc.handleAgentStalled(missing.ctx, missing.payload)
	missing.awaitSettlementDrained(t)
	require.Equal(t, int64(1),
		providerStallSettlementCount(providerStallOutcomeBlocked, providerStallBlockedIdentityMissing)-beforeMissing)

	// blocked / sql_failed: a failed-state row without the settlement effect
	// conflicts on every attempt, so the SQL settlement never commits.
	sqlFailed := newNeverStartedStallFixture(t, "bounded-sql", false)
	remember(sqlFailed)
	_, err = sqlFailed.repo.DB().ExecContext(sqlFailed.ctx,
		`UPDATE agent_delivery_submissions SET state = ?, outcome = ? WHERE id = ?`,
		models.DeliverySubmissionFailed, string(models.DeliverySubmissionFailed), sqlFailed.submissionID)
	require.NoError(t, err)
	beforeSQL := providerStallSettlementCount(providerStallOutcomeBlocked, providerStallBlockedSQLFailed)
	sqlFailed.svc.handleAgentStalled(sqlFailed.ctx, sqlFailed.payload)
	sqlFailed.awaitSettlementDrained(t)
	require.Equal(t, int64(1),
		providerStallSettlementCount(providerStallOutcomeBlocked, providerStallBlockedSQLFailed)-beforeSQL)

	// partial_output: the advisory branch records its own outcome once.
	advisory := newNeverStartedStallFixture(t, "bounded-advisory", false)
	remember(advisory)
	advisoryPayload := advisory.payload
	advisoryPayload.NeverStarted = false
	beforePartial := providerStallSettlementCount(providerStallOutcomePartialOutput, "")
	advisory.svc.handleAgentStalled(advisory.ctx, advisoryPayload)
	require.Equal(t, int64(1),
		providerStallSettlementCount(providerStallOutcomePartialOutput, "")-beforePartial)

	// skipped: the auto-resume guards refuse without launching.
	skipped := newNeverStartedStallFixture(t, "bounded-skipped", true)
	remember(skipped)
	_, err = skipped.repo.DB().ExecContext(skipped.ctx,
		`UPDATE tasks SET archived_at = ? WHERE id = ?`, time.Now().UTC(), skipped.taskID)
	require.NoError(t, err)
	beforeSkipped := providerStallAutoResumeCount(providerStallResumeSkipped)
	skipped.svc.attemptNeverStartedStallAutoResume(skipped.ctx, skipped.taskID, skipped.sessionID)
	skipped.requireNoLaunch(t)
	require.Equal(t, int64(1), providerStallAutoResumeCount(providerStallResumeSkipped)-beforeSkipped)

	// failed: an eligible resume whose launch cannot complete records failed.
	failed := newNeverStartedStallFixture(t, "bounded-failed", false)
	remember(failed)
	beforeFailed := providerStallAutoResumeCount(providerStallResumeFailed)
	failed.svc.handleAgentStalled(failed.ctx, failed.payload)
	failed.awaitSettlementDrained(t)
	failed.requireNoLaunch(t)
	require.Equal(t, int64(1), providerStallAutoResumeCount(providerStallResumeFailed)-beforeFailed)

	assertProviderStallClosedKeySets(t, identifiers)
}

// TestProviderStallBlockedReasonClassification pins the closed-reason mapping
// for journal retirement and SQL settlement failures: an arbitrary error never
// produces a reason outside the closed set.
func TestProviderStallBlockedReasonClassification(t *testing.T) {
	t.Run("journal retirement errors", func(t *testing.T) {
		cases := []struct {
			name string
			err  error
			want string
		}{
			{
				name: "owner mismatch means owner alive",
				err:  errors.Join(lifecycle.ErrDeliveryOwnerMismatch, lifecycle.ErrDeliveryRecoveryBlocked),
				want: providerStallBlockedOwnerAlive,
			},
			{
				name: "evidence conflict",
				err:  errors.Join(lifecycle.ErrDeliveryRecordEvidenceConflict, lifecycle.ErrDeliveryRecoveryBlocked),
				want: providerStallBlockedJournalConflict,
			},
			{
				name: "evidence ambiguous",
				err:  errors.Join(lifecycle.ErrDeliveryRecordEvidenceAmbiguous, lifecycle.ErrDeliveryRecoveryBlocked),
				want: providerStallBlockedJournalConflict,
			},
			{
				name: "bare recovery blocked is journal unavailable",
				err:  lifecycle.ErrDeliveryRecoveryBlocked,
				want: providerStallBlockedJournalUnavailable,
			},
			{
				name: "journal io failure",
				err:  errors.New("journal read failed"),
				want: providerStallBlockedJournalUnavailable,
			},
			{
				name: "settlement deadline",
				err:  fmt.Errorf("retire: %w", context.DeadlineExceeded),
				want: providerStallBlockedSettlementTimeout,
			},
		}
		for _, tc := range cases {
			if got := providerStallRetirementBlockedReason(tc.err); got != tc.want {
				t.Errorf("%s: reason = %q, want %q", tc.name, got, tc.want)
			}
		}
	})

	t.Run("sql settlement errors", func(t *testing.T) {
		if got := providerStallSQLBlockedReason(context.DeadlineExceeded); got != providerStallBlockedSettlementTimeout {
			t.Errorf("deadline: reason = %q, want %q", got, providerStallBlockedSettlementTimeout)
		}
		if got := providerStallSQLBlockedReason(errors.New("write failed")); got != providerStallBlockedSQLFailed {
			t.Errorf("storage failure: reason = %q, want %q", got, providerStallBlockedSQLFailed)
		}
	})
}

var providerStallSettlementOutcomes = map[string]bool{
	providerStallOutcomeSettled:        true,
	providerStallOutcomeAlreadySettled: true,
	providerStallOutcomeBlocked:        true,
	providerStallOutcomePartialOutput:  true,
}

var providerStallBlockedReasons = map[string]bool{
	providerStallBlockedIdentityMissing:    true,
	providerStallBlockedOwnerAlive:         true,
	providerStallBlockedJournalConflict:    true,
	providerStallBlockedJournalUnavailable: true,
	providerStallBlockedSQLFailed:          true,
	providerStallBlockedSettlementTimeout:  true,
}

var providerStallAutoResumeOutcomes = map[string]bool{
	providerStallResumeAttempted: true,
	providerStallResumeSucceeded: true,
	providerStallResumeFailed:    true,
	providerStallResumeSkipped:   true,
}

// assertProviderStallClosedKeySets proves every accumulated key of both
// counter maps decomposes into the closed label grammar and embeds no
// identifier of any fixture the process has driven.
func assertProviderStallClosedKeySets(t *testing.T, identifiers []string) {
	t.Helper()
	for _, key := range providerStallMapKeys(providerStallSettlementTotal) {
		outcome, reason, ok := parseProviderStallSettlementKey(key)
		require.True(t, ok, "settlement key %q is not outcome=v[;reason=v]", key)
		require.Contains(t, providerStallSettlementOutcomes, outcome,
			"settlement key %q carries an outcome outside the closed set", key)
		if outcome == providerStallOutcomeBlocked {
			require.Contains(t, providerStallBlockedReasons, reason,
				"blocked key %q carries a reason outside the closed set", key)
		} else {
			require.Empty(t, reason, "non-blocked key %q must not carry a reason", key)
		}
		requireNoProviderStallIdentifier(t, key, identifiers)
	}
	for _, key := range providerStallMapKeys(providerStallAutoResumeTotal) {
		outcome, reason, ok := parseProviderStallSettlementKey(key)
		require.True(t, ok, "auto-resume key %q is not outcome=v", key)
		require.Contains(t, providerStallAutoResumeOutcomes, outcome,
			"auto-resume key %q carries an outcome outside the closed set", key)
		require.Empty(t, reason, "auto-resume key %q must not carry a reason", key)
		requireNoProviderStallIdentifier(t, key, identifiers)
	}
}

func requireNoProviderStallIdentifier(t *testing.T, key string, identifiers []string) {
	t.Helper()
	for _, id := range identifiers {
		if id == "" {
			continue
		}
		require.NotContains(t, key, id, "metric key %q must not embed the identifier %q", key, id)
	}
}

// parseProviderStallSettlementKey splits an "outcome=v[;reason=v]" map key
// into its labels. Deliberately independent of officeStallLabel so the label
// grammar is verified, not repeated.
func parseProviderStallSettlementKey(key string) (outcome, reason string, ok bool) {
	parts := strings.Split(key, ";")
	if len(parts) > 2 {
		return "", "", false
	}
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return "", "", false
		}
		switch kv[0] {
		case "outcome":
			outcome = kv[1]
		case "reason":
			reason = kv[1]
		default:
			return "", "", false
		}
	}
	return outcome, reason, outcome != ""
}
