package orchestrator

import (
	"context"
	"testing"

	agentruntime "github.com/kandev/kandev/internal/agent/runtime"
	"github.com/kandev/kandev/internal/agentctl/journal"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

type blockedDeliveryCapabilityManager struct {
	*mockAgentManager
	capability agentruntime.DurableDeliveryCapability
}

func (m *blockedDeliveryCapabilityManager) DurableDeliveryCapabilityForExecution(
	context.Context,
	string,
) (agentruntime.DurableDeliveryCapability, bool) {
	return m.capability, true
}

func TestResolveDeliveryProtocolBlockedAdmissionCreatesNoSubmissionOrBlock(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedTaskAndSession(t, repo, "task-blocked-admission", "session-blocked-admission", models.TaskSessionStateWaitingForInput)
	session, err := repo.GetTaskSession(ctx, "session-blocked-admission")
	require.NoError(t, err)
	manager := &blockedDeliveryCapabilityManager{
		mockAgentManager: &mockAgentManager{},
		capability: agentruntime.DurableDeliveryCapability{
			Version: journal.CurrentVersion, Durable: true, AdmissionBlocked: true,
			Reason: deliveryAdmissionBlockedReason,
		},
	}
	svc := createTestServiceWithAgent(repo, newMockStepGetter(), newMockTaskRepo(), manager)

	_, err = svc.prepareAgentDeliverySubmission(ctx, session, "execution-blocked", "blocked prompt", nil, promptTaskOptions{})
	require.ErrorIs(t, err, errDeliveryAdmissionBlocked)
	require.NotErrorIs(t, err, agentruntime.ErrDeliveryRecoveryBlocked,
		"a blocked admission is transient and must not request durable recovery")

	submissions, err := repo.ListAgentDeliverySubmissions(ctx, session.ID)
	require.NoError(t, err)
	require.Empty(t, submissions, "a blocked admission must not create a SQL submission")

	blocks, err := repo.ListOpenSessionRecoveryBlocks(ctx, session.ID, session.QueueIncarnationID, 1)
	require.NoError(t, err)
	require.Empty(t, blocks, "a blocked admission must not create a recovery block")
}
