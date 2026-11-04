package lifecycle

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kandev/kandev/internal/agentctl/journal"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/stretchr/testify/require"
)

// A settled abandoned delivery leaves its journal record retired and its SQL
// row failed without terminal evidence. Native resume must ignore that retired
// record instead of demanding terminal proof that does not exist.
func TestNativeResumeIgnoresRetiredPeerAfterAbandonedSettlement(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		retirePeer bool
	}{
		{name: "retired peer converges", retirePeer: true},
		{name: "unretired peer still requires terminal evidence", retirePeer: false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := context.Background()
			store, err := journal.Open(journal.Config{Path: filepath.Join(t.TempDir(), "delivery.bbolt")})
			require.NoError(t, err)
			t.Cleanup(func() { _ = store.Close() })
			payload := []byte("abandoned instruction")
			peer := journal.Submission{
				ID: "prompt:abandoned", SessionID: "session", IncarnationID: "incarnation",
				HarnessGeneration: 2, StreamID: "stream", Hash: journal.SubmissionHash(payload),
				Payload: payload, State: journal.SubmissionInterruptedUnknown,
			}
			_, err = store.PutSubmission(ctx, peer)
			require.NoError(t, err)
			completedPayload := []byte("completed history")
			_, err = store.PutSubmission(ctx, journal.Submission{
				ID: "prompt:completed-history", SessionID: "session", IncarnationID: "incarnation",
				HarnessGeneration: 2, StreamID: "stream", Hash: journal.SubmissionHash(completedPayload),
				Payload: completedPayload, State: journal.SubmissionCompleted,
				TerminalEventRetained: true, TerminalSequence: 1,
			})
			require.NoError(t, err)
			if scenario.retirePeer {
				_, err = store.RetireSubmission(ctx, peer.ID, peer.HarnessGeneration)
				require.NoError(t, err)
			}

			repo, _ := runOwnerTestRepository(t)
			require.NoError(t, repo.CreateTask(ctx, &models.Task{ID: "task", Title: "Retired peer"}))
			require.NoError(t, repo.CreateTaskSession(ctx, &models.TaskSession{
				ID: "session", TaskID: "task", QueueIncarnationID: "incarnation",
			}))
			_, err = repo.PrepareAgentDeliverySubmission(ctx, &models.AgentDeliverySubmission{
				ID: peer.ID, SessionID: peer.SessionID, IncarnationID: peer.IncarnationID,
				HarnessGeneration: int64(peer.HarnessGeneration), OwnerGeneration: int64(peer.HarnessGeneration),
				PayloadHash: peer.Hash, Payload: peer.Payload,
				State: models.DeliverySubmissionFailed, Outcome: string(models.DeliverySubmissionFailed),
			})
			require.NoError(t, err)

			manager := newTestManager(t)
			manager.SetAgentDeliveryRepository(repo)
			execution := &AgentExecution{
				ID: "replacement", SessionID: "session", ACPSessionID: "saved-native",
				DeliveryIncarnationID: "incarnation", DeliveryHarnessGeneration: 2,
				DeliveryStreamID: "stream", agentctl: nativeResumeJournalPeer(t, store, false),
			}
			execution.setSessionInitialized(true)
			require.NoError(t, manager.executionStore.Add(execution))

			err = manager.AcknowledgeNativeResumeDelivery(ctx, "session")
			status, statusErr := execution.agentctl.GetDeliveryStatus(ctx, "stream")
			require.NoError(t, statusErr)
			if !scenario.retirePeer {
				require.ErrorContains(t, err, "terminal evidence disagrees",
					"an unretired peer must keep the existing terminal-evidence requirement")
				require.True(t, status.Unresolved)
				return
			}
			require.NoError(t, err)
			require.False(t, status.Unresolved)
			require.NoError(t, manager.AcknowledgeNativeResumeDelivery(ctx, "session"),
				"repeated native resume must converge after settlement")
			stored, err := store.GetSubmission(ctx, peer.ID)
			require.NoError(t, err)
			require.True(t, stored.Retired)
			require.Equal(t, journal.SubmissionInterruptedUnknown, stored.State)
			require.Equal(t, peer.Payload, stored.Payload)
		})
	}
}
