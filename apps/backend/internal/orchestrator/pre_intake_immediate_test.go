package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	agentruntime "github.com/kandev/kandev/internal/agent/runtime"
	"github.com/kandev/kandev/internal/task/models"
	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/stretchr/testify/require"
)

func seedRejectedPreIntakeSubmission(t *testing.T, repo *sqliterepo.Repository) (*models.TaskSession, string) {
	t.Helper()
	ctx := context.Background()
	seedTaskAndSession(t, repo, "task-pre-intake-immediate", "session-pre-intake-immediate", models.TaskSessionStateWaitingForInput)
	session, err := repo.GetTaskSession(ctx, "session-pre-intake-immediate")
	require.NoError(t, err)
	incarnationID := session.QueueIncarnationID
	if incarnationID == "" {
		incarnationID = session.ID
	}
	require.NoError(t, repo.CreateHarnessSessionGeneration(ctx, &models.HarnessSessionGeneration{
		SessionID: session.ID, IncarnationID: incarnationID, Generation: 1, CreationReason: "initial",
	}))
	const submissionID = "prompt:send-now:immediate"
	now := time.Now().UTC()
	_, err = repo.PrepareAgentDeliverySubmission(ctx, &models.AgentDeliverySubmission{
		ID: submissionID, SessionID: session.ID, IncarnationID: incarnationID,
		HarnessGeneration: 1, OwnerGeneration: 1, DispatchAttemptID: "message-pre-intake-immediate",
		PayloadHash: "pre-intake-immediate-hash", Payload: []byte(`{"text":"refused"}`),
		State: models.DeliverySubmissionDispatching, Outcome: "dispatching", CreatedAt: now, UpdatedAt: now,
	})
	require.NoError(t, err)
	return session, submissionID
}

func dispatchRejectedPrompt(t *testing.T, svc *Service, repo *sqliterepo.Repository, session *models.TaskSession, submissionID string) error {
	t.Helper()
	delivery := &agentDeliverySubmissionRuntime{store: repo, id: submissionID, sessionID: session.ID}
	_, err := svc.finishPromptExecutorDispatch(
		context.Background(), session.TaskID, session.ID, "refused prompt", false, false, nil,
		promptClaimRollback{previousSessionState: models.TaskSessionStateWaitingForInput},
		promptTaskOptions{}, nil, nil, nil,
		fmt.Errorf("agent refused prompt: %w", agentruntime.ErrDurableDeliveryAdmissionRejected),
		nil, nil, delivery,
	)
	return err
}

func TestPromptDispatchFailureSettlesRejectedSubmissionImmediately(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	session, submissionID := seedRejectedPreIntakeSubmission(t, repo)
	svc := createTestServiceWithScheduler(repo, newMockStepGetter(), newMockTaskRepo(), &mockAgentManager{})

	err := dispatchRejectedPrompt(t, svc, repo, session, submissionID)
	require.Error(t, err, "the prompt failure must still surface to the caller")

	submission, err := repo.GetAgentDeliverySubmission(ctx, submissionID)
	require.NoError(t, err)
	require.Equal(t, models.DeliverySubmissionFailed, submission.State)
	require.Equal(t, "failed", submission.Outcome)
	require.Zero(t, submission.TerminalSequence)
	blocks, err := repo.ListOpenSessionRecoveryBlocks(ctx, session.ID, session.QueueIncarnationID, 1)
	require.NoError(t, err)
	require.Empty(t, blocks, "a definitive pre-intake rejection must not create a recovery block")
}

type failingPreIntakeSettlementRepo struct {
	*sqliterepo.Repository
	err error
}

func (r *failingPreIntakeSettlementRepo) SettlePreIntakeAgentDeliverySubmission(
	context.Context,
	*models.AgentDeliveryPreIntakeSettlementRequest,
) (*models.AgentDeliveryPreIntakeSettlementResult, error) {
	return nil, r.err
}

func TestPromptDispatchFailureKeepsUncertainWhenSettlementFails(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	session, submissionID := seedRejectedPreIntakeSubmission(t, repo)
	svc := createTestServiceWithScheduler(repo, newMockStepGetter(), newMockTaskRepo(), &mockAgentManager{})
	settlementErr := errors.New("settlement storage unavailable")
	svc.repo = &failingPreIntakeSettlementRepo{Repository: repo, err: settlementErr}

	err := dispatchRejectedPrompt(t, svc, repo, session, submissionID)
	require.ErrorIs(t, err, settlementErr, "a failed settlement must be reported alongside the dispatch failure")

	submission, err := repo.GetAgentDeliverySubmission(ctx, submissionID)
	require.NoError(t, err)
	require.Equal(t, models.DeliverySubmissionInterruptedUnknown, submission.State)
	require.Equal(t, "prompt_dispatch_failed", submission.Outcome)
	blocks, err := repo.ListOpenSessionRecoveryBlocks(ctx, session.ID, session.QueueIncarnationID, 1)
	require.NoError(t, err)
	require.Len(t, blocks, 1, "a failed settlement must fall back to the uncertain recovery block")
}
