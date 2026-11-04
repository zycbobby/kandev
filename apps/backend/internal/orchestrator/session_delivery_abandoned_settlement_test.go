package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agentctl/journal"
	"github.com/kandev/kandev/internal/common/processidentity"
	"github.com/kandev/kandev/internal/orchestrator/executor"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	taskrepo "github.com/kandev/kandev/internal/task/repository"
	tasksqlite "github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/stretchr/testify/require"
)

const (
	abandonedRetryTaskID      = "task-abandoned-retry"
	abandonedRetrySessionID   = "session-abandoned-retry"
	abandonedRetryIncarn      = "incarnation-abandoned-retry"
	abandonedRetrySubmission  = "prompt:send-now:abandoned-retry"
	abandonedRetryStream      = "stream-abandoned-retry"
	abandonedRetryBlockID     = "block-abandoned-retry"
	abandonedRetryGeneration  = int64(1)
	abandonedRetryPromptGen   = uint64(3)
	abandonedRetryExecutionID = "abandoned-execution"
)

type abandonedRetryOptions struct {
	journalRows func(ctx context.Context, db *journal.Journal)
	skipTarget  bool
	target      *journal.Submission
	terminated  *bool
}

type abandonedRetryFixture struct {
	service *Service
	manager *retainedJournalRecoveryManager
	session *models.TaskSession
	repo    *tasksqlite.Repository
	payload []byte
	hash    string
}

func seedAbandonedRetry(t *testing.T, options abandonedRetryOptions) *abandonedRetryFixture {
	t.Helper()
	ctx := context.Background()
	_, repo := newServiceWithRealRepo(t)
	seedSession(t, repo, abandonedRetryTaskID, abandonedRetrySessionID, "step-1", abandonedRetryIncarn)
	require.NoError(t, repo.UpdateTaskSessionState(ctx, abandonedRetrySessionID, models.TaskSessionStateWaitingForInput, ""))
	session, err := repo.GetTaskSession(ctx, abandonedRetrySessionID)
	require.NoError(t, err)
	seedExecutorRunning(t, repo, abandonedRetrySessionID, abandonedRetryTaskID, abandonedRetryExecutionID)
	require.NoError(t, repo.CreateHarnessSessionGeneration(ctx, &models.HarnessSessionGeneration{
		SessionID: abandonedRetrySessionID, IncarnationID: abandonedRetryIncarn,
		Generation: abandonedRetryGeneration, NativeSessionID: "native-abandoned-retry", CreationReason: "initial",
	}))
	payload := []byte(`{"text":"abandoned instruction"}`)
	hash := journal.SubmissionHash(payload)
	now := time.Now().UTC()
	_, err = repo.PrepareAgentDeliverySubmission(ctx, &models.AgentDeliverySubmission{
		ID: abandonedRetrySubmission, SessionID: abandonedRetrySessionID, IncarnationID: abandonedRetryIncarn,
		HarnessGeneration: abandonedRetryGeneration, OwnerGeneration: abandonedRetryGeneration,
		DispatchAttemptID: "message-abandoned-retry", PayloadHash: hash, Payload: payload,
		State: models.DeliverySubmissionInterruptedUnknown, Outcome: "unknown_prompt_outcome",
		CreatedAt: now, UpdatedAt: now,
	})
	require.NoError(t, err)
	recovery := models.AgentDeliveryRecovery{
		OriginalRuntime: processidentity.Identity{PID: 4242, BirthToken: "abandoned-birth-token"},
		Phase:           models.AgentDeliveryRecoveryUncertain, SessionID: abandonedRetrySessionID,
		AgentExecutionID: abandonedRetryExecutionID, SubmissionID: abandonedRetrySubmission,
		StreamID: abandonedRetryStream, IncarnationID: abandonedRetryIncarn,
		HarnessGeneration: abandonedRetryGeneration, PromptGeneration: abandonedRetryPromptGen,
	}
	changed, err := repo.UpsertAgentDeliveryRecovery(ctx, &recovery, &models.SessionRecoveryBlock{
		ID: abandonedRetryBlockID, SessionID: abandonedRetrySessionID, IncarnationID: abandonedRetryIncarn,
		ExpectedGeneration: abandonedRetryGeneration, Reason: "unknown_prompt_outcome",
		State: models.RecoveryBlockOpen, ConsumerReference: "agent_delivery",
		DeliverySubmissionID: abandonedRetrySubmission, DeliveryStreamID: abandonedRetryStream,
	})
	require.NoError(t, err)
	require.True(t, changed)

	root := t.TempDir()
	journalDB, err := journal.Open(journal.Config{Path: journal.CheckStorage(root, abandonedRetrySessionID).Path})
	require.NoError(t, err)
	if !options.skipTarget {
		target := journal.Submission{
			ID: abandonedRetrySubmission, StreamID: abandonedRetryStream,
			SessionID: abandonedRetrySessionID, IncarnationID: abandonedRetryIncarn,
			HarnessGeneration: uint64(abandonedRetryGeneration), Hash: hash, Payload: payload,
			State: journal.SubmissionInterruptedUnknown,
		}
		if options.target != nil {
			target = *options.target
		}
		_, err = journalDB.PutSubmission(ctx, target)
		require.NoError(t, err)
	}
	if options.journalRows != nil {
		options.journalRows(ctx, journalDB)
	}
	require.NoError(t, journalDB.Close())

	terminated := true
	if options.terminated != nil {
		terminated = *options.terminated
	}
	manager := &retainedJournalRecoveryManager{
		mockAgentManager: &mockAgentManager{repoForExecutionLookup: repo}, root: root, terminated: terminated,
	}
	service := createTestServiceWithAgent(repo, newMockStepGetter(), newMockTaskRepo(), manager)
	persistentQueue, queueDB := newWorkflowTransferQueue(t, filepath.Join(t.TempDir(), "queue.db"))
	t.Cleanup(func() { _ = queueDB.Close() })
	service.messageQueue = persistentQueue
	return &abandonedRetryFixture{
		service: service, manager: manager, session: session, repo: repo, payload: payload, hash: hash,
	}
}

func (f *abandonedRetryFixture) sessionRecovery(t *testing.T) (models.AgentDeliveryRecovery, bool) {
	t.Helper()
	session, err := f.repo.GetTaskSession(context.Background(), f.session.ID)
	require.NoError(t, err)
	return models.LoadAgentDeliveryRecovery(session.Metadata)
}

func (f *abandonedRetryFixture) requireUnsettled(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	submission, err := f.repo.GetAgentDeliverySubmission(ctx, abandonedRetrySubmission)
	require.NoError(t, err)
	require.Equal(t, models.DeliverySubmissionInterruptedUnknown, submission.State)
	require.Zero(t, submission.TerminalSequence)
	require.Nil(t, submission.FullySettledAt)
	_, err = f.repo.GetAgentDeliveryEffect(ctx, models.DeliveryAbandonedEffectKey(abandonedRetrySubmission))
	require.ErrorIs(t, err, taskrepo.ErrAgentDeliveryEffectNotFound)
	blocks, err := f.repo.ListOpenSessionRecoveryBlocks(ctx, f.session.ID, abandonedRetryIncarn, abandonedRetryGeneration)
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	require.Equal(t, abandonedRetryStream, blocks[0].DeliveryStreamID)
}

func TestRetrySessionDeliverySettlesAbandonedJournalDelivery(t *testing.T) {
	ctx := context.Background()
	fixture := seedAbandonedRetry(t, abandonedRetryOptions{})
	_, err := fixture.service.messageQueue.QueueMessage(ctx, fixture.session.ID, fixture.session.TaskID,
		"queued prompt", "", messagequeue.QueuedByUser, false, nil)
	require.NoError(t, err)
	_, ok := fixture.service.messageQueue.ReserveQueued(ctx, fixture.session.ID)
	require.True(t, ok)

	response, err := fixture.service.RetrySessionDelivery(ctx, fixture.session.TaskID, fixture.session.ID)
	require.NoError(t, err)
	require.Equal(t, SessionDeliveryRecoverySettled, response.Outcome, response.Reason)
	require.Empty(t, response.Reason)
	require.Empty(t, fixture.manager.capturedPromptCalls, "abandoned settlement must not dispatch the historical prompt")
	require.Equal(t, 1, fixture.manager.inspectCalls)
	require.Equal(t, 1, fixture.manager.retirementCalls)
	require.Zero(t, fixture.manager.selectedPayloadReads)
	require.Zero(t, fixture.manager.recoveryCalls)

	submission, err := fixture.repo.GetAgentDeliverySubmission(ctx, abandonedRetrySubmission)
	require.NoError(t, err)
	require.Equal(t, models.DeliverySubmissionFailed, submission.State)
	require.Equal(t, "failed", submission.Outcome)
	require.Zero(t, submission.TerminalSequence)
	require.Nil(t, submission.FullySettledAt)

	effect, err := fixture.repo.GetAgentDeliveryEffect(ctx, models.DeliveryAbandonedEffectKey(abandonedRetrySubmission))
	require.NoError(t, err)
	require.Equal(t, models.DeliveryAbandonedEffectType, effect.EffectType)
	require.Equal(t, models.DeliveryEffectCompleted, effect.State)
	require.Empty(t, effect.StreamID)
	require.Zero(t, effect.Sequence)
	require.Equal(t, "failed", effect.Outcome)

	block, err := fixture.repo.GetSessionRecoveryBlock(ctx, abandonedRetryBlockID)
	require.NoError(t, err)
	require.Equal(t, models.RecoveryBlockResolved, block.State)
	require.Equal(t, abandonedRetryStream, block.DeliveryStreamID)
	require.Equal(t, "failed", block.DeliveryOutcome)
	require.Equal(t, models.DeliveryAbandonedAuthorizedAction, block.AuthorizedAction)
	require.NotNil(t, block.ResolvedAt)

	recovery, found := fixture.sessionRecovery(t)
	require.True(t, found)
	require.Equal(t, models.AgentDeliveryRecoverySettled, recovery.Phase)

	pending, err := fixture.service.messageQueue.ListPendingQueueDispatches(ctx)
	require.NoError(t, err)
	require.Empty(t, pending, "settlement must release the parked queue dispatch")
	require.Len(t, fixture.service.messageQueue.GetStatus(ctx, fixture.session.ID).Entries, 1,
		"the parked queue message must become visible")

	stored, err := journalOpenRetainedSubmission(t, fixture.manager.root, abandonedRetrySubmission)
	require.NoError(t, err)
	require.True(t, stored.Retired)
	require.Equal(t, journal.SubmissionInterruptedUnknown, stored.State)
}

func journalOpenRetainedSubmission(t *testing.T, root, submissionID string) (journal.Submission, error) {
	t.Helper()
	store, err := journal.Open(journal.Config{Path: journal.CheckStorage(root, abandonedRetrySessionID).Path})
	if err != nil {
		return journal.Submission{}, err
	}
	defer func() { _ = store.Close() }()
	return store.GetSubmission(context.Background(), submissionID)
}

func TestRetrySessionDeliveryFallsBackToTerminalRetainedHistory(t *testing.T) {
	ctx := context.Background()
	fixture := seedAbandonedRetry(t, abandonedRetryOptions{
		target: &journal.Submission{
			ID: abandonedRetrySubmission, StreamID: abandonedRetryStream,
			SessionID: abandonedRetrySessionID, IncarnationID: abandonedRetryIncarn,
			HarnessGeneration:     uint64(abandonedRetryGeneration),
			Hash:                  journal.SubmissionHash([]byte(`{"text":"abandoned instruction"}`)),
			Payload:               []byte(`{"text":"abandoned instruction"}`),
			State:                 journal.SubmissionInterruptedUnknown,
			TerminalEventRetained: true, TerminalSequence: 1,
		},
	})
	response, err := fixture.service.RetrySessionDelivery(ctx, fixture.session.TaskID, fixture.session.ID)
	require.NoError(t, err)
	require.Equal(t, SessionDeliveryRecoveryUncertain, response.Outcome)
	require.Equal(t, 1, fixture.manager.recoveryCalls, "terminal retained history keeps the existing runtime path")
	require.Zero(t, fixture.manager.retirementCalls)
	require.Empty(t, fixture.manager.capturedPromptCalls)
	fixture.requireUnsettled(t)
}

func TestRetrySessionDeliveryFallsBackWhenJournalSubmissionIsAbsent(t *testing.T) {
	ctx := context.Background()
	fixture := seedAbandonedRetry(t, abandonedRetryOptions{skipTarget: true})
	response, err := fixture.service.RetrySessionDelivery(ctx, fixture.session.TaskID, fixture.session.ID)
	require.NoError(t, err)
	require.Equal(t, SessionDeliveryRecoveryUncertain, response.Outcome)
	require.Equal(t, 1, fixture.manager.recoveryCalls, "absence keeps the existing runtime recovery path")
	require.Zero(t, fixture.manager.retirementCalls)
	require.Empty(t, fixture.manager.capturedPromptCalls)
	fixture.requireUnsettled(t)
}

func TestRetrySessionDeliveryBlocksUnsafeAbandonedEvidence(t *testing.T) {
	ctx := context.Background()
	basePayload := []byte(`{"text":"abandoned instruction"}`)
	scenarios := []struct {
		name     string
		reason   string
		options  abandonedRetryOptions
		extraSQL func(t *testing.T, repo *tasksqlite.Repository)
	}{
		{
			name:   "truncated descriptor",
			reason: retainedEvidenceAmbiguousReason,
			options: abandonedRetryOptions{journalRows: func(ctx context.Context, db *journal.Journal) {
				for i := 0; i <= journal.MaxRecoverySubmissionSummaries; i++ {
					_, err := db.PutSubmission(ctx, journal.Submission{
						ID: fmt.Sprintf("prompt:candidate-%02d", i), StreamID: "stream-candidates",
						SessionID: abandonedRetrySessionID, IncarnationID: abandonedRetryIncarn,
						HarnessGeneration: uint64(abandonedRetryGeneration),
						Hash:              journal.SubmissionHash([]byte(fmt.Sprintf("candidate-%02d", i))),
						Payload:           []byte(fmt.Sprintf("candidate-%02d", i)),
						State:             journal.SubmissionInterruptedUnknown,
					})
					require.NoError(t, err)
				}
			}},
		},
		{
			name:   "extra live journal peer",
			reason: deliveryIdentityMismatchReason,
			options: abandonedRetryOptions{journalRows: func(ctx context.Context, db *journal.Journal) {
				_, err := db.PutSubmission(ctx, journal.Submission{
					ID: "prompt:live-peer", StreamID: "stream-live-peer",
					SessionID: abandonedRetrySessionID, IncarnationID: abandonedRetryIncarn,
					HarnessGeneration: uint64(abandonedRetryGeneration),
					Hash:              journal.SubmissionHash([]byte("live peer")),
					Payload:           []byte("live peer"), State: journal.SubmissionDispatching,
				})
				require.NoError(t, err)
			}},
		},
		{
			name:   "extra active SQL peer",
			reason: deliveryIdentityMismatchReason,
			options: abandonedRetryOptions{journalRows: func(ctx context.Context, db *journal.Journal) {
				payload := []byte(`{"text":"active peer"}`)
				_, err := db.PutSubmission(ctx, journal.Submission{
					ID: "prompt:active-peer", StreamID: "stream-active-peer",
					SessionID: abandonedRetrySessionID, IncarnationID: abandonedRetryIncarn,
					HarnessGeneration: uint64(abandonedRetryGeneration),
					Hash:              journal.SubmissionHash(payload),
					Payload:           payload, State: journal.SubmissionInterruptedUnknown,
				})
				require.NoError(t, err)
			}},
			extraSQL: func(t *testing.T, repo *tasksqlite.Repository) {
				_, err := repo.PrepareAgentDeliverySubmission(ctx, &models.AgentDeliverySubmission{
					ID: "prompt:active-peer", SessionID: abandonedRetrySessionID,
					IncarnationID: abandonedRetryIncarn, HarnessGeneration: abandonedRetryGeneration,
					OwnerGeneration: abandonedRetryGeneration, PayloadHash: journal.SubmissionHash([]byte(`{"text":"active peer"}`)),
					Payload: []byte(`{"text":"active peer"}`), State: models.DeliverySubmissionAccepted,
				})
				require.NoError(t, err)
			},
		},
		{
			name:   "target hash mismatch",
			reason: deliveryIdentityMismatchReason,
			options: abandonedRetryOptions{target: &journal.Submission{
				ID: abandonedRetrySubmission, StreamID: abandonedRetryStream,
				SessionID: abandonedRetrySessionID, IncarnationID: abandonedRetryIncarn,
				HarnessGeneration: uint64(abandonedRetryGeneration),
				Hash:              "foreign-hash", Payload: basePayload,
				State: journal.SubmissionInterruptedUnknown,
			}},
		},
		{
			name:    "owner is still alive",
			reason:  deliveryIdentityMismatchReason,
			options: abandonedRetryOptions{terminated: boolPointer(false)},
		},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := seedAbandonedRetry(t, scenario.options)
			if scenario.extraSQL != nil {
				scenario.extraSQL(t, fixture.repo)
			}
			response, err := fixture.service.RetrySessionDelivery(ctx, fixture.session.TaskID, fixture.session.ID)
			require.NoError(t, err)
			require.Equal(t, SessionDeliveryRecoveryBlocked, response.Outcome)
			require.Equal(t, scenario.reason, response.Reason)
			require.Zero(t, fixture.manager.retirementCalls)
			require.Empty(t, fixture.manager.capturedPromptCalls)
			fixture.requireUnsettled(t)
		})
	}
}

func boolPointer(value bool) *bool {
	return &value
}

func TestRetrySessionDeliveryAbandonedSettlementIsIdempotent(t *testing.T) {
	ctx := context.Background()
	fixture := seedAbandonedRetry(t, abandonedRetryOptions{})
	first, err := fixture.service.RetrySessionDelivery(ctx, fixture.session.TaskID, fixture.session.ID)
	require.NoError(t, err)
	require.Equal(t, SessionDeliveryRecoverySettled, first.Outcome)
	firstBlock, err := fixture.repo.GetSessionRecoveryBlock(ctx, abandonedRetryBlockID)
	require.NoError(t, err)
	require.NotNil(t, firstBlock.ResolvedAt)
	firstResolvedAt := *firstBlock.ResolvedAt

	second, err := fixture.service.RetrySessionDelivery(ctx, fixture.session.TaskID, fixture.session.ID)
	require.NoError(t, err)
	require.Equal(t, SessionDeliveryRecoverySettled, second.Outcome)
	require.Equal(t, 1, fixture.manager.retirementCalls, "a settled submission must not be retired again")
	require.Empty(t, fixture.manager.capturedPromptCalls)

	effect, err := fixture.repo.GetAgentDeliveryEffect(ctx, models.DeliveryAbandonedEffectKey(abandonedRetrySubmission))
	require.NoError(t, err)
	require.Equal(t, models.DeliveryAbandonedEffectType, effect.EffectType)
	secondBlock, err := fixture.repo.GetSessionRecoveryBlock(ctx, abandonedRetryBlockID)
	require.NoError(t, err)
	require.NotNil(t, secondBlock.ResolvedAt)
	require.True(t, secondBlock.ResolvedAt.Equal(firstResolvedAt),
		"a repeated retry must not rewrite the resolved block")
	recovery, found := fixture.sessionRecovery(t)
	require.True(t, found)
	require.Equal(t, models.AgentDeliveryRecoverySettled, recovery.Phase)
}

type abandonedSettlementFailOnceRepository struct {
	*tasksqlite.Repository
	remainingFailures int
}

func (r *abandonedSettlementFailOnceRepository) SettleAbandonedAgentDeliverySubmission(
	ctx context.Context,
	request *models.AgentDeliveryAbandonedSettlementRequest,
) (*models.AgentDeliveryAbandonedSettlementResult, error) {
	if r.remainingFailures > 0 {
		r.remainingFailures--
		return nil, errors.New("injected abandoned settlement failure")
	}
	return r.Repository.SettleAbandonedAgentDeliverySubmission(ctx, request)
}

func TestRetrySessionDeliveryConvergesAfterRetirementBeforeSQLFailure(t *testing.T) {
	ctx := context.Background()
	fixture := seedAbandonedRetry(t, abandonedRetryOptions{})
	_, err := fixture.service.messageQueue.QueueMessage(ctx, fixture.session.ID, fixture.session.TaskID,
		"queued prompt", "", messagequeue.QueuedByUser, false, nil)
	require.NoError(t, err)
	_, ok := fixture.service.messageQueue.ReserveQueued(ctx, fixture.session.ID)
	require.True(t, ok)
	wrapper := &abandonedSettlementFailOnceRepository{Repository: fixture.repo, remainingFailures: 1}
	fixture.service.repo = wrapper

	_, err = fixture.service.RetrySessionDelivery(ctx, fixture.session.TaskID, fixture.session.ID)
	require.Error(t, err, "the injected SQL failure must surface")
	require.Equal(t, 1, fixture.manager.retirementCalls)
	stored, err := journalOpenRetainedSubmission(t, fixture.manager.root, abandonedRetrySubmission)
	require.NoError(t, err)
	require.True(t, stored.Retired, "the journal retirement committed before the SQL failure")
	submission, err := fixture.repo.GetAgentDeliverySubmission(ctx, abandonedRetrySubmission)
	require.NoError(t, err)
	require.Equal(t, models.DeliverySubmissionInterruptedUnknown, submission.State)

	response, err := fixture.service.RetrySessionDelivery(ctx, fixture.session.TaskID, fixture.session.ID)
	require.NoError(t, err)
	require.Equal(t, SessionDeliveryRecoverySettled, response.Outcome, response.Reason)
	require.Equal(t, 1, fixture.manager.retirementCalls,
		"a retired target with an empty descriptor must not be retired again")

	submission, err = fixture.repo.GetAgentDeliverySubmission(ctx, abandonedRetrySubmission)
	require.NoError(t, err)
	require.Equal(t, models.DeliverySubmissionFailed, submission.State)
	require.Equal(t, "failed", submission.Outcome)
	effect, err := fixture.repo.GetAgentDeliveryEffect(ctx, models.DeliveryAbandonedEffectKey(abandonedRetrySubmission))
	require.NoError(t, err)
	require.Equal(t, models.DeliveryAbandonedEffectType, effect.EffectType)
	block, err := fixture.repo.GetSessionRecoveryBlock(ctx, abandonedRetryBlockID)
	require.NoError(t, err)
	require.Equal(t, models.RecoveryBlockResolved, block.State)
	recovery, found := fixture.sessionRecovery(t)
	require.True(t, found)
	require.Equal(t, models.AgentDeliveryRecoverySettled, recovery.Phase)
	pending, err := fixture.service.messageQueue.ListPendingQueueDispatches(ctx)
	require.NoError(t, err)
	require.Empty(t, pending, "the converging retry must release the parked queue dispatch")
}

func TestRetrySessionDeliveryAbandonedSettlementRejectsBeforeWrites(t *testing.T) {
	t.Run("archived task", func(t *testing.T) {
		ctx := context.Background()
		fixture := seedAbandonedRetry(t, abandonedRetryOptions{})
		require.NoError(t, fixture.repo.ArchiveTask(ctx, fixture.session.TaskID))
		_, err := fixture.service.RetrySessionDelivery(ctx, fixture.session.TaskID, fixture.session.ID)
		require.ErrorIs(t, err, executor.ErrTaskArchived)
		require.Zero(t, fixture.manager.inspectCalls, "archived tasks must be refused before any evidence read")
		require.Zero(t, fixture.manager.retirementCalls)
		require.Empty(t, fixture.manager.capturedPromptCalls)
	})
	t.Run("unauthorized session control", func(t *testing.T) {
		ctx := context.Background()
		fixture := seedAbandonedRetry(t, abandonedRetryOptions{})
		fixture.service.sessionControlCheck = func(context.Context, string) error {
			return errors.New("denied")
		}
		_, err := fixture.service.RetrySessionDelivery(ctx, fixture.session.TaskID, fixture.session.ID)
		require.Error(t, err)
		require.Zero(t, fixture.manager.inspectCalls, "authorization must run before any evidence read")
		require.Zero(t, fixture.manager.retirementCalls)
		require.Empty(t, fixture.manager.capturedPromptCalls)
	})
}
