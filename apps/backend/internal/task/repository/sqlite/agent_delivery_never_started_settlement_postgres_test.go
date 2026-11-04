package sqlite

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestPostgresSettleNeverStartedAgentDeliverySubmissionSerializesConcurrentRetries(t *testing.T) {
	db := openIsolatedPostgresMultiConn(t, testutil.PostgresDSNFromEnv(t), 4)
	repo, err := NewWithDB(db, db, nil)
	require.NoError(t, err)
	ctx := context.Background()
	const (
		taskID        = "task-never-started-pg"
		sessionID     = "session-never-started-pg"
		incarnationID = "incarnation-never-started-pg"
		submissionID  = "prompt:send-now:never-started-pg"
		blockID       = "block-never-started-pg"
		streamID      = "stream-never-started-pg"
	)
	now := time.Now().UTC().Truncate(time.Microsecond)
	settledAt := now.Add(time.Minute)
	require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: taskID, Title: taskID}))
	require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
		ID: sessionID, TaskID: taskID, QueueIncarnationID: incarnationID,
		State: models.TaskSessionStateWaitingForInput,
	}))
	require.NoError(t, repo.CreateHarnessSessionGeneration(ctx, &models.HarnessSessionGeneration{
		SessionID: sessionID, IncarnationID: incarnationID, Generation: 4, CreationReason: "test",
	}))
	_, err = repo.PrepareAgentDeliverySubmission(ctx, &models.AgentDeliverySubmission{
		ID: submissionID, SessionID: sessionID, IncarnationID: incarnationID,
		HarnessGeneration: 4, OwnerGeneration: 4, DispatchAttemptID: "message-never-started-pg",
		PayloadHash: "never-started-hash-pg", Payload: []byte(`{"text":"never started"}`),
		State: models.DeliverySubmissionInterruptedUnknown, Outcome: "prompt_dispatch_failed",
		CreatedAt: now, UpdatedAt: now,
	})
	require.NoError(t, err)
	require.NoError(t, repo.UpsertSessionRecoveryBlock(ctx, &models.SessionRecoveryBlock{
		ID: blockID, SessionID: sessionID, IncarnationID: incarnationID, ExpectedGeneration: 4,
		Reason: "unknown_prompt_outcome", State: models.RecoveryBlockOpen,
		ConsumerReference: "agent_delivery", DeliverySubmissionID: submissionID,
		DeliveryStreamID: streamID,
	}))
	request := &models.AgentDeliveryNeverStartedSettlementRequest{
		SessionID: sessionID, IncarnationID: incarnationID, HarnessGeneration: 4,
		SubmissionID: submissionID, ExpectedState: models.DeliverySubmissionInterruptedUnknown,
		ExpectedOutcome: "prompt_dispatch_failed", BlockID: blockID, SettledAt: settledAt,
	}

	blocker, err := db.BeginTxx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = blocker.Rollback() }()
	_, err = blocker.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		agentDeliverySessionLockNamespace+sessionID)
	require.NoError(t, err)

	type result struct {
		value *models.AgentDeliveryNeverStartedSettlementResult
		err   error
	}
	results := make(chan result, 2)
	started := make(chan struct{}, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			started <- struct{}{}
			value, callErr := repo.SettleNeverStartedAgentDeliverySubmission(ctx, request)
			results <- result{value: value, err: callErr}
		}()
	}
	<-started
	<-started
	deadline := time.Now().Add(5 * time.Second)
	for db.Stats().InUse < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	require.GreaterOrEqual(t, db.Stats().InUse, 3, "both settlements must block on separate PostgreSQL connections")
	require.NoError(t, blocker.Commit())
	workers.Wait()
	close(results)

	fresh := 0
	replayed := 0
	for got := range results {
		require.NoError(t, got.err)
		require.NotNil(t, got.value)
		require.True(t, got.value.Settled)
		if got.value.AlreadySettled {
			replayed++
		} else {
			fresh++
		}
	}
	require.Equal(t, 1, fresh)
	require.Equal(t, 1, replayed)

	submission, err := repo.GetAgentDeliverySubmission(ctx, submissionID)
	require.NoError(t, err)
	require.Equal(t, models.DeliverySubmissionFailed, submission.State)
	require.Equal(t, "failed", submission.Outcome)
	require.Zero(t, submission.TerminalSequence)
	require.Nil(t, submission.FullySettledAt)
	var effectCount int
	require.NoError(t, db.GetContext(ctx, &effectCount,
		`SELECT COUNT(*) FROM agent_delivery_effects WHERE effect_type = ?`,
		models.DeliveryNeverStartedEffectType))
	require.Equal(t, 1, effectCount)
	var blockState, blockStream, authorizedAction string
	require.NoError(t, db.QueryRowxContext(ctx,
		`SELECT state, delivery_stream_id, authorized_action FROM session_recovery_blocks WHERE id = ?`,
		blockID).Scan(&blockState, &blockStream, &authorizedAction))
	require.Equal(t, models.RecoveryBlockResolved, blockState)
	require.Equal(t, streamID, blockStream)
	require.Equal(t, models.DeliveryNeverStartedAuthorizedAction, authorizedAction)
}
