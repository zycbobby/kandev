package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
	"github.com/stretchr/testify/require"
)

type agentDeliveryPreIntakeSettlementStore interface {
	SettlePreIntakeAgentDeliverySubmission(
		context.Context,
		*models.AgentDeliveryPreIntakeSettlementRequest,
	) (*models.AgentDeliveryPreIntakeSettlementResult, error)
}

type preIntakeSettlementFixture struct {
	repo          *Repository
	sessionID     string
	incarnationID string
	submissionID  string
	blockID       string
	settledAt     time.Time
}

func newPreIntakeSettlementFixture(t *testing.T) *preIntakeSettlementFixture {
	t.Helper()
	ctx := context.Background()
	const (
		taskID        = "task-pre-intake"
		sessionID     = "session-pre-intake"
		incarnationID = "incarnation-pre-intake"
		submissionID  = "prompt:send-now:pre-intake"
		blockID       = "block-pre-intake"
	)
	now := time.Date(2026, 11, 3, 14, 0, 0, 0, time.UTC)
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
		HarnessGeneration: 4, OwnerGeneration: 4, DispatchAttemptID: "message-pre-intake",
		PayloadHash: "pre-intake-hash", Payload: []byte(`{"text":"never journaled"}`),
		State: models.DeliverySubmissionDispatching, Outcome: "dispatching",
		CreatedAt: now, UpdatedAt: now,
	})
	require.NoError(t, err)
	require.NoError(t, repo.UpsertSessionRecoveryBlock(ctx, &models.SessionRecoveryBlock{
		ID: blockID, SessionID: sessionID, IncarnationID: incarnationID,
		ExpectedGeneration: 4, Reason: "unknown_prompt_outcome", State: models.RecoveryBlockOpen,
		ConsumerReference: "agent_delivery", DeliverySubmissionID: submissionID,
	}))
	settledAt := now.Add(time.Minute)
	repo.clockNow = func() time.Time { return settledAt }
	return &preIntakeSettlementFixture{
		repo: repo, sessionID: sessionID, incarnationID: incarnationID,
		submissionID: submissionID, blockID: blockID, settledAt: settledAt,
	}
}

func (f *preIntakeSettlementFixture) request() *models.AgentDeliveryPreIntakeSettlementRequest {
	return &models.AgentDeliveryPreIntakeSettlementRequest{
		SessionID: f.sessionID, IncarnationID: f.incarnationID, HarnessGeneration: 4,
		SubmissionID: f.submissionID, ExpectedState: models.DeliverySubmissionDispatching,
		ExpectedOutcome: "dispatching", BlockID: f.blockID, SettledAt: f.settledAt,
	}
}

func (f *preIntakeSettlementFixture) store(t *testing.T) agentDeliveryPreIntakeSettlementStore {
	t.Helper()
	store, ok := any(f.repo).(agentDeliveryPreIntakeSettlementStore)
	require.True(t, ok, "SQLite repository must expose atomic pre-intake settlement")
	return store
}

func TestSettlePreIntakeAgentDeliverySubmissionSettlesAndResolvesBlock(t *testing.T) {
	ctx := context.Background()
	fixture := newPreIntakeSettlementFixture(t)
	result, err := fixture.store(t).SettlePreIntakeAgentDeliverySubmission(ctx, fixture.request())
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

	effect, err := fixture.repo.GetAgentDeliveryEffect(ctx, models.DeliveryPreIntakeEffectKey(fixture.submissionID))
	require.NoError(t, err)
	require.Equal(t, models.DeliveryPreIntakeEffectType, effect.EffectType)
	require.Equal(t, models.DeliveryEffectCompleted, effect.State)
	require.Empty(t, effect.StreamID)
	require.Zero(t, effect.Sequence)
	require.Equal(t, "failed", effect.Outcome)
	require.NotNil(t, effect.CompletedAt)

	var state, outcome, authorizedAction string
	var resolvedAt *time.Time
	require.NoError(t, fixture.repo.db.QueryRowxContext(ctx, `
		SELECT state, delivery_outcome, authorized_action, resolved_at
		FROM session_recovery_blocks WHERE id = ?`, fixture.blockID).
		Scan(&state, &outcome, &authorizedAction, &resolvedAt))
	require.Equal(t, models.RecoveryBlockResolved, state)
	require.Equal(t, "failed", outcome)
	require.Equal(t, "durable_delivery_not_delivered", authorizedAction)
	require.NotNil(t, resolvedAt)

	var sessionUpdatedAt time.Time
	require.NoError(t, fixture.repo.db.GetContext(ctx, &sessionUpdatedAt,
		`SELECT updated_at FROM task_sessions WHERE id = ?`, fixture.sessionID))
	require.True(t, sessionUpdatedAt.Equal(fixture.settledAt), "resolving a block must advance the session recovery revision")
}

func TestSettlePreIntakeAgentDeliverySubmissionIsIdempotent(t *testing.T) {
	ctx := context.Background()
	fixture := newPreIntakeSettlementFixture(t)
	store := fixture.store(t)
	_, err := store.SettlePreIntakeAgentDeliverySubmission(ctx, fixture.request())
	require.NoError(t, err)
	var firstResolvedAt time.Time
	require.NoError(t, fixture.repo.db.GetContext(ctx, &firstResolvedAt,
		`SELECT resolved_at FROM session_recovery_blocks WHERE id = ?`, fixture.blockID))

	fixture.repo.clockNow = func() time.Time { return fixture.settledAt.Add(time.Hour) }
	replayed, err := store.SettlePreIntakeAgentDeliverySubmission(ctx, fixture.request())
	require.NoError(t, err)
	require.True(t, replayed.Settled)
	require.True(t, replayed.AlreadySettled)
	require.Empty(t, replayed.ResolvedBlockIDs)

	var effectCount int
	require.NoError(t, fixture.repo.db.GetContext(ctx, &effectCount,
		`SELECT COUNT(*) FROM agent_delivery_effects WHERE effect_type = ?`, models.DeliveryPreIntakeEffectType))
	require.Equal(t, 1, effectCount)
	var secondResolvedAt time.Time
	require.NoError(t, fixture.repo.db.GetContext(ctx, &secondResolvedAt,
		`SELECT resolved_at FROM session_recovery_blocks WHERE id = ?`, fixture.blockID))
	require.True(t, secondResolvedAt.Equal(firstResolvedAt), "a replayed settlement must not rewrite the resolved block")
	var sessionUpdatedAt time.Time
	require.NoError(t, fixture.repo.db.GetContext(ctx, &sessionUpdatedAt,
		`SELECT updated_at FROM task_sessions WHERE id = ?`, fixture.sessionID))
	require.True(t, sessionUpdatedAt.Equal(fixture.settledAt), "a replayed settlement must not advance the session revision")
}

func TestSettlePreIntakeAgentDeliverySubmissionRejectsStateConflict(t *testing.T) {
	ctx := context.Background()
	fixture := newPreIntakeSettlementFixture(t)
	request := fixture.request()
	request.ExpectedState = models.DeliverySubmissionInterruptedUnknown
	request.ExpectedOutcome = "prompt_dispatch_failed"
	_, err := fixture.store(t).SettlePreIntakeAgentDeliverySubmission(ctx, request)
	require.ErrorIs(t, err, repoerrors.ErrAgentDeliveryPreIntakeSettlementConflict)

	submission, err := fixture.repo.GetAgentDeliverySubmission(ctx, fixture.submissionID)
	require.NoError(t, err)
	require.Equal(t, models.DeliverySubmissionDispatching, submission.State)
	var effectCount int
	require.NoError(t, fixture.repo.db.GetContext(ctx, &effectCount,
		`SELECT COUNT(*) FROM agent_delivery_effects WHERE submission_id = ?`, fixture.submissionID))
	require.Zero(t, effectCount)
	var blockState string
	require.NoError(t, fixture.repo.db.GetContext(ctx, &blockState,
		`SELECT state FROM session_recovery_blocks WHERE id = ?`, fixture.blockID))
	require.Equal(t, models.RecoveryBlockOpen, blockState)

	_, err = fixture.repo.db.ExecContext(ctx,
		`UPDATE agent_delivery_submissions SET state = ?, outcome = ? WHERE id = ?`,
		models.DeliverySubmissionFailed, "failed", fixture.submissionID)
	require.NoError(t, err)
	_, err = fixture.store(t).SettlePreIntakeAgentDeliverySubmission(ctx, fixture.request())
	require.ErrorIs(t, err, repoerrors.ErrAgentDeliveryPreIntakeSettlementConflict,
		"a failed submission without the pre-intake effect must not be treated as settled")
}

func TestSettlePreIntakeAgentDeliverySubmissionLeavesUnrelatedBlocksOpen(t *testing.T) {
	ctx := context.Background()
	fixture := newPreIntakeSettlementFixture(t)
	unrelated := []*models.SessionRecoveryBlock{
		{
			ID: "block-stream-bound", SessionID: fixture.sessionID, IncarnationID: fixture.incarnationID,
			ExpectedGeneration: 4, Reason: "unknown_prompt_outcome_stream", State: models.RecoveryBlockOpen,
			ConsumerReference: "agent_delivery", DeliverySubmissionID: fixture.submissionID,
			DeliveryStreamID: "stream-old",
		},
		{
			ID: "block-other-submission", SessionID: fixture.sessionID, IncarnationID: fixture.incarnationID,
			ExpectedGeneration: 4, Reason: "unknown_prompt_outcome_other", State: models.RecoveryBlockOpen,
			ConsumerReference: "agent_delivery", DeliverySubmissionID: "prompt:other",
		},
		{
			ID: "block-other-consumer", SessionID: fixture.sessionID, IncarnationID: fixture.incarnationID,
			ExpectedGeneration: 4, Reason: "workflow_step", State: models.RecoveryBlockOpen,
			ConsumerReference: "workflow",
		},
	}
	for _, block := range unrelated {
		require.NoError(t, fixture.repo.UpsertSessionRecoveryBlock(ctx, block))
	}
	result, err := fixture.store(t).SettlePreIntakeAgentDeliverySubmission(ctx, fixture.request())
	require.NoError(t, err)
	require.Equal(t, []string{fixture.blockID}, result.ResolvedBlockIDs)
	var openCount int
	require.NoError(t, fixture.repo.db.GetContext(ctx, &openCount, `
		SELECT COUNT(*) FROM session_recovery_blocks
		WHERE session_id = ? AND state = ?`, fixture.sessionID, models.RecoveryBlockOpen))
	require.Equal(t, len(unrelated), openCount)
}
