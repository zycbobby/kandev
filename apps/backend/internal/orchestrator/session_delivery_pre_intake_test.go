package orchestrator

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agentctl/journal"
	"github.com/kandev/kandev/internal/orchestrator/executor"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	tasksqlite "github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/stretchr/testify/require"
)

const (
	preIntakeRetryTaskID     = "task-pre-intake-retry"
	preIntakeRetrySessionID  = "session-pre-intake-retry"
	preIntakeRetryIncarn     = "incarnation-pre-intake-retry"
	preIntakeRetrySubmission = "prompt:send-now:retry"
	preIntakeRetryBlockID    = "block-pre-intake-retry"
)

type preIntakeRetryOptions struct {
	outcome     string
	journalRows func(ctx context.Context, db *journal.Journal)
}

func seedPreIntakeRetry(t *testing.T, options preIntakeRetryOptions) (*Service, *retainedJournalRecoveryManager, *models.TaskSession, *tasksqlite.Repository) {
	t.Helper()
	ctx := context.Background()
	_, repo := newServiceWithRealRepo(t)
	seedSession(t, repo, preIntakeRetryTaskID, preIntakeRetrySessionID, "step-1", preIntakeRetryIncarn)
	require.NoError(t, repo.UpdateTaskSessionState(ctx, preIntakeRetrySessionID, models.TaskSessionStateWaitingForInput, ""))
	session, err := repo.GetTaskSession(ctx, preIntakeRetrySessionID)
	require.NoError(t, err)
	require.NoError(t, repo.CreateHarnessSessionGeneration(ctx, &models.HarnessSessionGeneration{
		SessionID: preIntakeRetrySessionID, IncarnationID: preIntakeRetryIncarn,
		Generation: 1, NativeSessionID: "native-pre-intake-retry", CreationReason: "initial",
	}))
	outcome := options.outcome
	if outcome == "" {
		outcome = "prompt_dispatch_failed"
	}
	now := time.Now().UTC()
	_, err = repo.PrepareAgentDeliverySubmission(ctx, &models.AgentDeliverySubmission{
		ID: preIntakeRetrySubmission, SessionID: preIntakeRetrySessionID, IncarnationID: preIntakeRetryIncarn,
		HarnessGeneration: 1, OwnerGeneration: 1, DispatchAttemptID: "message-pre-intake-retry",
		PayloadHash: "pre-intake-retry-hash", Payload: []byte(`{"text":"never journaled"}`),
		State: models.DeliverySubmissionInterruptedUnknown, Outcome: outcome,
		CreatedAt: now, UpdatedAt: now,
	})
	require.NoError(t, err)
	require.NoError(t, repo.UpsertSessionRecoveryBlock(ctx, &models.SessionRecoveryBlock{
		ID: preIntakeRetryBlockID, SessionID: preIntakeRetrySessionID, IncarnationID: preIntakeRetryIncarn,
		ExpectedGeneration: 1, Reason: "unknown_prompt_outcome", State: models.RecoveryBlockOpen,
		ConsumerReference: "agent_delivery", DeliverySubmissionID: preIntakeRetrySubmission,
	}))

	root := t.TempDir()
	journalDB, err := journal.Open(journal.Config{Path: journal.CheckStorage(root, preIntakeRetrySessionID).Path})
	require.NoError(t, err)
	if options.journalRows != nil {
		options.journalRows(ctx, journalDB)
	}
	require.NoError(t, journalDB.Close())
	manager := &retainedJournalRecoveryManager{
		mockAgentManager: &mockAgentManager{repoForExecutionLookup: repo}, root: root,
	}
	service := createTestServiceWithAgent(repo, newMockStepGetter(), newMockTaskRepo(), manager)
	persistentQueue, queueDB := newWorkflowTransferQueue(t, filepath.Join(t.TempDir(), "queue.db"))
	t.Cleanup(func() { _ = queueDB.Close() })
	service.messageQueue = persistentQueue
	return service, manager, session, repo
}

func preIntakeRetryBlock(session *models.TaskSession) *models.SessionRecoveryBlock {
	return &models.SessionRecoveryBlock{
		ID: preIntakeRetryBlockID, SessionID: session.ID, IncarnationID: preIntakeRetryIncarn,
		ExpectedGeneration: 1, Reason: "unknown_prompt_outcome", State: models.RecoveryBlockOpen,
		ConsumerReference: "agent_delivery", DeliverySubmissionID: preIntakeRetrySubmission,
	}
}

func requirePreIntakeNotSettled(t *testing.T, repo *tasksqlite.Repository, session *models.TaskSession) {
	t.Helper()
	ctx := context.Background()
	submission, err := repo.GetAgentDeliverySubmission(ctx, preIntakeRetrySubmission)
	require.NoError(t, err)
	require.Equal(t, models.DeliverySubmissionInterruptedUnknown, submission.State)
	blocks, err := repo.ListOpenSessionRecoveryBlocks(ctx, session.ID, preIntakeRetryIncarn, 1)
	require.NoError(t, err)
	require.Len(t, blocks, 1, "a conservative refusal must leave the bound block open")
}

func TestRetrySessionDeliverySettlesProvablyUnjournaledSubmission(t *testing.T) {
	ctx := context.Background()
	service, manager, session, repo := seedPreIntakeRetry(t, preIntakeRetryOptions{
		journalRows: func(ctx context.Context, db *journal.Journal) {
			payload := []byte(`{"text":"completed history"}`)
			_, err := db.PutSubmission(ctx, journal.Submission{
				ID: "prompt:completed-history", StreamID: "stream-history",
				SessionID: preIntakeRetrySessionID, IncarnationID: preIntakeRetryIncarn, HarnessGeneration: 1,
				Hash: journal.SubmissionHash(payload), Payload: payload,
				State: journal.SubmissionCompleted, TerminalEventRetained: true,
			})
			require.NoError(t, err)
		},
	})
	_, err := service.messageQueue.QueueMessage(ctx, session.ID, session.TaskID, "queued prompt", "", messagequeue.QueuedByUser, false, nil)
	require.NoError(t, err)
	_, ok := service.messageQueue.ReserveQueued(ctx, session.ID)
	require.True(t, ok)

	response, err := service.RetrySessionDelivery(ctx, session.TaskID, session.ID)
	require.NoError(t, err)
	require.Equal(t, SessionDeliveryRecoverySettled, response.Outcome)
	require.Empty(t, response.Reason)
	require.Empty(t, manager.capturedPromptCalls, "settlement must not dispatch the historical prompt")
	require.Equal(t, 1, manager.inspectCalls)
	require.Zero(t, manager.selectedPayloadReads, "absence evidence must not read a journal payload")
	require.Zero(t, manager.recoveryCalls)

	submission, err := repo.GetAgentDeliverySubmission(ctx, preIntakeRetrySubmission)
	require.NoError(t, err)
	require.Equal(t, models.DeliverySubmissionFailed, submission.State)
	require.Equal(t, "failed", submission.Outcome)
	require.Zero(t, submission.TerminalSequence)
	require.Nil(t, submission.FullySettledAt)

	blocks, err := repo.ListOpenSessionRecoveryBlocks(ctx, session.ID, preIntakeRetryIncarn, 1)
	require.NoError(t, err)
	require.Empty(t, blocks, "the bound block must be resolved")

	pending, err := service.messageQueue.ListPendingQueueDispatches(ctx)
	require.NoError(t, err)
	require.Empty(t, pending, "settlement must release the parked queue dispatch")
	require.Len(t, service.messageQueue.GetStatus(ctx, session.ID).Entries, 1, "the parked queue message must become visible")
}

func TestRetrySessionDeliveryKeepsBlockWhenJournalHasAnotherCandidate(t *testing.T) {
	ctx := context.Background()
	service, manager, session, repo := seedPreIntakeRetry(t, preIntakeRetryOptions{
		journalRows: func(ctx context.Context, db *journal.Journal) {
			payload := []byte(`{"text":"another unresolved candidate"}`)
			_, err := db.PutSubmission(ctx, journal.Submission{
				ID: "prompt:other", StreamID: "stream-other",
				SessionID: preIntakeRetrySessionID, IncarnationID: preIntakeRetryIncarn, HarnessGeneration: 1,
				Hash: journal.SubmissionHash(payload), Payload: payload, State: journal.SubmissionDispatching,
			})
			require.NoError(t, err)
		},
	})
	response, err := service.RetrySessionDelivery(ctx, session.TaskID, session.ID)
	require.NoError(t, err)
	require.Equal(t, SessionDeliveryRecoveryBlocked, response.Outcome)
	require.Empty(t, manager.capturedPromptCalls)
	requirePreIntakeNotSettled(t, repo, session)
}

func TestRetrySessionDeliveryKeepsBlockWhenJournalTruncated(t *testing.T) {
	ctx := context.Background()
	service, manager, session, repo := seedPreIntakeRetry(t, preIntakeRetryOptions{
		journalRows: func(ctx context.Context, db *journal.Journal) {
			for i := 0; i <= journal.MaxRecoverySubmissionSummaries; i++ {
				payload := []byte(`{"text":"candidate"}`)
				_, err := db.PutSubmission(ctx, journal.Submission{
					ID: fmt.Sprintf("prompt:candidate-%02d", i), StreamID: "stream-candidates",
					SessionID: preIntakeRetrySessionID, IncarnationID: preIntakeRetryIncarn, HarnessGeneration: 1,
					Hash: journal.SubmissionHash(payload), Payload: payload, State: journal.SubmissionDispatching,
				})
				require.NoError(t, err)
			}
		},
	})
	response, err := service.RetrySessionDelivery(ctx, session.TaskID, session.ID)
	require.NoError(t, err)
	require.Equal(t, SessionDeliveryRecoveryBlocked, response.Outcome)
	require.Equal(t, retainedEvidenceAmbiguousReason, response.Reason)
	require.Empty(t, manager.capturedPromptCalls)
	requirePreIntakeNotSettled(t, repo, session)
}

func TestRetrySessionDeliveryKeepsBlockWhenBlockStreamIsNotEmpty(t *testing.T) {
	ctx := context.Background()
	service, manager, session, repo := seedPreIntakeRetry(t, preIntakeRetryOptions{})
	block := preIntakeRetryBlock(session)
	block.DeliveryStreamID = "stream-previous"
	require.NoError(t, repo.UpsertSessionRecoveryBlock(ctx, block))

	response, err := service.RetrySessionDelivery(ctx, session.TaskID, session.ID)
	require.NoError(t, err)
	require.Equal(t, SessionDeliveryRecoveryBlocked, response.Outcome)
	require.Equal(t, retainedEvidenceAmbiguousReason, response.Reason)
	require.Empty(t, manager.capturedPromptCalls)
	requirePreIntakeNotSettled(t, repo, session)
}

func TestRetrySessionDeliveryKeepsBlockWhenSubmissionOutcomeDiffers(t *testing.T) {
	ctx := context.Background()
	service, manager, session, repo := seedPreIntakeRetry(t, preIntakeRetryOptions{outcome: "different_dispatch_failure"})
	response, err := service.RetrySessionDelivery(ctx, session.TaskID, session.ID)
	require.NoError(t, err)
	require.Equal(t, SessionDeliveryRecoveryBlocked, response.Outcome)
	require.Equal(t, deliveryIdentityMismatchReason, response.Reason)
	require.Empty(t, manager.capturedPromptCalls)
	requirePreIntakeNotSettled(t, repo, session)
}

func TestRetrySessionDeliveryPreIntakeSettlementIsIdempotent(t *testing.T) {
	ctx := context.Background()
	service, manager, session, repo := seedPreIntakeRetry(t, preIntakeRetryOptions{})
	block := preIntakeRetryBlock(session)
	first, err := service.retryMissingCanonicalDeliverySubmission(ctx, session, preIntakeRetryIncarn, 1, block)
	require.NoError(t, err)
	require.Equal(t, SessionDeliveryRecoverySettled, first.Outcome)
	second, err := service.retryMissingCanonicalDeliverySubmission(ctx, session, preIntakeRetryIncarn, 1, block)
	require.NoError(t, err)
	require.Equal(t, SessionDeliveryRecoverySettled, second.Outcome)
	require.Equal(t, 2, manager.inspectCalls)
	require.Empty(t, manager.capturedPromptCalls)

	effect, err := repo.GetAgentDeliveryEffect(ctx, models.DeliveryPreIntakeEffectKey(preIntakeRetrySubmission))
	require.NoError(t, err)
	require.Equal(t, models.DeliveryEffectCompleted, effect.State)
	blocks, err := repo.ListOpenSessionRecoveryBlocks(ctx, session.ID, preIntakeRetryIncarn, 1)
	require.NoError(t, err)
	require.Empty(t, blocks)
}

func TestRetrySessionDeliveryKeepsArchivedTaskBlocked(t *testing.T) {
	ctx := context.Background()
	service, manager, session, repo := seedPreIntakeRetry(t, preIntakeRetryOptions{})
	require.NoError(t, repo.ArchiveTask(ctx, session.TaskID))
	_, err := service.RetrySessionDelivery(ctx, session.TaskID, session.ID)
	require.ErrorIs(t, err, executor.ErrTaskArchived)
	require.Zero(t, manager.inspectCalls, "archived tasks must be refused before any evidence read")
	require.Empty(t, manager.capturedPromptCalls)
	requirePreIntakeNotSettled(t, repo, session)
}
