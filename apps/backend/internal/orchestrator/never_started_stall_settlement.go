package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	agentruntime "github.com/kandev/kandev/internal/agent/runtime"
	"github.com/kandev/kandev/internal/common/processidentity"
	"github.com/kandev/kandev/internal/orchestrator/messagequeue"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
	v1 "github.com/kandev/kandev/pkg/api/v1"
)

const (
	// neverStartedSettlementTimeout bounds the journal retirement plus the
	// SQL settlement attempt of one never-started stall.
	neverStartedSettlementTimeout = 20 * time.Second
	// neverStartedStallAutoResumeBudget bounds the single auto-resume attempt
	// so a wedged launch can never pin the detached settlement goroutine past
	// the resume path's own readiness waits.
	neverStartedStallAutoResumeBudget = 60 * time.Second
)

// errAgentNeverStartedStall is the stall error recorded on the session (and
// restated by the notice) when a never-started stall settles or fails to
// settle: it names the provider stall and the watchdog teardown, and states
// that the message was not delivered.
var errAgentNeverStartedStall = errors.New(
	"provider stalled: the agent produced no output before the watchdog stopped it; the message was not delivered")

// neverStartedDeliveryRetirer retires the exact journal record of a
// never-started prompt through the runtime that owns delivery.
type neverStartedDeliveryRetirer interface {
	RetireNeverStartedDeliverySubmission(
		context.Context, agentruntime.NeverStartedDeliveryRetirementRequest,
	) (*agentruntime.NeverStartedDeliveryRetirementResult, error)
}

// neverStartedStallSettlements dedupes duplicate agent.stalled deliveries for
// the same session, execution, and prompt generation while one detached
// settlement is still running. Deliberately in-memory, mirroring
// officeStalledSignals: durable idempotence is already guaranteed by the
// deterministic notice message ID, the settlement effect key, and the guarded
// session revalidation; this map only stops a second goroutine from racing
// the first during the bounded stop+settle window, where the session is still
// RUNNING and those durable guards cannot fire yet.
var neverStartedStallSettlements sync.Map

func neverStartedStallSettlementKey(payload agentruntime.AgentStalledPayload) string {
	return payload.SessionID + "|" + payload.AgentExecutionID + "|" +
		strconv.FormatUint(payload.PromptGeneration, 10)
}

// neverStartedStallSettlementEligible reports whether a never-started stall
// may take the recoverable settlement path. The payload must carry the exact
// durable-delivery identity and runtime proof, the session must be an
// interactive non-passthrough session, and the task must be live and neither
// Office- nor automation-owned; the runtime and storage must expose the
// settlement seams. Everything else keeps the legacy terminal handling.
func (s *Service) neverStartedStallSettlementEligible(
	ctx context.Context,
	payload agentruntime.AgentStalledPayload,
	session *models.TaskSession,
) bool {
	if session == nil || session.IsPassthrough {
		return false
	}
	if payload.DeliverySubmissionID == "" || payload.DeliveryStreamID == "" ||
		payload.DeliveryIncarnationID == "" || payload.DeliveryHarnessGeneration == 0 ||
		payload.OriginalRuntime.Validate() != nil {
		return false
	}
	if _, ok := s.agentManager.(neverStartedDeliveryRetirer); !ok {
		return false
	}
	if _, ok := s.repo.(repository.AgentDeliveryNeverStartedSettlementRepository); !ok {
		return false
	}
	task, err := s.repo.GetTask(ctx, payload.TaskID)
	if err != nil || task == nil || taskArchived(task) || task.IsFromOffice ||
		models.IsAutomationTaskOrigin(task.Origin) {
		return false
	}
	return true
}

// startNeverStartedStallSettlement launches the detached settlement for one
// never-started stall and reports whether it started. The in-memory dedupe
// runs first so a duplicate stall event neither claims teardown ownership nor
// spawns a second settlement; a duplicate reports true because the running
// settlement owns the outcome. The stalled prompt generation is stamped onto
// the captured turn while handleAgentStalled still holds the guard and the
// live generation check has passed, so the settlement's later write guard can
// re-decide ownership from durable rows after the force-stop removes the
// execution from the runtime's store. Teardown ownership is still claimed
// synchronously here, so the agent.stopped event the forced stop publishes is
// recognized as already-owned (see stopNeverStartedExecution's comment).
func (s *Service) startNeverStartedStallSettlement(
	ctx context.Context,
	payload agentruntime.AgentStalledPayload,
	turnID string,
) bool {
	key := neverStartedStallSettlementKey(payload)
	if _, duplicate := neverStartedStallSettlements.LoadOrStore(key, struct{}{}); duplicate {
		return true
	}
	if !s.stampNeverStartedStallTurnGeneration(ctx, payload, turnID) {
		neverStartedStallSettlements.Delete(key)
		return false
	}
	s.claimExecutionTeardown(payload.SessionID, payload.AgentExecutionID, executionTeardownIntentForce)
	settleCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), neverStartedStopTimeout+neverStartedSettlementTimeout,
	)
	go func() {
		defer cancel()
		defer neverStartedStallSettlements.Delete(key)
		s.runNeverStartedStallSettlement(settleCtx, payload, turnID)
	}()
	return true
}

// stampNeverStartedStallTurnGeneration durably records the stalled prompt
// generation on the captured turn. The settlement's write guard re-reads this
// binding after the teardown: a turn whose recorded generation no longer
// matches means a successor prompt took over the session.
func (s *Service) stampNeverStartedStallTurnGeneration(
	ctx context.Context,
	payload agentruntime.AgentStalledPayload,
	turnID string,
) bool {
	if s.turnService == nil || turnID == "" {
		return false
	}
	if err := s.turnService.PatchTurnMetadata(ctx, payload.SessionID, turnID, map[string]interface{}{
		neverStartedMetaPromptGen: strconv.FormatUint(payload.PromptGeneration, 10),
	}); err != nil {
		s.logger.Warn("failed to capture the stalled prompt generation on the turn; using the legacy stall outcome",
			zap.String("task_id", payload.TaskID),
			zap.String("session_id", payload.SessionID),
			zap.String("turn_id", turnID),
			zap.Error(err))
		return false
	}
	return true
}

// runNeverStartedStallSettlement owns the whole detached flow for one
// never-started stall: tear the execution down, settle the delivery
// submission, apply the recovered or failed outcome, and — only when the
// session was durably recovered — attempt the single auto-resume.
func (s *Service) runNeverStartedStallSettlement(
	ctx context.Context,
	payload agentruntime.AgentStalledPayload,
	turnID string,
) {
	stopCtx, cancelStop := context.WithTimeout(ctx, neverStartedStopTimeout)
	s.stopNeverStartedExecutionBounded(stopCtx, payload)
	cancelStop()
	outcome := s.settleNeverStartedStall(ctx, payload, payload.OriginalRuntime, turnID)
	if !outcome.settled {
		s.fallBackNeverStartedStall(ctx, payload, turnID)
		return
	}
	if outcome.recovered {
		s.attemptNeverStartedStallAutoResume(ctx, payload.TaskID, payload.SessionID)
	}
}

// neverStartedStallOutcome reports the terminal result of one settlement.
type neverStartedStallOutcome struct {
	// settled records that the journal retirement and the SQL settlement both
	// committed, so the delivery outcome is authoritative.
	settled bool
	// recovered records that the session and turn writes were applied; false
	// means a user action superseded the stalled generation during settlement.
	recovered bool
}

// settleNeverStartedStall retires the stalled prompt's journal record, settles
// its SQL submission, and applies the recovered session outcome. identity is
// the original runtime proof captured in the payload. Any blocked evidence,
// conflicting state, or failed write leaves the uncertain submission and its
// open block untouched and reports settled=false for the fail-closed
// fallback.
func (s *Service) settleNeverStartedStall(
	ctx context.Context,
	payload agentruntime.AgentStalledPayload,
	identity processidentity.Identity,
	turnID string,
) neverStartedStallOutcome {
	retirer, ok := s.agentManager.(neverStartedDeliveryRetirer)
	if !ok {
		return neverStartedStallOutcome{}
	}
	rows, ok := s.repo.(repository.AgentDeliveryRepository)
	if !ok {
		return neverStartedStallOutcome{}
	}
	observed, err := rows.GetAgentDeliverySubmission(ctx, payload.DeliverySubmissionID)
	if err != nil || !neverStartedStallSubmissionMatchesPayload(observed, payload) {
		s.logger.Warn("never-started stall settlement skipped; the SQL submission does not match the stall identity",
			zap.String("task_id", payload.TaskID),
			zap.String("session_id", payload.SessionID),
			zap.String("submission_id", payload.DeliverySubmissionID),
			zap.Error(err))
		return neverStartedStallOutcome{}
	}
	settleCtx, cancel := context.WithTimeout(ctx, neverStartedSettlementTimeout)
	defer cancel()
	if _, err := retirer.RetireNeverStartedDeliverySubmission(settleCtx, agentruntime.NeverStartedDeliveryRetirementRequest{
		TaskID:            payload.TaskID,
		SessionID:         payload.SessionID,
		ExecutionID:       payload.AgentExecutionID,
		IncarnationID:     payload.DeliveryIncarnationID,
		HarnessGeneration: payload.DeliveryHarnessGeneration,
		SubmissionID:      payload.DeliverySubmissionID,
		StreamID:          payload.DeliveryStreamID,
		PayloadHash:       observed.PayloadHash,
		OriginalRuntime:   identity,
	}); err != nil {
		s.logger.Warn("never-started stall settlement blocked by journal retirement",
			zap.String("task_id", payload.TaskID),
			zap.String("session_id", payload.SessionID),
			zap.String("submission_id", payload.DeliverySubmissionID),
			zap.Error(err))
		return neverStartedStallOutcome{}
	}
	result, err := s.settleNeverStartedSQLSubmission(settleCtx, payload, observed)
	if err != nil || result == nil || !result.Settled {
		s.logger.Warn("never-started stall SQL settlement did not commit",
			zap.String("task_id", payload.TaskID),
			zap.String("session_id", payload.SessionID),
			zap.String("submission_id", payload.DeliverySubmissionID),
			zap.Error(err))
		return neverStartedStallOutcome{}
	}
	return neverStartedStallOutcome{settled: true, recovered: s.applySettledNeverStartedStall(ctx, payload, turnID)}
}

// neverStartedStallSubmissionMatchesPayload requires the observed SQL row to
// carry the exact identity the watchdog published.
func neverStartedStallSubmissionMatchesPayload(
	submission *models.AgentDeliverySubmission,
	payload agentruntime.AgentStalledPayload,
) bool {
	return submission != nil && submission.SessionID == payload.SessionID &&
		submission.IncarnationID == payload.DeliveryIncarnationID &&
		submission.HarnessGeneration == int64(payload.DeliveryHarnessGeneration) &&
		submission.PayloadHash != ""
}

// settleNeverStartedSQLSubmission settles the exact submission with the
// observed source state and outcome. The prompt error path can flip
// dispatching to interrupted_unknown between the observed read and the
// transaction, so one conflict triggers a single re-read and retry.
func (s *Service) settleNeverStartedSQLSubmission(
	ctx context.Context,
	payload agentruntime.AgentStalledPayload,
	observed *models.AgentDeliverySubmission,
) (*models.AgentDeliveryNeverStartedSettlementResult, error) {
	store, ok := s.repo.(repository.AgentDeliveryNeverStartedSettlementRepository)
	if !ok {
		return nil, nil
	}
	request := &models.AgentDeliveryNeverStartedSettlementRequest{
		SessionID:         payload.SessionID,
		IncarnationID:     payload.DeliveryIncarnationID,
		HarnessGeneration: int64(payload.DeliveryHarnessGeneration),
		SubmissionID:      payload.DeliverySubmissionID,
		ExpectedState:     observed.State,
		ExpectedOutcome:   observed.Outcome,
		SettledAt:         time.Now().UTC(),
	}
	result, err := store.SettleNeverStartedAgentDeliverySubmission(ctx, request)
	if err == nil || !errors.Is(err, repoerrors.ErrAgentDeliveryNeverStartedSettlementConflict) {
		return result, err
	}
	rows, ok := s.repo.(repository.AgentDeliveryRepository)
	if !ok {
		return result, err
	}
	refreshed, readErr := rows.GetAgentDeliverySubmission(ctx, payload.DeliverySubmissionID)
	if readErr != nil || refreshed == nil {
		return result, err
	}
	request.ExpectedState = refreshed.State
	request.ExpectedOutcome = refreshed.Outcome
	return store.SettleNeverStartedAgentDeliverySubmission(ctx, request)
}

// applySettledNeverStartedStall applies the recovered outcome under the
// per-session cancel-in-flight guard. The guard revalidates, from durable
// rows, that the session still belongs to the stalled generation:
// handleAgentStalled released the guard before the detached settlement
// started, and the force-stopped prompt's error path may already have
// reverted the session to WAITING_FOR_INPUT, which stays eligible. When a
// successor owns the session, the delivery settlement result stays
// authoritative but no session, turn, notice, or task write is made. Returns
// whether the recovery was applied.
func (s *Service) applySettledNeverStartedStall(
	ctx context.Context,
	payload agentruntime.AgentStalledPayload,
	turnID string,
) bool {
	lock, release := s.acquireCancelInFlightGuard(payload.SessionID)
	defer release()
	lock.Lock()
	defer lock.Unlock()
	session, err := s.repo.GetTaskSession(ctx, payload.SessionID)
	if err != nil || !s.neverStartedStallSessionOwned(ctx, payload, turnID, session) {
		s.logger.Info("skipping never-started stall recovery writes; the session changed during settlement",
			zap.String("task_id", payload.TaskID),
			zap.String("session_id", payload.SessionID))
		return false
	}
	s.markNeverStartedStallTurnFailed(ctx, payload, turnID)
	if inputErr := s.settleManagedInputTurn(
		ctx, payload.TaskID, payload.SessionID, turnID, payload.AgentExecutionID,
		messagequeue.ManagedInputStateFailed, "execution_failed_without_observed_effects",
	); inputErr != nil {
		s.logger.Warn("failed to settle managed input after never-started stall",
			zap.String("task_id", payload.TaskID),
			zap.String("session_id", payload.SessionID),
			zap.String("turn_id", turnID),
			zap.Error(inputErr))
	}
	if turnErr := s.completeTurnForTaskSessionCheckedOwned(ctx, payload.TaskID, payload.SessionID, turnID); turnErr != nil {
		s.logger.Warn("failed to complete never-started stall turn",
			zap.String("task_id", payload.TaskID),
			zap.String("session_id", payload.SessionID),
			zap.String("turn_id", turnID),
			zap.Error(turnErr))
	}
	if !s.persistRecoveredNeverStartedSession(ctx, payload, session) {
		return false
	}
	s.persistNeverStartedStallNoticeChecked(ctx, payload, turnID, true)
	s.writeTaskReviewState(ctx, payload.TaskID, payload.SessionID)
	return true
}

// persistRecoveredNeverStartedSession moves the session to WAITING_FOR_INPUT
// with the stall error. A session the prompt error path already reverted to
// WAITING_FOR_INPUT keeps that state: updateTaskSessionStateWithHook treats a
// same-state write as a no-op, so the error message is persisted and published
// through the existing same-state CAS and publish paths instead.
func (s *Service) persistRecoveredNeverStartedSession(
	ctx context.Context,
	payload agentruntime.AgentStalledPayload,
	session *models.TaskSession,
) bool {
	if session.State == models.TaskSessionStateWaitingForInput {
		return s.persistSettledNeverStartedStallError(ctx, payload, session)
	}
	if _, changed := s.updateTaskSessionStateWithHook(
		ctx, payload.TaskID, payload.SessionID, models.TaskSessionStateWaitingForInput,
		errAgentNeverStartedStall.Error(), false, nil, session,
	); !changed {
		s.logger.Warn("never-started stall recovery state write did not persist",
			zap.String("task_id", payload.TaskID),
			zap.String("session_id", payload.SessionID))
		return false
	}
	return true
}

// persistSettledNeverStartedStallError writes the stall error onto an
// already-waiting session and republishes the unchanged state with the new
// error message, mirroring the same-state publish the delivery-recovery
// surface uses.
func (s *Service) persistSettledNeverStartedStallError(
	ctx context.Context,
	payload agentruntime.AgentStalledPayload,
	session *models.TaskSession,
) bool {
	updater, ok := s.repo.(conditionalTaskSessionStateUpdater)
	if !ok {
		s.logger.Warn("never-started stall error message could not persist; the repository cannot update session state",
			zap.String("task_id", payload.TaskID),
			zap.String("session_id", payload.SessionID))
		return false
	}
	changed, updatedAt, err := updater.UpdateTaskSessionStateIfCurrent(
		ctx, payload.SessionID,
		models.TaskSessionStateWaitingForInput, models.TaskSessionStateWaitingForInput,
		errAgentNeverStartedStall.Error(),
	)
	if err != nil || !changed {
		s.logger.Warn("never-started stall error message did not persist",
			zap.String("task_id", payload.TaskID),
			zap.String("session_id", payload.SessionID),
			zap.Error(err))
		return false
	}
	publishedAt := updatedAt.UTC()
	s.publishTaskSessionStateChanged(
		ctx, payload.TaskID, payload.SessionID,
		models.TaskSessionStateWaitingForInput, models.TaskSessionStateWaitingForInput,
		errAgentNeverStartedStall.Error(), &publishedAt, session,
	)
	return true
}

// persistNeverStartedStallNoticeChecked persists the recovered outcome's
// notice and logs a failure instead of dropping it silently.
func (s *Service) persistNeverStartedStallNoticeChecked(
	ctx context.Context,
	payload agentruntime.AgentStalledPayload,
	turnID string,
	settled bool,
) {
	if err := s.persistNeverStartedStallNotice(ctx, payload, turnID, settled); err != nil {
		s.logger.Warn("failed to create never-started stall notice",
			zap.String("task_id", payload.TaskID),
			zap.String("session_id", payload.SessionID),
			zap.Error(err))
	}
}

// markNeverStartedStallTurnFailed patches the captured turn as
// error-terminated, mirroring markRetainedFailureTurn for the settled
// provider-turn-failure path. The durable ownership check has already
// verified the captured turn is still the session's active turn.
func (s *Service) markNeverStartedStallTurnFailed(
	ctx context.Context,
	payload agentruntime.AgentStalledPayload,
	turnID string,
) {
	if turnID == "" || s.turnService == nil {
		return
	}
	if err := s.turnService.PatchTurnMetadata(ctx, payload.SessionID, turnID, map[string]interface{}{
		models.TurnMetaKeyErrorTerminated: true,
	}); err != nil {
		s.logger.Warn("failed to mark never-started stall turn error-terminated",
			zap.String("session_id", payload.SessionID),
			zap.String("turn_id", turnID),
			zap.Error(err))
	}
}

// neverStartedStallSessionOwned reports whether the loaded session and its
// captured turn still belong to the stalled prompt generation, decided from
// durable rows. The live OwnsPromptGeneration check is wrong here: the
// successful force-stop that precedes the writes removes the execution from
// the runtime's store, so the live owner is always gone by this point. The
// prompt error path may also have reverted the session to WAITING_FOR_INPUT
// while the settlement was still tearing the runtime down, so WAITING and
// IDLE remain eligible. A successor execution, a turn whose captured prompt
// generation no longer matches, a terminal session, or an in-flight user
// cancellation ends ownership. The caller must already hold the per-session
// cancel-in-flight guard.
func (s *Service) neverStartedStallSessionOwned(
	ctx context.Context,
	payload agentruntime.AgentStalledPayload,
	turnID string,
	session *models.TaskSession,
) bool {
	if session == nil {
		return false
	}
	switch session.State {
	case models.TaskSessionStateRunning, models.TaskSessionStateWaitingForInput, models.TaskSessionStateIdle:
	default:
		return false
	}
	if session.AgentExecutionID != "" && session.AgentExecutionID != payload.AgentExecutionID {
		return false
	}
	if s.isCancelInFlight(payload.SessionID) {
		return false
	}
	return s.neverStartedStallTurnOwned(ctx, payload, turnID)
}

// neverStartedStallTurnOwned requires the captured turn to still be the
// session's open, active turn carrying the prompt generation stamped at stall
// time. A successor prompt (a different active turn), a re-dispatched
// generation, or a completed turn (a user cancel, a settled replay, or an
// abandoned resume) ends the stall's ownership of the session.
func (s *Service) neverStartedStallTurnOwned(
	ctx context.Context,
	payload agentruntime.AgentStalledPayload,
	turnID string,
) bool {
	if s.turnService == nil || turnID == "" {
		return false
	}
	turn, err := s.turnService.GetTurn(ctx, turnID)
	if err != nil || turn == nil || turn.CompletedAt != nil || turn.TaskSessionID != payload.SessionID {
		return false
	}
	stamped, _ := turn.Metadata[neverStartedMetaPromptGen].(string)
	if stamped != strconv.FormatUint(payload.PromptGeneration, 10) {
		return false
	}
	active, err := s.turnService.GetActiveTurn(ctx, payload.SessionID)
	return err == nil && active != nil && active.ID == turnID
}

// fallBackNeverStartedStall applies the fail-closed outcome: the session and
// task transition to FAILED with the stall error, and the fallback notice is
// posted. The uncertain SQL submission and its open block stay untouched. The
// write runs under the same durable revalidation as the recovered outcome —
// a session the prompt error path reverted to WAITING_FOR_INPUT can still be
// recorded FAILED, while a successor or terminal session is not overwritten.
func (s *Service) fallBackNeverStartedStall(
	ctx context.Context,
	payload agentruntime.AgentStalledPayload,
	turnID string,
) {
	lock, release := s.acquireCancelInFlightGuard(payload.SessionID)
	defer release()
	lock.Lock()
	defer lock.Unlock()
	session, err := s.repo.GetTaskSession(ctx, payload.SessionID)
	if err != nil || !s.neverStartedStallSessionOwned(ctx, payload, turnID, session) {
		s.logger.Info("skipping never-started stall fallback writes; the session changed during settlement",
			zap.String("task_id", payload.TaskID),
			zap.String("session_id", payload.SessionID))
		return
	}
	if !s.recordSessionLaunchFailure(ctx, payload.TaskID, payload.SessionID, errAgentNeverStartedStall, session) {
		s.logger.Warn("never-started stall fallback FAILED transition did not persist",
			zap.String("task_id", payload.TaskID),
			zap.String("session_id", payload.SessionID))
		return
	}
	if noticeErr := s.persistNeverStartedStallNotice(ctx, payload, turnID, false); noticeErr != nil {
		s.logger.Warn("failed to create never-started stall fallback notice",
			zap.String("task_id", payload.TaskID),
			zap.String("session_id", payload.SessionID),
			zap.Error(noticeErr))
	}
}

// persistNeverStartedStallNotice posts the single definitive notice for a
// never-started stall under a deterministic message ID, so a replayed
// settlement cannot duplicate it. settled selects the recovered wording; the
// fallback wording states the unsettled outcome.
func (s *Service) persistNeverStartedStallNotice(
	ctx context.Context,
	payload agentruntime.AgentStalledPayload,
	turnID string,
	settled bool,
) error {
	if s.messageCreator == nil {
		return nil
	}
	content := neverStartedBlockedNoticeContent(payload.StalledFor)
	messageID := neverStartedStallFallbackMessageID(payload)
	if settled {
		content = neverStartedSettledNoticeContent(payload.StalledFor)
		messageID = neverStartedStallMessageID(payload)
	}
	return s.messageCreator.CreateSessionMessageIdempotent(
		ctx, messageID, payload.TaskID, content, payload.SessionID,
		string(v1.MessageTypeError), turnID, map[string]interface{}{
			metaKeySessionID:          payload.SessionID,
			metaKeyTaskID:             payload.TaskID,
			neverStartedMetaExecID:    payload.AgentExecutionID,
			neverStartedMetaPromptGen: payload.PromptGeneration,
			metaKeyVariant:            neverStartedMetaVariantError,
		}, false,
	)
}

// Notice metadata keys and the error variant for the stall notice, kept as
// constants so the new-code string-occurrence lint stays quiet.
const (
	neverStartedMetaExecID       = "execution_id"
	neverStartedMetaPromptGen    = "prompt_generation"
	neverStartedMetaVariantError = "error"
)

func neverStartedStallMessageID(payload agentruntime.AgentStalledPayload) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf(
		"session-never-started-stall:%s:%s:%d",
		payload.SessionID, payload.AgentExecutionID, payload.PromptGeneration,
	))).String()
}

func neverStartedStallFallbackMessageID(payload agentruntime.AgentStalledPayload) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf(
		"session-never-started-stall-fallback:%s:%s:%d",
		payload.SessionID, payload.AgentExecutionID, payload.PromptGeneration,
	))).String()
}

func neverStartedSettledNoticeContent(stalledFor time.Duration) string {
	return fmt.Sprintf(
		"The agent produced no output for %s and the watchdog stopped it. The message was not delivered. Resume the session or send it again.",
		stalledFor.Round(time.Second),
	)
}

func neverStartedBlockedNoticeContent(stalledFor time.Duration) string {
	return fmt.Sprintf(
		"The agent produced no output for %s and the watchdog stopped it. The message was not delivered and the outcome could not be settled, so the session was marked failed.",
		stalledFor.Round(time.Second),
	)
}

// attemptNeverStartedStallAutoResume performs the settlement's single
// auto-resume attempt for a recovered session. It relaunches the agent through
// the existing resume path without re-sending the historical prompt; a failure
// is logged by the resume path itself and leaves the session recoverable, and
// no retry is scheduled. Bounded by neverStartedStallAutoResumeBudget.
func (s *Service) attemptNeverStartedStallAutoResume(ctx context.Context, taskID, sessionID string) {
	resumeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), neverStartedStallAutoResumeBudget)
	defer cancel()
	s.logger.Debug("attempting never-started stall auto-resume",
		zap.String("task_id", taskID),
		zap.String("session_id", sessionID))
	s.tryEnsureExecution(resumeCtx, sessionID, seam3CallShapeViewing, launchOriginAutomatic, "")
}
