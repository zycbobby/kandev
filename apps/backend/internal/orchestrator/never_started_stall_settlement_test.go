package orchestrator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/agent/runtime/lifecycle"
	"github.com/kandev/kandev/internal/common/processidentity"
	"github.com/kandev/kandev/internal/orchestrator/executor"
	"github.com/kandev/kandev/internal/task/models"
	sqliterepo "github.com/kandev/kandev/internal/task/repository/sqlite"
	v1 "github.com/kandev/kandev/pkg/api/v1"
)

const (
	neverStartedStallGeneration = 2
	neverStartedStallPromptGen  = 7
)

// neverStartedRetirementManager extends the shared mock with the Task-3
// retirement seam. Tests record requests and control the outcome (blocked
// error, success, or an onRetire hook used to stage mid-settlement races).
type neverStartedRetirementManager struct {
	*mockAgentManager

	mu             sync.Mutex
	retireRequests []lifecycle.NeverStartedDeliveryRetirementRequest
	retireErr      error
	onRetire       func(*lifecycle.NeverStartedDeliveryRetirementRequest)
}

func (m *neverStartedRetirementManager) RetireNeverStartedDeliverySubmission(
	_ context.Context,
	request lifecycle.NeverStartedDeliveryRetirementRequest,
) (*lifecycle.NeverStartedDeliveryRetirementResult, error) {
	m.mu.Lock()
	m.retireRequests = append(m.retireRequests, request)
	hook := m.onRetire
	err := m.retireErr
	m.mu.Unlock()
	if hook != nil {
		hook(&request)
	}
	if err != nil {
		return nil, err
	}
	return &lifecycle.NeverStartedDeliveryRetirementResult{ProcessTerminated: true}, nil
}

func (m *neverStartedRetirementManager) retireRequestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.retireRequests)
}

func (m *neverStartedRetirementManager) firstRetireRequest() lifecycle.NeverStartedDeliveryRetirementRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.retireRequests) == 0 {
		return lifecycle.NeverStartedDeliveryRetirementRequest{}
	}
	return m.retireRequests[0]
}

// neverStartedStallFixture seeds one durable never-started stall scenario: a
// RUNNING session with an open turn, a dispatching SQL submission, an open
// agent_delivery recovery block bound to that submission, and a full delivery
// identity in the stall payload. wireAutoResume installs the launch
// observability for tests that assert the auto-resume attempt.
type neverStartedStallFixture struct {
	ctx           context.Context
	repo          *sqliterepo.Repository
	svc           *Service
	manager       *neverStartedRetirementManager
	messages      *mockMessageCreator
	taskRepo      *mockTaskRepo
	taskID        string
	sessionID     string
	executionID   string
	submissionID  string
	incarnationID string
	blockID       string
	turnID        string
	payload       lifecycle.AgentStalledPayload
	launches      chan struct{}
}

func newNeverStartedStallFixture(t *testing.T, suffix string, wireAutoResume bool) *neverStartedStallFixture {
	t.Helper()
	ctx := context.Background()
	taskID := "ns-stall-task-" + suffix
	sessionID := "ns-stall-session-" + suffix
	incarnationID := "ns-stall-incarnation-" + suffix
	executionID := "ns-stall-execution-" + suffix
	submissionID := "prompt:ns-stall-" + suffix
	blockID := "ns-stall-block-" + suffix

	repo := setupTestRepo(t)
	seedSession(t, repo, taskID, sessionID, "", incarnationID)
	session, err := repo.GetTaskSession(ctx, sessionID)
	require.NoError(t, err)
	session.AgentProfileID = "ns-stall-profile"
	require.NoError(t, repo.UpdateTaskSession(ctx, session))
	require.NoError(t, repo.CreateHarnessSessionGeneration(ctx, &models.HarnessSessionGeneration{
		SessionID: sessionID, IncarnationID: incarnationID,
		Generation: neverStartedStallGeneration, CreationReason: "test",
	}))
	now := time.Now().UTC()
	_, err = repo.PrepareAgentDeliverySubmission(ctx, &models.AgentDeliverySubmission{
		ID: submissionID, SessionID: sessionID, IncarnationID: incarnationID,
		HarnessGeneration: neverStartedStallGeneration, OwnerGeneration: neverStartedStallGeneration,
		DispatchAttemptID: "ns-stall-dispatch-" + suffix,
		PayloadHash:       "ns-stall-hash-" + suffix, Payload: []byte(`{"text":"stalled prompt"}`),
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

	manager := &neverStartedRetirementManager{mockAgentManager: &mockAgentManager{
		repoForExecutionLookup:   repo,
		currentPromptExecutionID: executionID,
	}}
	// Prompt-ready but not running: the executor's session lookup treats the
	// stopped agent as absent (forcing a cold resume to LaunchAgent), while
	// the resume's post-launch readiness probe succeeds immediately.
	manager.isAgentReadyFn = func(context.Context, string) bool { return true }
	manager.currentPromptGeneration.Store(neverStartedStallPromptGen)
	manager.currentPromptActivityEpoch.Store(1)
	taskRepo := newMockTaskRepo()
	seedMockTaskState(taskRepo, taskID, v1.TaskStateInProgress)
	svc := createTestServiceWithScheduler(repo, newMockStepGetter(), taskRepo, manager)
	svc.turnService = &repoTurnService{repo: repo}
	turn, err := svc.turnService.StartTurn(ctx, sessionID)
	require.NoError(t, err)
	messages := &mockMessageCreator{}
	svc.messageCreator = messages

	f := &neverStartedStallFixture{
		ctx: ctx, repo: repo, svc: svc, manager: manager, messages: messages, taskRepo: taskRepo,
		taskID: taskID, sessionID: sessionID, executionID: executionID,
		submissionID: submissionID, incarnationID: incarnationID, blockID: blockID,
		turnID:   turn.ID,
		launches: make(chan struct{}, 8),
		payload: lifecycle.AgentStalledPayload{
			AgentExecutionID: executionID, TaskID: taskID, SessionID: sessionID,
			PromptGeneration: neverStartedStallPromptGen, ActivityEpoch: 1,
			StalledFor: 5 * time.Minute, NeverStarted: true,
			DeliverySubmissionID:      submissionID,
			DeliveryStreamID:          "ns-stall-stream-" + suffix,
			DeliveryIncarnationID:     incarnationID,
			DeliveryHarnessGeneration: neverStartedStallGeneration,
			OriginalRuntime:           processidentity.Identity{PID: 4242, BirthToken: "ns-stall-birth-" + suffix},
		},
	}
	if wireAutoResume {
		f.installAutoResumeLaunch(t)
	}
	return f
}

// installAutoResumeLaunch makes the settlement's single auto-resume attempt
// observable and completable: the seeded executors_running row lets the cold
// resume reach LaunchAgent, and the launch func records the attempt and
// settles the session back to WAITING_FOR_INPUT so the resume's bounded
// readiness waits return promptly.
func (f *neverStartedStallFixture) installAutoResumeLaunch(t *testing.T) {
	seedExecutorRunning(t, f.repo, f.sessionID, f.taskID, f.executionID)
	resumedExecution := "ns-stall-resumed-" + f.executionID
	f.manager.launchAgentFunc = func(
		context.Context,
		*executor.LaunchAgentRequest,
	) (*executor.LaunchAgentResponse, error) {
		f.launches <- struct{}{}
		_ = f.repo.UpsertExecutorRunning(context.Background(), &models.ExecutorRunning{
			ID: f.sessionID, SessionID: f.sessionID, TaskID: f.taskID,
			AgentExecutionID: resumedExecution, Status: "ready",
		})
		_ = f.repo.UpdateTaskSessionState(context.Background(), f.sessionID,
			models.TaskSessionStateWaitingForInput, "")
		return &executor.LaunchAgentResponse{
			AgentExecutionID: resumedExecution, Status: v1.AgentStatusReady,
		}, nil
	}
}

// awaitSettlementDrained blocks until the detached settlement goroutine for
// the fixture's payload releases its in-memory dedupe entry, i.e. until the
// whole stop + settle + outcome + auto-resume sequence has finished.
func (f *neverStartedStallFixture) awaitSettlementDrained(t *testing.T) {
	t.Helper()
	key := neverStartedStallSettlementKey(f.payload)
	require.Eventually(t, func() bool {
		_, active := neverStartedStallSettlements.Load(key)
		return !active
	}, 5*time.Second, 5*time.Millisecond, "never-started stall settlement did not finish")
}

func (f *neverStartedStallFixture) requireSubmission(t *testing.T) *models.AgentDeliverySubmission {
	t.Helper()
	submission, err := f.repo.GetAgentDeliverySubmission(f.ctx, f.submissionID)
	require.NoError(t, err)
	return submission
}

func (f *neverStartedStallFixture) requireSession(t *testing.T) *models.TaskSession {
	t.Helper()
	session, err := f.repo.GetTaskSession(f.ctx, f.sessionID)
	require.NoError(t, err)
	return session
}

func (f *neverStartedStallFixture) requireBlockState(t *testing.T) string {
	t.Helper()
	block, err := f.repo.GetSessionRecoveryBlock(f.ctx, f.blockID)
	require.NoError(t, err)
	require.NotNil(t, block)
	return block.State
}

func (f *neverStartedStallFixture) requireNoLaunch(t *testing.T) {
	t.Helper()
	select {
	case <-f.launches:
		t.Fatal("auto-resume launched the agent unexpectedly")
	default:
	}
}

func (f *neverStartedStallFixture) requireOneLaunch(t *testing.T) {
	t.Helper()
	select {
	case <-f.launches:
	default:
		t.Fatal("expected exactly one auto-resume launch, got none")
	}
	f.requireNoLaunch(t)
}

// TestHandleAgentStalled_NeverStartedSettlesAndRecoversSession covers the
// success path: the journal retirement and SQL settlement succeed, the open
// delivery block is resolved, the session recovers to WAITING_FOR_INPUT with
// the stall error, the task does not go FAILED, the captured turn is
// completed, exactly one "not delivered" notice is persisted, and one
// auto-resume launch is attempted.
func TestHandleAgentStalled_NeverStartedSettlesAndRecoversSession(t *testing.T) {
	f := newNeverStartedStallFixture(t, "settles", true)

	f.svc.handleAgentStalled(f.ctx, f.payload)
	f.awaitSettlementDrained(t)

	require.Equal(t, 1, f.manager.retireRequestCount(), "retirement must run exactly once")
	request := f.manager.firstRetireRequest()
	require.Equal(t, f.taskID, request.TaskID)
	require.Equal(t, f.sessionID, request.SessionID)
	require.Equal(t, f.executionID, request.ExecutionID)
	require.Equal(t, f.submissionID, request.SubmissionID)
	require.Equal(t, f.payload.DeliveryStreamID, request.StreamID)
	require.Equal(t, f.incarnationID, request.IncarnationID)
	require.Equal(t, uint64(neverStartedStallGeneration), request.HarnessGeneration)
	require.Equal(t, f.payload.OriginalRuntime, request.OriginalRuntime)
	require.Equal(t, "ns-stall-hash-settles", request.PayloadHash)

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
		"prompt admission must no longer be fenced by the open delivery block")

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
	require.False(t, failedInHistory, "task must never transition to FAILED for a settled stall")
	require.NotEqual(t, v1.TaskStateFailed, taskState)
	require.Equal(t, v1.TaskStateReview, taskState, "task should reconcile to REVIEW")

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

// TestHandleAgentStalled_NeverStartedSettlementBlockedFallsBack covers the
// fail-closed path: a blocked journal retirement keeps the uncertain SQL
// submission and its open block authoritative, records the session and task
// FAILED with the stall error, posts the fallback notice, and never attempts
// an auto-resume.
func TestHandleAgentStalled_NeverStartedSettlementBlockedFallsBack(t *testing.T) {
	f := newNeverStartedStallFixture(t, "blocked", false)
	_, transitionErr := f.repo.TransitionAgentDeliverySubmission(f.ctx, f.submissionID,
		models.DeliverySubmissionDispatching, models.DeliverySubmissionInterruptedUnknown,
		"prompt_dispatch_failed", time.Now().UTC())
	require.NoError(t, transitionErr)
	f.manager.retireErr = errors.Join(
		lifecycle.ErrDeliveryRecoveryBlocked, lifecycle.ErrDeliveryOwnerMismatch)

	f.svc.handleAgentStalled(f.ctx, f.payload)
	f.awaitSettlementDrained(t)

	require.Equal(t, 1, f.manager.retireRequestCount())

	submission := f.requireSubmission(t)
	require.Equal(t, models.DeliverySubmissionInterruptedUnknown, submission.State)
	require.Equal(t, "prompt_dispatch_failed", submission.Outcome)
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

	f.requireNoLaunch(t)
}

// TestHandleAgentStalled_NeverStartedLatePromptErrorDoesNotRevert covers the
// race where the settlement wins and the force-stopped prompt's late error
// handler runs afterwards: it must not create a new unknown_prompt_outcome
// block, must not revert a successor prompt's RUNNING session state, and must
// not re-complete turns.
func TestHandleAgentStalled_NeverStartedLatePromptErrorDoesNotRevert(t *testing.T) {
	f := newNeverStartedStallFixture(t, "late-error", false)

	f.svc.handleAgentStalled(f.ctx, f.payload)
	f.awaitSettlementDrained(t)

	// The session error message carries the stall reason. It is transient: a
	// launching auto-resume persists STARTING with an empty error (shared
	// resume semantics), so it is asserted here, where the resume fails fast
	// on the missing executors_running row and never writes state.
	recoveredSession := f.requireSession(t)
	require.Equal(t, models.TaskSessionStateWaitingForInput, recoveredSession.State)
	require.Contains(t, recoveredSession.ErrorMessage, "provider stalled")
	require.Contains(t, recoveredSession.ErrorMessage, "not delivered")

	// A user re-prompted after the recovery: the session is RUNNING again and
	// a successor turn is open.
	successorTurn, err := f.svc.turnService.StartTurn(f.ctx, f.sessionID)
	require.NoError(t, err)
	require.NoError(t, f.repo.UpdateTaskSessionState(f.ctx, f.sessionID,
		models.TaskSessionStateRunning, ""))

	delivery := &agentDeliverySubmissionRuntime{store: f.repo, id: f.submissionID, sessionID: f.sessionID}
	_, err = f.svc.finishPromptExecutorDispatch(
		f.ctx, f.taskID, f.sessionID, "stalled prompt", false, false, nil,
		promptClaimRollback{previousSessionState: models.TaskSessionStateWaitingForInput, turnID: f.turnID},
		promptTaskOptions{}, nil, nil, nil,
		lifecycle.ErrUncertainPromptDelivery, nil, nil, delivery,
	)
	require.ErrorIs(t, err, lifecycle.ErrUncertainPromptDelivery)

	session := f.requireSession(t)
	require.Equal(t, models.TaskSessionStateRunning, session.State,
		"a late prompt error must not revert the recovered session")
	activeTurn, err := f.svc.turnService.GetActiveTurn(f.ctx, f.sessionID)
	require.NoError(t, err)
	require.NotNil(t, activeTurn, "the successor turn must stay open")
	require.Equal(t, successorTurn.ID, activeTurn.ID)

	blocks, err := f.repo.ListOpenSessionRecoveryBlocks(f.ctx, f.sessionID, f.incarnationID, neverStartedStallGeneration)
	require.NoError(t, err)
	require.Empty(t, blocks, "a late prompt error must not recreate a recovery block")

	submission := f.requireSubmission(t)
	require.Equal(t, models.DeliverySubmissionFailed, submission.State)
}

// TestHandleAgentStalled_ScopeGuards covers the scope boundary: Office,
// automation, passthrough, and archived sessions with full delivery identity
// never enter the new settlement path and keep the legacy FAILED handling.
func TestHandleAgentStalled_ScopeGuards(t *testing.T) {
	t.Run("office task keeps legacy handling", func(t *testing.T) {
		f := newNeverStartedStallFixture(t, "scope-office", false)
		_, err := f.repo.DB().ExecContext(f.ctx,
			`UPDATE tasks SET project_id = ? WHERE id = ?`, "office-project", f.taskID)
		require.NoError(t, err)

		f.svc.handleAgentStalled(f.ctx, f.payload)

		require.Zero(t, f.manager.retireRequestCount(), "office tasks must not enter the settlement path")
		require.Equal(t, models.TaskSessionStateFailed, f.requireSession(t).State)
		require.Len(t, f.messages.sessionMessages, 1)
		require.Equal(t, neverStartedNoticeContent, f.messages.sessionMessages[0].content)
	})

	t.Run("automation task keeps legacy handling", func(t *testing.T) {
		f := newNeverStartedStallFixture(t, "scope-automation", false)
		_, err := f.repo.DB().ExecContext(f.ctx,
			`UPDATE tasks SET origin = ? WHERE id = ?`, models.TaskOriginAutomationRun, f.taskID)
		require.NoError(t, err)

		f.svc.handleAgentStalled(f.ctx, f.payload)

		require.Zero(t, f.manager.retireRequestCount(), "automation tasks must not enter the settlement path")
		require.Equal(t, models.TaskSessionStateFailed, f.requireSession(t).State)
	})

	t.Run("passthrough session keeps legacy handling", func(t *testing.T) {
		f := newNeverStartedStallFixture(t, "scope-passthrough", false)
		session := f.requireSession(t)
		session.IsPassthrough = true
		require.NoError(t, f.repo.UpdateTaskSession(f.ctx, session))

		f.svc.handleAgentStalled(f.ctx, f.payload)

		require.Zero(t, f.manager.retireRequestCount(), "passthrough sessions must not enter the settlement path")
		require.Equal(t, models.TaskSessionStateFailed, f.requireSession(t).State)
	})

	t.Run("archived task keeps legacy handling", func(t *testing.T) {
		f := newNeverStartedStallFixture(t, "scope-archived", false)
		_, err := f.repo.DB().ExecContext(f.ctx,
			`UPDATE tasks SET archived_at = ? WHERE id = ?`, time.Now().UTC(), f.taskID)
		require.NoError(t, err)

		f.svc.handleAgentStalled(f.ctx, f.payload)

		require.Zero(t, f.manager.retireRequestCount(), "archived tasks must not enter the settlement path")
		require.Equal(t, models.TaskSessionStateFailed, f.requireSession(t).State)
	})
}

// TestHandleAgentStalled_DuplicateStallEventSettlesOnce covers the in-flight
// dedupe: a duplicate stall event delivered while the first settlement is
// still running produces no second retirement, effect, notice, or launch.
func TestHandleAgentStalled_DuplicateStallEventSettlesOnce(t *testing.T) {
	f := newNeverStartedStallFixture(t, "duplicate", true)
	retireEntered := make(chan struct{})
	releaseRetirement := make(chan struct{})
	var retireEnteredOnce sync.Once
	f.manager.onRetire = func(*lifecycle.NeverStartedDeliveryRetirementRequest) {
		retireEnteredOnce.Do(func() { close(retireEntered) })
		<-releaseRetirement
	}

	f.svc.handleAgentStalled(f.ctx, f.payload)
	select {
	case <-retireEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the first settlement to reach retirement")
	}

	// The duplicate arrives while the settlement goroutine is still blocked in
	// retirement, so the session is still RUNNING and only the in-memory
	// dedupe can suppress the second settlement.
	f.svc.handleAgentStalled(f.ctx, f.payload)
	close(releaseRetirement)
	f.awaitSettlementDrained(t)

	require.Equal(t, 1, f.manager.retireRequestCount(), "the duplicate stall event must not retire again")
	submission := f.requireSubmission(t)
	require.Equal(t, models.DeliverySubmissionFailed, submission.State)
	require.Len(t, f.messages.sessionMessages, 1)
	require.Equal(t, models.RecoveryBlockResolved, f.requireBlockState(t))
	f.requireOneLaunch(t)
}

// TestHandleAgentStalled_NeverStartedUserInterventionKeepsSettlement covers
// the guarded final write: when a user action takes the session over during
// the settlement, the delivery settlement result is still recorded, but the
// state write, notice, and auto-resume are skipped.
func TestHandleAgentStalled_NeverStartedUserInterventionKeepsSettlement(t *testing.T) {
	f := newNeverStartedStallFixture(t, "intervened", false)
	f.manager.onRetire = func(*lifecycle.NeverStartedDeliveryRetirementRequest) {
		require.NoError(t, f.repo.UpdateTaskSessionState(f.ctx, f.sessionID,
			models.TaskSessionStateWaitingForInput, ""))
	}

	f.svc.handleAgentStalled(f.ctx, f.payload)
	f.awaitSettlementDrained(t)

	submission := f.requireSubmission(t)
	require.Equal(t, models.DeliverySubmissionFailed, submission.State,
		"the delivery settlement result must survive user intervention")
	require.Equal(t, models.RecoveryBlockResolved, f.requireBlockState(t))

	session := f.requireSession(t)
	require.Equal(t, models.TaskSessionStateWaitingForInput, session.State)
	require.Empty(t, session.ErrorMessage, "the settlement must not overwrite the user's state")
	require.Empty(t, f.messages.sessionMessages, "the notice belongs to the recovered outcome")
	f.requireNoLaunch(t)
}
