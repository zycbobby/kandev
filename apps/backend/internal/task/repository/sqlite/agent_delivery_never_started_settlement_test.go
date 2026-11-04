package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
	"github.com/stretchr/testify/require"
)

type agentDeliveryNeverStartedSettlementStore interface {
	SettleNeverStartedAgentDeliverySubmission(
		context.Context,
		*models.AgentDeliveryNeverStartedSettlementRequest,
	) (*models.AgentDeliveryNeverStartedSettlementResult, error)
}

var _ agentDeliveryNeverStartedSettlementStore = (*Repository)(nil)

type neverStartedSettlementFixture struct {
	repo           *Repository
	sessionID      string
	incarnationID  string
	submissionID   string
	blockID        string
	streamID       string
	settledAt      time.Time
	initialState   models.DeliverySubmissionState
	initialOutcome string
}

func newNeverStartedSettlementFixture(
	t *testing.T,
	state models.DeliverySubmissionState,
	outcome, streamID string,
) *neverStartedSettlementFixture {
	t.Helper()
	ctx := context.Background()
	const (
		taskID        = "task-never-started"
		sessionID     = "session-never-started"
		incarnationID = "incarnation-never-started"
		submissionID  = "prompt:send-now:never-started"
		blockID       = "block-never-started"
	)
	now := time.Date(2026, 11, 4, 14, 0, 0, 0, time.UTC)
	repo := newRepoForSessionTests(t)
	repo.clockNow = func() time.Time { return now }
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, Title: taskID}))
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: sessionID, TaskID: taskID, QueueIncarnationID: incarnationID,
		State: models.TaskSessionStateWaitingForInput,
	}))
	require.NoError(t, repo.CreateHarnessSessionGeneration(ctx, &models.HarnessSessionGeneration{
		SessionID: sessionID, IncarnationID: incarnationID, Generation: 4, CreationReason: "test",
	}))
	_, err := repo.PrepareAgentDeliverySubmission(ctx, &models.AgentDeliverySubmission{
		ID: submissionID, SessionID: sessionID, IncarnationID: incarnationID,
		HarnessGeneration: 4, OwnerGeneration: 4, DispatchAttemptID: "message-never-started",
		PayloadHash: "never-started-hash", Payload: []byte(`{"text":"never started"}`),
		State: state, Outcome: outcome,
		CreatedAt: now, UpdatedAt: now,
	})
	require.NoError(t, err)
	require.NoError(t, repo.UpsertSessionRecoveryBlock(ctx, &models.SessionRecoveryBlock{
		ID: blockID, SessionID: sessionID, IncarnationID: incarnationID,
		ExpectedGeneration: 4, Reason: "unknown_prompt_outcome", State: models.RecoveryBlockOpen,
		ConsumerReference: "agent_delivery", DeliverySubmissionID: submissionID,
		DeliveryStreamID: streamID,
	}))
	settledAt := now.Add(time.Minute)
	repo.clockNow = func() time.Time { return settledAt }
	return &neverStartedSettlementFixture{
		repo: repo, sessionID: sessionID, incarnationID: incarnationID,
		submissionID: submissionID, blockID: blockID, streamID: streamID,
		settledAt: settledAt, initialState: state, initialOutcome: outcome,
	}
}

func (f *neverStartedSettlementFixture) request() *models.AgentDeliveryNeverStartedSettlementRequest {
	return &models.AgentDeliveryNeverStartedSettlementRequest{
		SessionID: f.sessionID, IncarnationID: f.incarnationID, HarnessGeneration: 4,
		SubmissionID: f.submissionID, ExpectedState: f.initialState,
		ExpectedOutcome: f.initialOutcome, BlockID: f.blockID, SettledAt: f.settledAt,
	}
}

func (f *neverStartedSettlementFixture) requireSubmissionUncertain(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	submission, err := f.repo.GetAgentDeliverySubmission(ctx, f.submissionID)
	require.NoError(t, err)
	require.Equal(t, f.initialState, submission.State)
	require.Equal(t, f.initialOutcome, submission.Outcome)
	require.Zero(t, submission.TerminalSequence)
	require.Nil(t, submission.FullySettledAt)
	var effectCount int
	require.NoError(t, f.repo.db.GetContext(ctx, &effectCount,
		`SELECT COUNT(*) FROM agent_delivery_effects WHERE effect_type = ?`,
		models.DeliveryNeverStartedEffectType))
	require.Zero(t, effectCount)
	var blockState string
	require.NoError(t, f.repo.db.GetContext(ctx, &blockState,
		`SELECT state FROM session_recovery_blocks WHERE id = ?`, f.blockID))
	require.Equal(t, models.RecoveryBlockOpen, blockState)
}

func TestSettleNeverStartedFromDispatching(t *testing.T) {
	ctx := context.Background()
	fixture := newNeverStartedSettlementFixture(t, models.DeliverySubmissionDispatching, "dispatching", "")
	result, err := fixture.repo.SettleNeverStartedAgentDeliverySubmission(ctx, fixture.request())
	require.NoError(t, err)
	require.True(t, result.Settled)
	require.False(t, result.AlreadySettled)
	require.Equal(t, []string{fixture.blockID}, result.ResolvedBlockIDs)

	submission, err := fixture.repo.GetAgentDeliverySubmission(ctx, fixture.submissionID)
	require.NoError(t, err)
	require.Equal(t, models.DeliverySubmissionFailed, submission.State)
	require.Equal(t, "failed", submission.Outcome)
	require.Zero(t, submission.TerminalSequence)
	require.Nil(t, submission.FullySettledAt)
	require.True(t, submission.UpdatedAt.Equal(fixture.settledAt))

	require.Equal(t, "agent_delivery.never_started_settlement:"+fixture.submissionID,
		models.DeliveryNeverStartedEffectKey(fixture.submissionID))
	effect, err := fixture.repo.GetAgentDeliveryEffect(ctx, models.DeliveryNeverStartedEffectKey(fixture.submissionID))
	require.NoError(t, err)
	require.Equal(t, models.DeliveryNeverStartedEffectType, effect.EffectType)
	require.Equal(t, models.DeliveryEffectCompleted, effect.State)
	require.Empty(t, effect.StreamID)
	require.Zero(t, effect.Sequence)
	require.Equal(t, "failed", effect.Outcome)
	require.Equal(t, fixture.submissionID, effect.SubmissionID)
	require.NotNil(t, effect.CompletedAt)

	var terminalStreamID string
	var terminalSequence int64
	require.NoError(t, fixture.repo.db.QueryRowxContext(ctx, `
		SELECT terminal_stream_id, terminal_sequence FROM agent_delivery_submissions WHERE id = ?`,
		fixture.submissionID).Scan(&terminalStreamID, &terminalSequence))
	require.Empty(t, terminalStreamID, "a never-started settlement must not fabricate terminal evidence")
	require.Zero(t, terminalSequence)
	var eventCount int
	require.NoError(t, fixture.repo.db.GetContext(ctx, &eventCount,
		`SELECT COUNT(*) FROM agent_delivery_inbox WHERE session_id = ?`, fixture.sessionID))
	require.Zero(t, eventCount, "a never-started settlement must not write a terminal event")

	var state, outcome, authorizedAction string
	var resolvedAt *time.Time
	require.NoError(t, fixture.repo.db.QueryRowxContext(ctx, `
		SELECT state, delivery_outcome, authorized_action, resolved_at
		FROM session_recovery_blocks WHERE id = ?`, fixture.blockID).
		Scan(&state, &outcome, &authorizedAction, &resolvedAt))
	require.Equal(t, models.RecoveryBlockResolved, state)
	require.Equal(t, "failed", outcome)
	require.Equal(t, models.DeliveryNeverStartedAuthorizedAction, authorizedAction)
	require.NotNil(t, resolvedAt)

	var sessionUpdatedAt time.Time
	require.NoError(t, fixture.repo.db.GetContext(ctx, &sessionUpdatedAt,
		`SELECT updated_at FROM task_sessions WHERE id = ?`, fixture.sessionID))
	require.True(t, sessionUpdatedAt.Equal(fixture.settledAt),
		"resolving a block must advance the session recovery revision")
}

func TestSettleNeverStartedFromInterruptedUnknown(t *testing.T) {
	ctx := context.Background()
	fixture := newNeverStartedSettlementFixture(t,
		models.DeliverySubmissionInterruptedUnknown, "prompt_dispatch_failed", "stream-never-started")
	unrelated := []*models.SessionRecoveryBlock{
		{
			ID: "block-never-started-other-submission", SessionID: fixture.sessionID,
			IncarnationID: fixture.incarnationID, ExpectedGeneration: 4,
			Reason: "unknown_prompt_outcome_other", State: models.RecoveryBlockOpen,
			ConsumerReference: "agent_delivery", DeliverySubmissionID: "prompt:other",
			DeliveryStreamID: "stream-other",
		},
		{
			ID: "block-never-started-other-consumer", SessionID: fixture.sessionID,
			IncarnationID: fixture.incarnationID, ExpectedGeneration: 4,
			Reason: "workflow_step", State: models.RecoveryBlockOpen,
			ConsumerReference: "workflow",
		},
	}
	for _, block := range unrelated {
		require.NoError(t, fixture.repo.UpsertSessionRecoveryBlock(ctx, block))
	}

	result, err := fixture.repo.SettleNeverStartedAgentDeliverySubmission(ctx, fixture.request())
	require.NoError(t, err)
	require.True(t, result.Settled)
	require.False(t, result.AlreadySettled)
	require.Equal(t, []string{fixture.blockID}, result.ResolvedBlockIDs)

	submission, err := fixture.repo.GetAgentDeliverySubmission(ctx, fixture.submissionID)
	require.NoError(t, err)
	require.Equal(t, models.DeliverySubmissionFailed, submission.State)
	require.Equal(t, "failed", submission.Outcome)
	require.Zero(t, submission.TerminalSequence)
	require.Nil(t, submission.FullySettledAt)

	var state, streamID, outcome string
	require.NoError(t, fixture.repo.db.QueryRowxContext(ctx, `
		SELECT state, delivery_stream_id, delivery_outcome
		FROM session_recovery_blocks WHERE id = ?`, fixture.blockID).
		Scan(&state, &streamID, &outcome))
	require.Equal(t, models.RecoveryBlockResolved, state)
	require.Equal(t, fixture.streamID, streamID, "a stream-bound block keeps its binding as audit evidence")
	require.Equal(t, "failed", outcome)

	var openCount int
	require.NoError(t, fixture.repo.db.GetContext(ctx, &openCount, `
		SELECT COUNT(*) FROM session_recovery_blocks
		WHERE session_id = ? AND state = ?`, fixture.sessionID, models.RecoveryBlockOpen))
	require.Equal(t, len(unrelated), openCount, "unrelated consumers and blocks must stay untouched")
}

func TestSettleNeverStartedIdempotentReplay(t *testing.T) {
	ctx := context.Background()
	fixture := newNeverStartedSettlementFixture(t,
		models.DeliverySubmissionInterruptedUnknown, "prompt_dispatch_failed", "stream-never-started")
	_, err := fixture.repo.SettleNeverStartedAgentDeliverySubmission(ctx, fixture.request())
	require.NoError(t, err)
	var firstResolvedAt time.Time
	require.NoError(t, fixture.repo.db.GetContext(ctx, &firstResolvedAt,
		`SELECT resolved_at FROM session_recovery_blocks WHERE id = ?`, fixture.blockID))
	var firstSessionUpdatedAt time.Time
	require.NoError(t, fixture.repo.db.GetContext(ctx, &firstSessionUpdatedAt,
		`SELECT updated_at FROM task_sessions WHERE id = ?`, fixture.sessionID))

	fixture.repo.clockNow = func() time.Time { return fixture.settledAt.Add(time.Hour) }
	replayed, err := fixture.repo.SettleNeverStartedAgentDeliverySubmission(ctx, fixture.request())
	require.NoError(t, err)
	require.True(t, replayed.Settled)
	require.True(t, replayed.AlreadySettled)
	require.Empty(t, replayed.ResolvedBlockIDs)

	var effectCount int
	require.NoError(t, fixture.repo.db.GetContext(ctx, &effectCount,
		`SELECT COUNT(*) FROM agent_delivery_effects WHERE effect_type = ?`,
		models.DeliveryNeverStartedEffectType))
	require.Equal(t, 1, effectCount)
	var secondResolvedAt time.Time
	require.NoError(t, fixture.repo.db.GetContext(ctx, &secondResolvedAt,
		`SELECT resolved_at FROM session_recovery_blocks WHERE id = ?`, fixture.blockID))
	require.True(t, secondResolvedAt.Equal(firstResolvedAt),
		"a replayed settlement must not rewrite the resolved block")
	var secondSessionUpdatedAt time.Time
	require.NoError(t, fixture.repo.db.GetContext(ctx, &secondSessionUpdatedAt,
		`SELECT updated_at FROM task_sessions WHERE id = ?`, fixture.sessionID))
	require.True(t, secondSessionUpdatedAt.Equal(firstSessionUpdatedAt),
		"a replayed settlement must not advance the session revision")

	lateBlockID := "block-never-started-late"
	require.NoError(t, fixture.repo.UpsertSessionRecoveryBlock(ctx, &models.SessionRecoveryBlock{
		ID: lateBlockID, SessionID: fixture.sessionID, IncarnationID: fixture.incarnationID,
		ExpectedGeneration: 4, Reason: "unknown_prompt_outcome_late", State: models.RecoveryBlockOpen,
		ConsumerReference: "agent_delivery", DeliverySubmissionID: fixture.submissionID,
	}))
	request := fixture.request()
	request.BlockID = ""
	replayedAgain, err := fixture.repo.SettleNeverStartedAgentDeliverySubmission(ctx, request)
	require.NoError(t, err)
	require.True(t, replayedAgain.Settled)
	require.True(t, replayedAgain.AlreadySettled)
	require.Equal(t, []string{lateBlockID}, replayedAgain.ResolvedBlockIDs,
		"a replay must still resolve blocks bound to the settled submission")
	var lateBlockState string
	require.NoError(t, fixture.repo.db.GetContext(ctx, &lateBlockState,
		`SELECT state FROM session_recovery_blocks WHERE id = ?`, lateBlockID))
	require.Equal(t, models.RecoveryBlockResolved, lateBlockState)
}

func TestSettleNeverStartedConflictsOnTerminalSequence(t *testing.T) {
	ctx := context.Background()
	fixture := newNeverStartedSettlementFixture(t, models.DeliverySubmissionDispatching, "dispatching", "")
	_, err := fixture.repo.db.ExecContext(ctx, `
		UPDATE agent_delivery_submissions
		SET terminal_stream_id = ?, terminal_sequence = 3 WHERE id = ?`,
		"stream-terminal", fixture.submissionID)
	require.NoError(t, err)

	_, err = fixture.repo.SettleNeverStartedAgentDeliverySubmission(ctx, fixture.request())
	require.ErrorIs(t, err, repoerrors.ErrAgentDeliveryNeverStartedSettlementConflict)

	submission, err := fixture.repo.GetAgentDeliverySubmission(ctx, fixture.submissionID)
	require.NoError(t, err)
	require.Equal(t, models.DeliverySubmissionDispatching, submission.State)
	require.Equal(t, int64(3), submission.TerminalSequence, "terminal evidence must not be overwritten")
	require.Equal(t, "stream-terminal", submission.TerminalStreamID)
	var effectCount int
	require.NoError(t, fixture.repo.db.GetContext(ctx, &effectCount,
		`SELECT COUNT(*) FROM agent_delivery_effects WHERE effect_type = ?`,
		models.DeliveryNeverStartedEffectType))
	require.Zero(t, effectCount)
	var blockState string
	require.NoError(t, fixture.repo.db.GetContext(ctx, &blockState,
		`SELECT state FROM session_recovery_blocks WHERE id = ?`, fixture.blockID))
	require.Equal(t, models.RecoveryBlockOpen, blockState)
}

func TestSettleNeverStartedRejectsFailedWithoutEffect(t *testing.T) {
	ctx := context.Background()
	fixture := newNeverStartedSettlementFixture(t, models.DeliverySubmissionDispatching, "dispatching", "")
	_, err := fixture.repo.db.ExecContext(ctx,
		`UPDATE agent_delivery_submissions SET state = ?, outcome = ? WHERE id = ?`,
		models.DeliverySubmissionFailed, "failed", fixture.submissionID)
	require.NoError(t, err)

	_, err = fixture.repo.SettleNeverStartedAgentDeliverySubmission(ctx, fixture.request())
	require.ErrorIs(t, err, repoerrors.ErrAgentDeliveryNeverStartedSettlementConflict,
		"a failed submission without the never-started effect must not be treated as settled")
}

func TestSettleNeverStartedRejectsUnacceptedSourceState(t *testing.T) {
	ctx := context.Background()
	t.Run("evidence mismatch against a settleable row", func(t *testing.T) {
		fixture := newNeverStartedSettlementFixture(t, models.DeliverySubmissionDispatching, "dispatching", "")
		request := fixture.request()
		request.ExpectedState = models.DeliverySubmissionInterruptedUnknown
		request.ExpectedOutcome = "prompt_dispatch_failed"
		_, err := fixture.repo.SettleNeverStartedAgentDeliverySubmission(ctx, request)
		require.ErrorIs(t, err, repoerrors.ErrAgentDeliveryNeverStartedSettlementConflict)
		fixture.requireSubmissionUncertain(t)
	})
	t.Run("source state outside the accepted set", func(t *testing.T) {
		fixture := newNeverStartedSettlementFixture(t, models.DeliverySubmissionAccepted, "accepted", "")
		_, err := fixture.repo.SettleNeverStartedAgentDeliverySubmission(ctx, fixture.request())
		require.ErrorIs(t, err, repoerrors.ErrAgentDeliveryNeverStartedSettlementConflict)
		fixture.requireSubmissionUncertain(t)
	})
}

func TestSettleNeverStartedRejectsMissingSubmission(t *testing.T) {
	ctx := context.Background()
	fixture := newNeverStartedSettlementFixture(t, models.DeliverySubmissionDispatching, "dispatching", "")
	_, err := fixture.repo.db.ExecContext(ctx,
		`DELETE FROM agent_delivery_submissions WHERE id = ?`, fixture.submissionID)
	require.NoError(t, err)
	_, err = fixture.repo.SettleNeverStartedAgentDeliverySubmission(ctx, fixture.request())
	require.ErrorIs(t, err, repoerrors.ErrAgentDeliveryNeverStartedSettlementStale)
}
