package orchestrator

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/agent/runtime/lifecycle"
	"github.com/kandev/kandev/internal/orchestrator/executor"
	"github.com/kandev/kandev/internal/task/models"
	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
	"github.com/stretchr/testify/require"
)

// successorExecutionRecoveryManager records identity recovery calls so a test
// can tell a rejected execution identity from a runtime-decided outcome.
type successorExecutionRecoveryManager struct {
	*mockAgentManager
	identityCalls int
	result        lifecycle.DeliveryReconciliationResult
}

func (m *successorExecutionRecoveryManager) RecoverAgentPromptStreamWithIdentity(
	context.Context,
	lifecycle.AgentDeliveryRecoveryIdentity,
) lifecycle.DeliveryReconciliationResult {
	m.identityCalls++
	return m.result
}

var _ executor.AgentManagerClient = (*successorExecutionRecoveryManager)(nil)

type successorDeliveryFixture struct {
	taskID, sessionID, incarnationID string
	submissionID, streamID           string
}

func seedUncertainDeliveryRecovery(t *testing.T, repo *sqliterepo.Repository, fixture successorDeliveryFixture) *models.TaskSession {
	t.Helper()
	ctx := context.Background()
	seedSession(t, repo, fixture.taskID, fixture.sessionID, "step-1", fixture.incarnationID)
	seedExecutorRunning(t, repo, fixture.sessionID, fixture.taskID, "old")
	session, err := repo.GetTaskSession(ctx, fixture.sessionID)
	require.NoError(t, err)
	require.Equal(t, "old", session.AgentExecutionID)
	_, err = repo.PrepareAgentDeliverySubmission(ctx, &models.AgentDeliverySubmission{
		ID: fixture.submissionID, SessionID: fixture.sessionID, IncarnationID: fixture.incarnationID,
		HarnessGeneration: 1, OwnerGeneration: 1, PayloadHash: "hash-" + fixture.submissionID,
		Payload: []byte(`{"text":"uncertain instruction"}`), State: models.DeliverySubmissionDispatching,
	})
	require.NoError(t, err)
	return session
}

func upsertUncertainDeliveryRecovery(t *testing.T, repo *sqliterepo.Repository, session *models.TaskSession, fixture successorDeliveryFixture) {
	t.Helper()
	recovery := models.AgentDeliveryRecovery{
		SessionID: session.ID, AgentExecutionID: "old", IncarnationID: fixture.incarnationID,
		HarnessGeneration: 1, SubmissionID: fixture.submissionID, StreamID: fixture.streamID,
		PromptGeneration: 1, Phase: models.AgentDeliveryRecoveryUncertain,
	}
	block := &models.SessionRecoveryBlock{
		SessionID: session.ID, IncarnationID: recovery.IncarnationID,
		ExpectedGeneration: recovery.HarnessGeneration, Reason: "unknown_prompt_outcome",
		State: models.RecoveryBlockOpen, ConsumerReference: "agent_delivery",
		DeliverySubmissionID: recovery.SubmissionID, DeliveryStreamID: recovery.StreamID,
	}
	changed, err := repo.UpsertAgentDeliveryRecovery(context.Background(), &recovery, block)
	require.NoError(t, err)
	require.True(t, changed)
}

// rewriteStoredRecovery replaces the persisted recovery snapshot after the
// store wrote it, standing in for stale metadata left by an earlier incarnation
// or generation.
func rewriteStoredRecovery(t *testing.T, repo *sqliterepo.Repository, sessionID string, mutate func(*models.AgentDeliveryRecovery)) {
	t.Helper()
	ctx := context.Background()
	session, err := repo.GetTaskSession(ctx, sessionID)
	require.NoError(t, err)
	recovery, ok := models.LoadAgentDeliveryRecovery(session.Metadata)
	require.True(t, ok)
	mutate(&recovery)
	metadata := session.Metadata
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	metadata[models.SessionMetaKeyAgentDeliveryRecovery] = recovery
	require.NoError(t, repo.UpdateTaskSessionWithMetadata(ctx, session, metadata))
}

func TestRetrySessionDeliveryAcceptsSuccessorExecution(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	fixture := successorDeliveryFixture{
		taskID: "task-successor", sessionID: "session-successor", incarnationID: "incarnation-successor",
		submissionID: "submission-successor", streamID: "stream-successor",
	}
	session := seedUncertainDeliveryRecovery(t, repo, fixture)
	upsertUncertainDeliveryRecovery(t, repo, session, fixture)
	seedExecutorRunning(t, repo, fixture.sessionID, fixture.taskID, "successor")
	manager := &successorExecutionRecoveryManager{
		mockAgentManager: &mockAgentManager{repoForExecutionLookup: repo},
		result: lifecycle.DeliveryReconciliationResult{
			Outcome: lifecycle.DeliveryReconciliationUncertain,
			Reason:  "terminal_outcome_missing",
		},
	}
	service := createTestServiceWithAgent(repo, newMockStepGetter(), newMockTaskRepo(), manager)

	response, err := service.RetrySessionDelivery(ctx, session.TaskID, session.ID)
	require.NoError(t, err)
	require.NotEqual(t, "recovery_identity_mismatch", response.Reason)
	require.Equal(t, SessionDeliveryRecoveryUncertain, response.Outcome)
	require.Equal(t, 1, manager.identityCalls,
		"the successor execution must reach runtime identity reconciliation")
}

func TestRetrySessionDeliveryRejectsNonExecutionIdentityMismatch(t *testing.T) {
	cases := map[string]func(t *testing.T, repo *sqliterepo.Repository, recovery *models.AgentDeliveryRecovery){
		"incarnation": func(_ *testing.T, _ *sqliterepo.Repository, recovery *models.AgentDeliveryRecovery) {
			recovery.IncarnationID = "other-incarnation"
		},
		"generation": func(t *testing.T, repo *sqliterepo.Repository, recovery *models.AgentDeliveryRecovery) {
			// A newer harness generation is now current; the stored recovery
			// still names the interrupted generation.
			require.NoError(t, repo.CreateHarnessSessionGeneration(context.Background(), &models.HarnessSessionGeneration{
				SessionID: recovery.SessionID, IncarnationID: recovery.IncarnationID,
				Generation: 2, PredecessorGeneration: 1,
				NativeSessionID: "native-successor", CreationReason: "silent_restart_restored",
			}))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			repo := setupTestRepo(t)
			fixture := successorDeliveryFixture{
				taskID: "task-mismatch", sessionID: "session-mismatch", incarnationID: "incarnation-mismatch",
				submissionID: "submission-mismatch", streamID: "stream-mismatch",
			}
			session := seedUncertainDeliveryRecovery(t, repo, fixture)
			upsertUncertainDeliveryRecovery(t, repo, session, fixture)
			rewriteStoredRecovery(t, repo, session.ID, func(recovery *models.AgentDeliveryRecovery) {
				mutate(t, repo, recovery)
			})
			manager := &successorExecutionRecoveryManager{
				mockAgentManager: &mockAgentManager{repoForExecutionLookup: repo},
				result: lifecycle.DeliveryReconciliationResult{
					Outcome: lifecycle.DeliveryReconciliationUncertain,
					Reason:  "terminal_outcome_missing",
				},
			}
			service := createTestServiceWithAgent(repo, newMockStepGetter(), newMockTaskRepo(), manager)

			response, err := service.RetrySessionDelivery(ctx, session.TaskID, session.ID)
			require.NoError(t, err)
			require.Equal(t, SessionDeliveryRecoveryBlocked, response.Outcome)
			require.Equal(t, "recovery_identity_mismatch", response.Reason)
			require.Zero(t, manager.identityCalls,
				"a non-execution identity mismatch must never reach runtime reconciliation")
		})
	}
}
