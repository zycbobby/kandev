package orchestrator

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/task/models"
	v1 "github.com/kandev/kandev/pkg/api/v1"
)

// TestHandleAgentStalled_NeverStartedResolvesSubmissionFromDurableRows covers
// the cancel-and-replace gap: the execution's delivery binding was cleared by
// the cancel and the replacement prompt's dispatch failed before it could
// re-bind, so the stall event arrives with an empty DeliverySubmissionID.
// The durable-row resolution must find the single admitted-but-undelivered
// submission for the session, incarnation, and generation, and run the full
// settlement with that identity.
func TestHandleAgentStalled_NeverStartedResolvesSubmissionFromDurableRows(t *testing.T) {
	f := newNeverStartedStallFixture(t, "resolves", true)
	f.payload.DeliverySubmissionID = ""

	f.svc.handleAgentStalled(f.ctx, f.payload)
	f.awaitSettlementDrained(t)

	require.Equal(t, 1, f.manager.retireRequestCount(), "retirement must run exactly once")
	request := f.manager.firstRetireRequest()
	require.Equal(t, f.submissionID, request.SubmissionID,
		"the resolved submission must be the durable interrupted_unknown row")

	session := f.requireSession(t)
	require.Equal(t, models.TaskSessionStateWaitingForInput, session.State)

	f.taskRepo.mu.Lock()
	defer f.taskRepo.mu.Unlock()
	for _, state := range f.taskRepo.stateHistory[f.taskID] {
		require.NotEqual(t, v1.TaskStateFailed, state,
			"task must never transition to FAILED for a settled stall")
	}
}

// TestHandleAgentStalled_NeverStartedDurableIdentityAmbiguousKeepsLegacy
// covers the ambiguous case: two non-terminal submissions on the same
// incarnation and generation cannot be disambiguated from durable rows, so
// the legacy terminal handling stays and no auto-resume fires.
func TestHandleAgentStalled_NeverStartedDurableIdentityAmbiguousKeepsLegacy(t *testing.T) {
	f := newNeverStartedStallFixture(t, "ambiguous", false)
	f.payload.DeliverySubmissionID = ""

	now := time.Now().UTC()
	inserted, err := f.repo.PrepareAgentDeliverySubmission(f.ctx, &models.AgentDeliverySubmission{
		ID: "prompt:ns-stall-ambiguous-second", SessionID: f.sessionID,
		IncarnationID: f.incarnationID, HarnessGeneration: neverStartedStallGeneration,
		OwnerGeneration: neverStartedStallGeneration, DispatchAttemptID: "ns-stall-dispatch-second",
		PayloadHash: "ns-stall-hash-second", Payload: []byte(`{"text":"second"}`),
		State: models.DeliverySubmissionDispatching, Outcome: "dispatching",
		CreatedAt: now, UpdatedAt: now,
	})
	require.NoError(t, err)
	require.True(t, inserted)

	f.svc.handleAgentStalled(f.ctx, f.payload)
	f.awaitSettlementDrained(t)

	require.Zero(t, f.manager.retireRequestCount(), "ambiguous identity must not retire anything")

	session := f.requireSession(t)
	require.Equal(t, models.TaskSessionStateFailed, session.State)
	require.Contains(t, session.ErrorMessage, "agent produced no output since start")
	f.requireNoLaunch(t)
}
