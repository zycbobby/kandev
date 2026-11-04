package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
	"github.com/stretchr/testify/require"
)

type agentDeliveryAbandonedSettlementStore interface {
	SettleAbandonedAgentDeliverySubmission(
		context.Context,
		*models.AgentDeliveryAbandonedSettlementRequest,
	) (*models.AgentDeliveryAbandonedSettlementResult, error)
}

var _ agentDeliveryAbandonedSettlementStore = (*Repository)(nil)

type abandonedSettlementFixture struct {
	repo           *Repository
	sessionID      string
	incarnationID  string
	submissionID   string
	blockID        string
	streamID       string
	payloadHash    string
	settledAt      time.Time
	generation     int64
	initialState   models.DeliverySubmissionState
	initialOutcome string
}

func newAbandonedSettlementFixture(t *testing.T) *abandonedSettlementFixture {
	t.Helper()
	ctx := context.Background()
	const (
		taskID        = "task-abandoned"
		sessionID     = "session-abandoned"
		incarnationID = "incarnation-abandoned"
		submissionID  = "prompt:send-now:abandoned"
		blockID       = "block-abandoned"
		streamID      = "stream-abandoned"
		payloadHash   = "abandoned-hash"
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
		HarnessGeneration: 4, OwnerGeneration: 4, DispatchAttemptID: "message-abandoned",
		PayloadHash: payloadHash, Payload: []byte(`{"text":"abandoned prompt"}`),
		State: models.DeliverySubmissionInterruptedUnknown, Outcome: "unknown_prompt_outcome",
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
	return &abandonedSettlementFixture{
		repo: repo, sessionID: sessionID, incarnationID: incarnationID,
		submissionID: submissionID, blockID: blockID, streamID: streamID,
		payloadHash: payloadHash, settledAt: settledAt, generation: 4,
		initialState:   models.DeliverySubmissionInterruptedUnknown,
		initialOutcome: "unknown_prompt_outcome",
	}
}

func (f *abandonedSettlementFixture) request() *models.AgentDeliveryAbandonedSettlementRequest {
	return &models.AgentDeliveryAbandonedSettlementRequest{
		SessionID: f.sessionID, IncarnationID: f.incarnationID, HarnessGeneration: f.generation,
		SubmissionID: f.submissionID, DeliveryStreamID: f.streamID,
		ExpectedPayloadHash: f.payloadHash, ExpectedState: f.initialState,
		BlockID: f.blockID, SettledAt: f.settledAt,
	}
}

func (f *abandonedSettlementFixture) requireUnchanged(t *testing.T) {
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
		models.DeliveryAbandonedEffectType))
	require.Zero(t, effectCount)
	var blockState, blockStream, outcome, action string
	require.NoError(t, f.repo.db.QueryRowxContext(ctx, `
		SELECT state, delivery_stream_id, delivery_outcome, authorized_action
		FROM session_recovery_blocks WHERE id = ?`, f.blockID).
		Scan(&blockState, &blockStream, &outcome, &action))
	require.Equal(t, models.RecoveryBlockOpen, blockState)
	require.Equal(t, f.streamID, blockStream)
	require.Empty(t, outcome)
	require.Empty(t, action)
}

func TestSettleAbandonedAgentDeliverySubmissionSettlesAndResolvesStreamBlock(t *testing.T) {
	ctx := context.Background()
	fixture := newAbandonedSettlementFixture(t)
	result, err := fixture.repo.SettleAbandonedAgentDeliverySubmission(ctx, fixture.request())
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

	effect, err := fixture.repo.GetAgentDeliveryEffect(ctx, models.DeliveryAbandonedEffectKey(fixture.submissionID))
	require.NoError(t, err)
	require.Equal(t, models.DeliveryAbandonedEffectType, effect.EffectType)
	require.Equal(t, models.DeliveryEffectCompleted, effect.State)
	require.Empty(t, effect.StreamID)
	require.Zero(t, effect.Sequence)
	require.Equal(t, "failed", effect.Outcome)
	require.Equal(t, fixture.submissionID, effect.SubmissionID)
	require.NotNil(t, effect.CompletedAt)

	var state, streamID, outcome, action string
	var sequence int64
	var turnID string
	var resolvedAt *time.Time
	require.NoError(t, fixture.repo.db.QueryRowxContext(ctx, `
		SELECT state, delivery_stream_id, delivery_sequence, delivery_turn_id,
		       delivery_outcome, authorized_action, resolved_at
		FROM session_recovery_blocks WHERE id = ?`, fixture.blockID).
		Scan(&state, &streamID, &sequence, &turnID, &outcome, &action, &resolvedAt))
	require.Equal(t, models.RecoveryBlockResolved, state)
	require.Equal(t, fixture.streamID, streamID)
	require.Zero(t, sequence)
	require.Empty(t, turnID)
	require.Equal(t, "failed", outcome)
	require.Equal(t, models.DeliveryAbandonedAuthorizedAction, action)
	require.NotNil(t, resolvedAt)

	var sessionUpdatedAt time.Time
	require.NoError(t, fixture.repo.db.GetContext(ctx, &sessionUpdatedAt,
		`SELECT updated_at FROM task_sessions WHERE id = ?`, fixture.sessionID))
	require.True(t, sessionUpdatedAt.Equal(fixture.settledAt),
		"resolving a block must advance the session recovery revision")
}

func TestSettleAbandonedAgentDeliverySubmissionRequiresExactStreamBinding(t *testing.T) {
	ctx := context.Background()
	t.Run("different request stream", func(t *testing.T) {
		fixture := newAbandonedSettlementFixture(t)
		request := fixture.request()
		request.DeliveryStreamID = "stream-foreign"
		_, err := fixture.repo.SettleAbandonedAgentDeliverySubmission(ctx, request)
		require.ErrorIs(t, err, repoerrors.ErrAgentDeliveryAbandonedSettlementConflict)
		fixture.requireUnchanged(t)
	})
	t.Run("block without stream", func(t *testing.T) {
		fixture := newAbandonedSettlementFixture(t)
		_, err := fixture.repo.db.ExecContext(ctx,
			`UPDATE session_recovery_blocks SET delivery_stream_id = '' WHERE id = ?`, fixture.blockID)
		require.NoError(t, err)
		_, err = fixture.repo.SettleAbandonedAgentDeliverySubmission(ctx, fixture.request())
		require.ErrorIs(t, err, repoerrors.ErrAgentDeliveryAbandonedSettlementConflict)
		var effectCount int
		require.NoError(t, fixture.repo.db.GetContext(ctx, &effectCount,
			`SELECT COUNT(*) FROM agent_delivery_effects WHERE effect_type = ?`,
			models.DeliveryAbandonedEffectType))
		require.Zero(t, effectCount)
	})
	t.Run("missing block", func(t *testing.T) {
		fixture := newAbandonedSettlementFixture(t)
		_, err := fixture.repo.db.ExecContext(ctx,
			`DELETE FROM session_recovery_blocks WHERE id = ?`, fixture.blockID)
		require.NoError(t, err)
		_, err = fixture.repo.SettleAbandonedAgentDeliverySubmission(ctx, fixture.request())
		require.ErrorIs(t, err, repoerrors.ErrAgentDeliveryAbandonedSettlementConflict)
	})
	t.Run("unrelated stream block stays open", func(t *testing.T) {
		fixture := newAbandonedSettlementFixture(t)
		require.NoError(t, fixture.repo.UpsertSessionRecoveryBlock(ctx, &models.SessionRecoveryBlock{
			ID: "block-unrelated-abandoned", SessionID: fixture.sessionID,
			IncarnationID: fixture.incarnationID, ExpectedGeneration: 4,
			Reason: "independent_recovery", State: models.RecoveryBlockOpen,
			ConsumerReference: "agent_delivery", DeliverySubmissionID: "prompt:other",
			DeliveryStreamID: "stream-other",
		}))
		result, err := fixture.repo.SettleAbandonedAgentDeliverySubmission(ctx, fixture.request())
		require.NoError(t, err)
		require.Equal(t, []string{fixture.blockID}, result.ResolvedBlockIDs)
		var blockState string
		require.NoError(t, fixture.repo.db.GetContext(ctx, &blockState,
			`SELECT state FROM session_recovery_blocks WHERE id = ?`, "block-unrelated-abandoned"))
		require.Equal(t, models.RecoveryBlockOpen, blockState)
	})
}

func TestSettleAbandonedAgentDeliverySubmissionRejectsConflicts(t *testing.T) {
	ctx := context.Background()
	scenarios := []struct {
		name   string
		mutate func(t *testing.T, fixture *abandonedSettlementFixture, request *models.AgentDeliveryAbandonedSettlementRequest)
	}{
		{
			name: "expected state mismatch",
			mutate: func(_ *testing.T, _ *abandonedSettlementFixture, request *models.AgentDeliveryAbandonedSettlementRequest) {
				request.ExpectedState = models.DeliverySubmissionDispatching
			},
		},
		{
			name: "payload hash mismatch",
			mutate: func(_ *testing.T, _ *abandonedSettlementFixture, request *models.AgentDeliveryAbandonedSettlementRequest) {
				request.ExpectedPayloadHash = "foreign-hash"
			},
		},
		{
			name: "terminal sequence already recorded",
			mutate: func(t *testing.T, fixture *abandonedSettlementFixture, _ *models.AgentDeliveryAbandonedSettlementRequest) {
				_, err := fixture.repo.db.ExecContext(ctx, `
					UPDATE agent_delivery_submissions
					SET terminal_stream_id = ?, terminal_sequence = 3 WHERE id = ?`,
					fixture.streamID, fixture.submissionID)
				require.NoError(t, err)
			},
		},
		{
			name: "fully settled already recorded",
			mutate: func(t *testing.T, fixture *abandonedSettlementFixture, _ *models.AgentDeliveryAbandonedSettlementRequest) {
				_, err := fixture.repo.db.ExecContext(ctx,
					`UPDATE agent_delivery_submissions SET fully_settled_at = ? WHERE id = ?`,
					fixture.settledAt, fixture.submissionID)
				require.NoError(t, err)
			},
		},
		{
			name: "submission already completed",
			mutate: func(t *testing.T, fixture *abandonedSettlementFixture, _ *models.AgentDeliveryAbandonedSettlementRequest) {
				_, err := fixture.repo.db.ExecContext(ctx,
					`UPDATE agent_delivery_submissions SET state = ?, outcome = ? WHERE id = ?`,
					models.DeliverySubmissionCompleted, "completed", fixture.submissionID)
				require.NoError(t, err)
			},
		},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := newAbandonedSettlementFixture(t)
			request := fixture.request()
			scenario.mutate(t, fixture, request)
			_, err := fixture.repo.SettleAbandonedAgentDeliverySubmission(ctx, request)
			require.ErrorIs(t, err, repoerrors.ErrAgentDeliveryAbandonedSettlementConflict)
			var effectCount int
			require.NoError(t, fixture.repo.db.GetContext(ctx, &effectCount,
				`SELECT COUNT(*) FROM agent_delivery_effects WHERE effect_type = ?`,
				models.DeliveryAbandonedEffectType))
			require.Zero(t, effectCount)
			var blockState string
			require.NoError(t, fixture.repo.db.GetContext(ctx, &blockState,
				`SELECT state FROM session_recovery_blocks WHERE id = ?`, fixture.blockID))
			require.Equal(t, models.RecoveryBlockOpen, blockState)
		})
	}
}

func TestSettleAbandonedAgentDeliverySubmissionRejectsMissingSubmission(t *testing.T) {
	ctx := context.Background()
	fixture := newAbandonedSettlementFixture(t)
	_, err := fixture.repo.db.ExecContext(ctx,
		`DELETE FROM agent_delivery_submissions WHERE id = ?`, fixture.submissionID)
	require.NoError(t, err)
	_, err = fixture.repo.SettleAbandonedAgentDeliverySubmission(ctx, fixture.request())
	require.ErrorIs(t, err, repoerrors.ErrAgentDeliveryAbandonedSettlementStale)
}

func TestSettleAbandonedAgentDeliverySubmissionIsIdempotent(t *testing.T) {
	ctx := context.Background()
	fixture := newAbandonedSettlementFixture(t)
	_, err := fixture.repo.SettleAbandonedAgentDeliverySubmission(ctx, fixture.request())
	require.NoError(t, err)
	var firstResolvedAt time.Time
	require.NoError(t, fixture.repo.db.GetContext(ctx, &firstResolvedAt,
		`SELECT resolved_at FROM session_recovery_blocks WHERE id = ?`, fixture.blockID))
	var firstSessionUpdatedAt time.Time
	require.NoError(t, fixture.repo.db.GetContext(ctx, &firstSessionUpdatedAt,
		`SELECT updated_at FROM task_sessions WHERE id = ?`, fixture.sessionID))

	fixture.repo.clockNow = func() time.Time { return fixture.settledAt.Add(time.Hour) }
	replayed, err := fixture.repo.SettleAbandonedAgentDeliverySubmission(ctx, fixture.request())
	require.NoError(t, err)
	require.True(t, replayed.Settled)
	require.True(t, replayed.AlreadySettled)
	require.Empty(t, replayed.ResolvedBlockIDs)

	var effectCount int
	require.NoError(t, fixture.repo.db.GetContext(ctx, &effectCount,
		`SELECT COUNT(*) FROM agent_delivery_effects WHERE effect_type = ?`,
		models.DeliveryAbandonedEffectType))
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
}

func TestSettleAbandonedAgentDeliverySubmissionRejectsFailedWithoutEffect(t *testing.T) {
	ctx := context.Background()
	fixture := newAbandonedSettlementFixture(t)
	_, err := fixture.repo.db.ExecContext(ctx,
		`UPDATE agent_delivery_submissions SET state = ?, outcome = ? WHERE id = ?`,
		models.DeliverySubmissionFailed, "failed", fixture.submissionID)
	require.NoError(t, err)
	_, err = fixture.repo.SettleAbandonedAgentDeliverySubmission(ctx, fixture.request())
	require.ErrorIs(t, err, repoerrors.ErrAgentDeliveryAbandonedSettlementConflict,
		"a failed submission without the abandoned effect must not be treated as settled")
}
