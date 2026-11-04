package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/agent/runtime/lifecycle"
	"github.com/kandev/kandev/internal/agentctl/journal"
	"github.com/kandev/kandev/internal/common/processidentity"
	"github.com/kandev/kandev/internal/orchestrator/executor"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
	v1 "github.com/kandev/kandev/pkg/api/v1"
)

// journalBackedRetirementManager reuses the Task-5 fake manager and replaces
// its canned retirement with the real journal write. Evidence is inspected
// before anything changes, mirroring
// lifecycle.Manager.RetireNeverStartedDeliverySubmission: the request must
// name the owner identity the journal was seeded with (the proven-dead
// agentctl birth identity), the stored record must carry the exact requested
// identity without terminal evidence, and the complete unresolved inventory
// must consist of exactly the target. The one authorized write goes through
// journal.RetireSubmissionWithEvidence with the submission ID as the
// zero-event authorization.
type journalBackedRetirementManager struct {
	*neverStartedRetirementManager

	journal *journal.Journal
	owner   processidentity.Identity
}

// RetireNeverStartedDeliverySubmission retires the exact journal record of a
// never-started prompt through the real journal, after the evidence checks the
// lifecycle method enforces. A replay after the record is retired converges
// without another journal write.
func (m *journalBackedRetirementManager) RetireNeverStartedDeliverySubmission(
	ctx context.Context,
	request lifecycle.NeverStartedDeliveryRetirementRequest,
) (*lifecycle.NeverStartedDeliveryRetirementResult, error) {
	m.mu.Lock()
	m.retireRequests = append(m.retireRequests, request)
	m.mu.Unlock()
	if !journalRetirementRequestValid(request) {
		return nil, errors.Join(lifecycle.ErrDeliveryOwnerMismatch, lifecycle.ErrDeliveryRecoveryBlocked)
	}
	if request.OriginalRuntime != m.owner {
		return nil, errors.Join(lifecycle.ErrDeliveryOwnerMismatch, lifecycle.ErrDeliveryRecoveryBlocked)
	}
	stored, err := m.journal.GetSubmission(ctx, request.SubmissionID)
	if err != nil {
		return nil, errors.Join(lifecycle.ErrDeliveryRecoveryBlocked, err)
	}
	if !journalRecordMatchesRequest(stored, request) {
		return nil, errors.Join(lifecycle.ErrDeliveryRecordEvidenceConflict, lifecycle.ErrDeliveryRecoveryBlocked)
	}
	descriptor, err := m.journal.RecoveryDescriptor(
		ctx, request.SessionID, request.IncarnationID, request.HarnessGeneration, "")
	if err != nil {
		return nil, errors.Join(lifecycle.ErrDeliveryRecoveryBlocked, err)
	}
	if stored.Retired && journalInventoryResolved(descriptor) {
		return journalRetirementResult(stored, descriptor), nil
	}
	if err := journalInventoryTargetsStored(descriptor, stored); err != nil {
		return nil, err
	}
	retired, err := m.journal.RetireSubmissionWithEvidence(
		ctx, request.SubmissionID, request.HarnessGeneration, request.SubmissionID)
	if err != nil {
		return nil, errors.Join(lifecycle.ErrDeliveryRecoveryBlocked, err)
	}
	refreshed, err := m.journal.RecoveryDescriptor(
		ctx, request.SessionID, request.IncarnationID, request.HarnessGeneration, "")
	if err != nil {
		return nil, errors.Join(lifecycle.ErrDeliveryRecoveryBlocked, err)
	}
	return journalRetirementResult(retired, refreshed), nil
}

func journalRetirementRequestValid(request lifecycle.NeverStartedDeliveryRetirementRequest) bool {
	return request.TaskID != "" && request.SessionID != "" && request.ExecutionID != "" &&
		request.IncarnationID != "" && request.HarnessGeneration > 0 &&
		request.SubmissionID != "" && request.StreamID != "" && request.PayloadHash != ""
}

// journalRecordMatchesRequest mirrors lifecycle's
// neverStartedDeliveryRecordMatches: the stored record must carry the exact
// requested identity with no terminal evidence, because a dispatch state label
// alone is not evidence.
func journalRecordMatchesRequest(
	stored journal.Submission, request lifecycle.NeverStartedDeliveryRetirementRequest,
) bool {
	if stored.ID != request.SubmissionID || stored.SessionID != request.SessionID ||
		stored.IncarnationID != request.IncarnationID || stored.HarnessGeneration != request.HarnessGeneration ||
		stored.StreamID != request.StreamID || stored.Hash != request.PayloadHash {
		return false
	}
	return !stored.TerminalEventRetained && stored.TerminalSequence == 0
}

// journalInventoryTargetsStored mirrors lifecycle's
// neverStartedDeliveryInventoryTarget: the complete unresolved inventory must
// consist of exactly the stored target, untruncated, with no terminal
// evidence.
func journalInventoryTargetsStored(descriptor journal.RecoveryDescriptor, stored journal.Submission) error {
	if descriptor.SubmissionsTruncated || descriptor.SubmissionCount != 1 || len(descriptor.Submissions) != 1 {
		return errors.Join(lifecycle.ErrDeliveryRecordEvidenceAmbiguous, lifecycle.ErrDeliveryRecoveryBlocked)
	}
	summary := descriptor.Submissions[0]
	if summary.ID != stored.ID || summary.StreamID != stored.StreamID || summary.Hash != stored.Hash ||
		summary.HarnessGeneration != stored.HarnessGeneration || summary.State != stored.State ||
		summary.TerminalEventRetained || summary.TerminalSequence != 0 {
		return errors.Join(lifecycle.ErrDeliveryRecordEvidenceConflict, lifecycle.ErrDeliveryRecoveryBlocked)
	}
	return nil
}

// journalInventoryResolved mirrors lifecycle's
// neverStartedDeliveryInventoryResolved: the converged post-retirement state
// has no unresolved submissions and no unacknowledged stream work.
func journalInventoryResolved(descriptor journal.RecoveryDescriptor) bool {
	return !descriptor.Unresolved && descriptor.SubmissionCount == 0 && len(descriptor.Submissions) == 0
}

func journalRetirementResult(
	retired journal.Submission, descriptor journal.RecoveryDescriptor,
) *lifecycle.NeverStartedDeliveryRetirementResult {
	retired.Payload = nil
	return &lifecycle.NeverStartedDeliveryRetirementResult{
		Retired: retired, Descriptor: descriptor, ProcessTerminated: true,
	}
}

// neverStartedStallIntegrationFixture seeds one durable never-started stall
// across a real SQLite repository and a real bbolt journal: a RUNNING session
// with an open turn, a dispatching SQL submission plus its open agent_delivery
// recovery block, and a dispatching journal submission carrying the exact same
// payload identity and hash under the captured owner identity. The agent
// manager wraps the Task-5 fake so the orchestrator's type-asserted retirement
// seam exercises the real journal write.
type neverStartedStallIntegrationFixture struct {
	ctx           context.Context
	repo          *sqliterepo.Repository
	svc           *Service
	manager       *journalBackedRetirementManager
	journal       *journal.Journal
	messages      *mockMessageCreator
	taskRepo      *mockTaskRepo
	taskID        string
	sessionID     string
	executionID   string
	submissionID  string
	incarnationID string
	streamID      string
	blockID       string
	turnID        string
	promptPayload []byte
	payloadHash   string
	payload       lifecycle.AgentStalledPayload
	launches      chan struct{}
}

func newNeverStartedStallIntegrationFixture(
	t *testing.T, suffix string, wireAutoResume bool,
) *neverStartedStallIntegrationFixture {
	t.Helper()
	ctx := context.Background()
	taskID := "ns-e2e-task-" + suffix
	sessionID := "ns-e2e-session-" + suffix
	incarnationID := "ns-e2e-incarnation-" + suffix
	executionID := "ns-e2e-execution-" + suffix
	submissionID := "prompt:ns-e2e-" + suffix
	streamID := "ns-e2e-stream-" + suffix
	blockID := "ns-e2e-block-" + suffix
	promptPayload := []byte(fmt.Sprintf(`{"text":"never started e2e prompt %s"}`, suffix))
	payloadHash := journal.SubmissionHash(promptPayload)
	ownerIdentity := processidentity.Identity{PID: 4242, BirthToken: "ns-e2e-birth-" + suffix}

	repo := setupTestRepo(t)
	seedSession(t, repo, taskID, sessionID, "", incarnationID)
	session, err := repo.GetTaskSession(ctx, sessionID)
	require.NoError(t, err)
	session.AgentProfileID = "ns-e2e-profile"
	require.NoError(t, repo.UpdateTaskSession(ctx, session))
	require.NoError(t, repo.CreateHarnessSessionGeneration(ctx, &models.HarnessSessionGeneration{
		SessionID: sessionID, IncarnationID: incarnationID,
		Generation: neverStartedStallGeneration, CreationReason: "test",
	}))
	now := time.Now().UTC()
	_, err = repo.PrepareAgentDeliverySubmission(ctx, &models.AgentDeliverySubmission{
		ID: submissionID, SessionID: sessionID, IncarnationID: incarnationID,
		HarnessGeneration: neverStartedStallGeneration, OwnerGeneration: neverStartedStallGeneration,
		DispatchAttemptID: "ns-e2e-dispatch-" + suffix,
		PayloadHash:       payloadHash, Payload: promptPayload,
		State: models.DeliverySubmissionDispatching, Outcome: "dispatching",
		CreatedAt: now, UpdatedAt: now,
	})
	require.NoError(t, err)
	require.NoError(t, repo.UpsertSessionRecoveryBlock(ctx, &models.SessionRecoveryBlock{
		ID: blockID, SessionID: sessionID, IncarnationID: incarnationID,
		ExpectedGeneration: neverStartedStallGeneration, Reason: "unknown_prompt_outcome",
		State: models.RecoveryBlockOpen, ConsumerReference: "agent_delivery",
		DeliverySubmissionID: submissionID,
	}))

	deliveryJournal, err := journal.Open(journal.Config{Path: filepath.Join(t.TempDir(), "journal.bbolt")})
	require.NoError(t, err)
	t.Cleanup(func() { _ = deliveryJournal.Close() })
	_, err = deliveryJournal.PutSubmission(ctx, journal.Submission{
		ID: submissionID, SessionID: sessionID, IncarnationID: incarnationID,
		HarnessGeneration: neverStartedStallGeneration, StreamID: streamID,
		Hash: payloadHash, Payload: promptPayload, State: journal.SubmissionDispatching,
	})
	require.NoError(t, err)

	manager := &journalBackedRetirementManager{
		neverStartedRetirementManager: &neverStartedRetirementManager{mockAgentManager: &mockAgentManager{
			repoForExecutionLookup:   repo,
			currentPromptExecutionID: executionID,
		}},
		journal: deliveryJournal,
		owner:   ownerIdentity,
	}
	// Prompt-ready but not running: the executor's session lookup treats the
	// stopped agent as absent (forcing a cold resume to LaunchAgent), while the
	// resume's post-launch readiness probe succeeds immediately.
	manager.isAgentReadyFn = func(context.Context, string) bool { return true }
	manager.currentPromptGeneration.Store(neverStartedStallPromptGen)
	manager.currentPromptActivityEpoch.Store(1)
	// Mirror production teardown: a successful force-stop removes the execution
	// from the runtime's store, so the settlement re-decides ownership from
	// durable rows.
	manager.stopAgentWithReasonFunc = func(context.Context, string, string, bool) error {
		manager.mu.Lock()
		manager.currentPromptExecutionID = ""
		manager.mu.Unlock()
		return nil
	}
	taskRepo := newMockTaskRepo()
	seedMockTaskState(taskRepo, taskID, v1.TaskStateInProgress)
	svc := createTestServiceWithScheduler(repo, newMockStepGetter(), taskRepo, manager)
	svc.turnService = &repoTurnService{repo: repo}
	turn, err := svc.turnService.StartTurn(ctx, sessionID)
	require.NoError(t, err)
	messages := &mockMessageCreator{}
	svc.messageCreator = messages

	f := &neverStartedStallIntegrationFixture{
		ctx: ctx, repo: repo, svc: svc, manager: manager, journal: deliveryJournal,
		messages: messages, taskRepo: taskRepo,
		taskID: taskID, sessionID: sessionID, executionID: executionID,
		submissionID: submissionID, incarnationID: incarnationID, streamID: streamID,
		blockID: blockID, turnID: turn.ID,
		promptPayload: promptPayload, payloadHash: payloadHash,
		launches: make(chan struct{}, 8),
		payload: lifecycle.AgentStalledPayload{
			AgentExecutionID: executionID, TaskID: taskID, SessionID: sessionID,
			PromptGeneration: neverStartedStallPromptGen, ActivityEpoch: 1,
			StalledFor: 5 * time.Minute, NeverStarted: true,
			DeliverySubmissionID:      submissionID,
			DeliveryStreamID:          streamID,
			DeliveryIncarnationID:     incarnationID,
			DeliveryHarnessGeneration: neverStartedStallGeneration,
			OriginalRuntime:           ownerIdentity,
		},
	}
	f.installAutoResumeLaunch(t, wireAutoResume)
	return f
}

// installAutoResumeLaunch installs the launch observability for the
// auto-resume assertion. The record-only variant catches unexpected launches
// in the blocked and advisory tests; the completing variant (plus the seeded
// executors_running row) lets the success test's auto-resume reach LaunchAgent
// and settle the session back to WAITING_FOR_INPUT so the resume's bounded
// readiness waits return promptly.
func (f *neverStartedStallIntegrationFixture) installAutoResumeLaunch(t *testing.T, completing bool) {
	t.Helper()
	f.manager.launchAgentFunc = func(
		context.Context,
		*executor.LaunchAgentRequest,
	) (*executor.LaunchAgentResponse, error) {
		f.launches <- struct{}{}
		launched := "ns-e2e-unexpected-" + f.executionID
		if completing {
			launched = "ns-e2e-resumed-" + f.executionID
			_ = f.repo.UpsertExecutorRunning(context.Background(), &models.ExecutorRunning{
				ID: f.sessionID, SessionID: f.sessionID, TaskID: f.taskID,
				AgentExecutionID: launched, Status: "ready",
			})
			_ = f.repo.UpdateTaskSessionState(context.Background(), f.sessionID,
				models.TaskSessionStateWaitingForInput, "")
		}
		return &executor.LaunchAgentResponse{
			AgentExecutionID: launched, Status: v1.AgentStatusReady,
		}, nil
	}
	if completing {
		seedExecutorRunning(t, f.repo, f.sessionID, f.taskID, f.executionID)
	}
}

// awaitSettlementDrained blocks until the detached settlement goroutine for
// the fixture's payload releases its in-memory dedupe entry, i.e. until the
// whole stop + settle + outcome + auto-resume sequence has finished.
func (f *neverStartedStallIntegrationFixture) awaitSettlementDrained(t *testing.T) {
	t.Helper()
	key := neverStartedStallSettlementKey(f.payload)
	require.Eventually(t, func() bool {
		_, active := neverStartedStallSettlements.Load(key)
		return !active
	}, 5*time.Second, 5*time.Millisecond, "never-started stall settlement did not finish")
}

// seedJournalUnresolvedNoise pushes the journal's unresolved inventory past the
// recovery-summary bound so the descriptor the retirement inspects is
// truncated: the complete inventory can no longer be proven to consist of the
// target alone.
func (f *neverStartedStallIntegrationFixture) seedJournalUnresolvedNoise(t *testing.T) {
	t.Helper()
	for i := 0; i < journal.MaxRecoverySubmissionSummaries; i++ {
		_, err := f.journal.PutSubmission(f.ctx, journal.Submission{
			ID: fmt.Sprintf("prompt:ns-e2e-noise-%d", i), SessionID: f.sessionID,
			IncarnationID: f.incarnationID, HarnessGeneration: neverStartedStallGeneration,
			StreamID: f.streamID, Hash: fmt.Sprintf("ns-e2e-noise-hash-%d", i),
			Payload: []byte(fmt.Sprintf(`{"text":"noise %d"}`, i)),
			State:   journal.SubmissionDispatching,
		})
		require.NoError(t, err)
	}
}

func (f *neverStartedStallIntegrationFixture) requireJournalSubmission(t *testing.T) journal.Submission {
	t.Helper()
	stored, err := f.journal.GetSubmission(f.ctx, f.submissionID)
	require.NoError(t, err)
	return stored
}

func (f *neverStartedStallIntegrationFixture) requireSubmission(t *testing.T) *models.AgentDeliverySubmission {
	t.Helper()
	submission, err := f.repo.GetAgentDeliverySubmission(f.ctx, f.submissionID)
	require.NoError(t, err)
	return submission
}

func (f *neverStartedStallIntegrationFixture) requireSession(t *testing.T) *models.TaskSession {
	t.Helper()
	session, err := f.repo.GetTaskSession(f.ctx, f.sessionID)
	require.NoError(t, err)
	return session
}

func (f *neverStartedStallIntegrationFixture) requireBlockState(t *testing.T) string {
	t.Helper()
	block, err := f.repo.GetSessionRecoveryBlock(f.ctx, f.blockID)
	require.NoError(t, err)
	require.NotNil(t, block)
	return block.State
}

func (f *neverStartedStallIntegrationFixture) requireNoLaunch(t *testing.T) {
	t.Helper()
	select {
	case <-f.launches:
		t.Fatal("auto-resume launched the agent unexpectedly")
	default:
	}
}

func (f *neverStartedStallIntegrationFixture) requireOneLaunch(t *testing.T) {
	t.Helper()
	select {
	case <-f.launches:
	default:
		t.Fatal("expected exactly one auto-resume launch, got none")
	}
	f.requireNoLaunch(t)
}

// TestNeverStartedStallEndToEndSettlesJournalAndSQL covers the success path of
// the whole never-started settlement against a real SQLite repository and a
// real bbolt journal: the journal record is retired through the authorized
// zero-event write with no terminal evidence, the SQL submission settles to
// failed/failed with the settlement effect, the bound recovery block resolves,
// a follow-up prompt admission is no longer fenced, the session recovers to
// WAITING_FOR_INPUT, the task reconciles to REVIEW, the captured turn
// completes, exactly one "not delivered" notice is posted, and one auto-resume
// launch is attempted.
func TestNeverStartedStallEndToEndSettlesJournalAndSQL(t *testing.T) {
	f := newNeverStartedStallIntegrationFixture(t, "settles", true)

	f.svc.handleAgentStalled(f.ctx, f.payload)
	f.awaitSettlementDrained(t)

	require.Equal(t, 1, f.manager.retireRequestCount(), "retirement must run exactly once")
	request := f.manager.firstRetireRequest()
	require.Equal(t, f.taskID, request.TaskID)
	require.Equal(t, f.sessionID, request.SessionID)
	require.Equal(t, f.executionID, request.ExecutionID)
	require.Equal(t, f.submissionID, request.SubmissionID)
	require.Equal(t, f.streamID, request.StreamID)
	require.Equal(t, f.incarnationID, request.IncarnationID)
	require.Equal(t, uint64(neverStartedStallGeneration), request.HarnessGeneration)
	require.Equal(t, f.payload.OriginalRuntime, request.OriginalRuntime)
	require.Equal(t, f.payloadHash, request.PayloadHash)

	retired := f.requireJournalSubmission(t)
	require.True(t, retired.Retired, "the real journal write must retire the record")
	require.Equal(t, journal.SubmissionInterruptedUnknown, retired.State)
	require.False(t, retired.TerminalEventRetained)
	require.Zero(t, retired.TerminalSequence)
	require.Equal(t, string(f.promptPayload), string(retired.Payload),
		"retirement must preserve the record and its payload")
	descriptor, err := f.journal.RecoveryDescriptor(
		f.ctx, f.sessionID, f.incarnationID, neverStartedStallGeneration, "")
	require.NoError(t, err)
	require.False(t, descriptor.Unresolved)
	require.Zero(t, descriptor.SubmissionCount)

	submission := f.requireSubmission(t)
	require.Equal(t, models.DeliverySubmissionFailed, submission.State)
	require.Equal(t, string(models.DeliverySubmissionFailed), submission.Outcome)
	require.Zero(t, submission.TerminalSequence)
	require.Nil(t, submission.FullySettledAt)
	effect, err := f.repo.GetAgentDeliveryEffect(f.ctx, models.DeliveryNeverStartedEffectKey(f.submissionID))
	require.NoError(t, err)
	require.Equal(t, models.DeliveryEffectCompleted, effect.State)

	require.Equal(t, models.RecoveryBlockResolved, f.requireBlockState(t))
	require.NoError(t, f.svc.checkSessionRecoveryBlock(f.ctx, f.sessionID),
		"a follow-up prompt admission must no longer be fenced by the delivery block")

	// The stall error message itself is transient: the completing auto-resume
	// persists its own state writes with an empty error (shared resume
	// semantics), so the definitive wording is asserted on the notice below.
	session := f.requireSession(t)
	require.Equal(t, models.TaskSessionStateWaitingForInput, session.State)

	f.taskRepo.mu.Lock()
	taskState := f.taskRepo.updatedStates[f.taskID]
	failedInHistory := false
	for _, state := range f.taskRepo.stateHistory[f.taskID] {
		if state == v1.TaskStateFailed {
			failedInHistory = true
		}
	}
	f.taskRepo.mu.Unlock()
	require.False(t, failedInHistory, "the task must never transition to FAILED for a settled stall")
	require.Equal(t, v1.TaskStateReview, taskState)

	require.Zero(t, openTurnCount(t, f.repo, f.sessionID), "the captured turn must be completed")
	turn, err := f.svc.turnService.GetTurn(f.ctx, f.turnID)
	require.NoError(t, err)
	require.Equal(t, true, turn.Metadata[models.TurnMetaKeyErrorTerminated])

	require.Len(t, f.messages.sessionMessages, 1)
	notice := f.messages.sessionMessages[0]
	require.Equal(t, string(v1.MessageTypeError), notice.messageType)
	require.Equal(t, f.turnID, notice.turnID)
	require.Contains(t, notice.content, "not delivered")
	require.Contains(t, notice.content, "5m0s")

	f.requireOneLaunch(t)
}

// TestNeverStartedStallEndToEndBlockedKeepsUncertain covers the fail-closed
// path with the real journal in the loop: when the retirement evidence cannot
// prove the stall's owner is the journal's terminated owner, or the journal's
// unresolved inventory is truncated, nothing is written to the journal or SQL,
// the uncertain submission (interrupted after the forced stop's prompt-error
// path, or still dispatching) and its open block stay authoritative, the
// session and task transition to FAILED with the stall error, the fallback
// notice is posted, and no auto-resume is attempted.
func TestNeverStartedStallEndToEndBlockedKeepsUncertain(t *testing.T) {
	t.Run("unproven owner keeps uncertainty", func(t *testing.T) {
		f := newNeverStartedStallIntegrationFixture(t, "blocked-owner", false)
		f.payload.OriginalRuntime = processidentity.Identity{PID: 4243, BirthToken: "ns-e2e-unproven-owner"}
		// The forced stop's prompt-error path flips the uncertain submission to
		// interrupted_unknown before the retirement blocks, mirroring the
		// production stop-then-settle ordering.
		_, transitionErr := f.repo.TransitionAgentDeliverySubmission(f.ctx, f.submissionID,
			models.DeliverySubmissionDispatching, models.DeliverySubmissionInterruptedUnknown,
			"prompt_dispatch_failed", time.Now().UTC())
		require.NoError(t, transitionErr)

		f.svc.handleAgentStalled(f.ctx, f.payload)
		f.awaitSettlementDrained(t)

		requireBlockedUncertainOutcome(t, f,
			models.DeliverySubmissionInterruptedUnknown, "prompt_dispatch_failed")
	})

	t.Run("truncated journal evidence keeps uncertainty", func(t *testing.T) {
		f := newNeverStartedStallIntegrationFixture(t, "blocked-truncated", false)
		f.seedJournalUnresolvedNoise(t)

		f.svc.handleAgentStalled(f.ctx, f.payload)
		f.awaitSettlementDrained(t)

		requireBlockedUncertainOutcome(t, f, models.DeliverySubmissionDispatching, "dispatching")
	})
}

// requireBlockedUncertainOutcome asserts the fail-closed fallback outcome for
// one blocked scenario: the uncertain SQL submission keeps its source state,
// the journal record is untouched, the recovery block stays open, the session
// and task are FAILED with the stall error, the fallback notice is posted, and
// no auto-resume is attempted.
func requireBlockedUncertainOutcome(
	t *testing.T, f *neverStartedStallIntegrationFixture,
	expectedState models.DeliverySubmissionState, expectedOutcome string,
) {
	t.Helper()
	require.Equal(t, 1, f.manager.retireRequestCount(),
		"the settlement must reach the retirement seam exactly once")

	stored := f.requireJournalSubmission(t)
	require.False(t, stored.Retired, "a blocked retirement must not write the journal")
	require.Equal(t, journal.SubmissionDispatching, stored.State)
	require.False(t, stored.TerminalEventRetained)
	require.Zero(t, stored.TerminalSequence)

	submission := f.requireSubmission(t)
	require.Equal(t, expectedState, submission.State)
	require.Equal(t, expectedOutcome, submission.Outcome)
	_, err := f.repo.GetAgentDeliveryEffect(f.ctx, models.DeliveryNeverStartedEffectKey(f.submissionID))
	require.ErrorIs(t, err, repoerrors.ErrAgentDeliveryEffectNotFound)
	require.Equal(t, models.RecoveryBlockOpen, f.requireBlockState(t))

	session := f.requireSession(t)
	require.Equal(t, models.TaskSessionStateFailed, session.State)
	require.Contains(t, session.ErrorMessage, "provider stalled")
	require.Contains(t, session.ErrorMessage, "not delivered")

	f.taskRepo.mu.Lock()
	taskState := f.taskRepo.updatedStates[f.taskID]
	f.taskRepo.mu.Unlock()
	require.Equal(t, v1.TaskStateFailed, taskState)

	require.Len(t, f.messages.sessionMessages, 1)
	notice := f.messages.sessionMessages[0]
	require.Equal(t, string(v1.MessageTypeError), notice.messageType)
	require.Contains(t, notice.content, "not delivered")
	require.Contains(t, notice.content, "could not be settled")

	f.requireNoLaunch(t)
}

// TestNeverStartedStallEndToEndAdvisoryUnchanged covers the partial-output
// regression: a stall after the agent produced output keeps the advisory
// notice with its Cancel turn affordance even when the payload carries the
// full durable delivery identity. No settlement, journal write, SQL write,
// session state change, or auto-resume happens.
func TestNeverStartedStallEndToEndAdvisoryUnchanged(t *testing.T) {
	f := newNeverStartedStallIntegrationFixture(t, "advisory", false)
	f.payload.NeverStarted = false
	f.payload.ToolName = "shell"
	f.payload.ToolTitle = "Start dev server"
	f.payload.ToolStatus = "in_progress"

	f.svc.handleAgentStalled(f.ctx, f.payload)

	require.Zero(t, f.manager.retireRequestCount(),
		"a partial-output stall must not enter the settlement path")
	require.Len(t, f.messages.sessionMessages, 1)
	notice := f.messages.sessionMessages[0]
	require.Equal(t, string(v1.MessageTypeStatus), notice.messageType)
	require.Contains(t, notice.content, "Still waiting on Start dev server")
	require.Equal(t, f.turnID, notice.turnID)
	require.Equal(t, actionVisibilityRunning, notice.metadata[metaKeyActionVisibility])
	actions, ok := notice.metadata["actions"].([]map[string]interface{})
	require.True(t, ok, "the advisory notice must carry its actions")
	require.Len(t, actions, 1)
	require.Equal(t, "Cancel turn", actions[0]["label"])
	require.Equal(t, stallCancelTurnButtonTestID, actions[0]["test_id"])

	session := f.requireSession(t)
	require.Equal(t, models.TaskSessionStateRunning, session.State)
	require.Empty(t, session.ErrorMessage)

	stored := f.requireJournalSubmission(t)
	require.False(t, stored.Retired)
	require.Equal(t, journal.SubmissionDispatching, stored.State)
	submission := f.requireSubmission(t)
	require.Equal(t, models.DeliverySubmissionDispatching, submission.State)
	require.Equal(t, "dispatching", submission.Outcome)
	require.Equal(t, models.RecoveryBlockOpen, f.requireBlockState(t))
	require.Equal(t, 1, openTurnCount(t, f.repo, f.sessionID), "the advisory stall must leave the turn open")
	f.requireNoLaunch(t)
}
