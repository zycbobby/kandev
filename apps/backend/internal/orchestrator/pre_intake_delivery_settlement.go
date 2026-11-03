package orchestrator

import (
	"context"
	"database/sql"
	"errors"

	"go.uber.org/zap"

	agentruntime "github.com/kandev/kandev/internal/agent/runtime"
	"github.com/kandev/kandev/internal/agentctl/journal"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository"
)

var errPreIntakeSettlementUnavailable = errors.New("pre-intake delivery settlement is unavailable")

// preIntakeDeliverySettlementEligible requires the conservative evidence set:
// an exact empty-stream block binding, an authenticated untruncated journal
// with no other unresolved candidate, and explicit absence of the exact ID.
func preIntakeDeliverySettlementEligible(
	evidence *agentruntime.DeliveryRecordEvidence,
	session *models.TaskSession,
	incarnationID string,
	generation int64,
	block *models.SessionRecoveryBlock,
) bool {
	if evidence == nil || session == nil || !preIntakeBlockBindingMatches(block, session, incarnationID, generation) {
		return false
	}
	if !evidence.SubmissionAbsent {
		return false
	}
	descriptor := evidence.Descriptor
	if !descriptor.Durable || !journal.SupportsDeliveryVersion(descriptor.Version) || descriptor.SubmissionsTruncated {
		return false
	}
	if descriptor.SessionID != session.ID || descriptor.IncarnationID != incarnationID ||
		descriptor.HarnessGeneration != uint64(generation) {
		return false
	}
	return descriptor.SubmissionCount == 0 && len(descriptor.Submissions) == 0
}

func preIntakeBlockBindingMatches(
	block *models.SessionRecoveryBlock,
	session *models.TaskSession,
	incarnationID string,
	generation int64,
) bool {
	return block != nil && block.ConsumerReference == agentDeliveryConsumer &&
		block.DeliverySubmissionID != "" && block.DeliveryStreamID == "" &&
		block.Reason == unknownPromptOutcome && block.SessionID == session.ID &&
		block.IncarnationID == incarnationID && block.ExpectedGeneration == generation
}

func (s *Service) commitPreIntakeDeliverySettlement(
	ctx context.Context,
	session *models.TaskSession,
	incarnationID string,
	generation int64,
	block *models.SessionRecoveryBlock,
	evidence *agentruntime.DeliveryRecordEvidence,
) (*SessionDeliveryRecoveryResponse, error) {
	blocked := func(reason string) (*SessionDeliveryRecoveryResponse, error) {
		return sessionDeliveryRecoveryResponse(session.TaskID, session.ID, SessionDeliveryRecoveryBlocked, reason, 0, nil), nil
	}
	if evidence == nil || block == nil || !evidence.SubmissionAbsent {
		return blocked(recoveryStateUnavailableReason)
	}
	submissions, ok := s.repo.(repository.AgentDeliveryRepository)
	if !ok {
		return blocked(recoveryStateUnavailableReason)
	}
	submission, err := submissions.GetAgentDeliverySubmission(ctx, block.DeliverySubmissionID)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, repository.ErrAgentDeliverySubmissionNotFound) {
		return blocked(deliveryIdentityMismatchReason)
	}
	if err != nil {
		return nil, err
	}
	if !preIntakeSubmissionMatches(session, submission, incarnationID, generation, block) {
		return blocked(deliveryIdentityMismatchReason)
	}
	if _, err = s.settlePreIntakeDelivery(ctx, session, submission.ID,
		models.DeliverySubmissionInterruptedUnknown, "prompt_dispatch_failed", block.ID, true); err != nil {
		return s.preIntakeSettlementError(session, err)
	}
	s.publishAgentDeliveryRecoveryState(ctx, session.ID)
	return sessionDeliveryRecoveryResponse(session.TaskID, session.ID, SessionDeliveryRecoverySettled, "", 0, nil), nil
}

func (s *Service) preIntakeSettlementError(
	session *models.TaskSession,
	err error,
) (*SessionDeliveryRecoveryResponse, error) {
	blocked := func(reason string) (*SessionDeliveryRecoveryResponse, error) {
		return sessionDeliveryRecoveryResponse(session.TaskID, session.ID, SessionDeliveryRecoveryBlocked, reason, 0, nil), nil
	}
	switch {
	case errors.Is(err, repository.ErrAgentDeliveryPreIntakeSettlementConflict),
		errors.Is(err, repository.ErrAgentDeliverySubmissionNotFound):
		return blocked(deliveryIdentityMismatchReason)
	case errors.Is(err, repository.ErrAgentDeliveryPreIntakeSettlementStale):
		return blocked(staleDeliveryRecoveryReason)
	case errors.Is(err, errPreIntakeSettlementUnavailable):
		return blocked(recoveryStateUnavailableReason)
	default:
		return nil, err
	}
}

func preIntakeSubmissionMatches(
	session *models.TaskSession,
	submission *models.AgentDeliverySubmission,
	incarnationID string,
	generation int64,
	block *models.SessionRecoveryBlock,
) bool {
	if submission == nil || submission.ID != block.DeliverySubmissionID ||
		submission.SessionID != session.ID || submission.IncarnationID != incarnationID ||
		submission.HarnessGeneration != generation ||
		submission.TerminalSequence != 0 || submission.FullySettledAt != nil {
		return false
	}
	fresh := submission.State == models.DeliverySubmissionInterruptedUnknown &&
		submission.Outcome == "prompt_dispatch_failed"
	settled := submission.State == models.DeliverySubmissionFailed && submission.Outcome == "failed"
	return fresh || settled
}

// settlePreIntakeDelivery settles one submission that provably never entered
// the retained journal. Only the retry path releases parked queue work; the
// immediate dispatch-failure path already restores its claim.
func (s *Service) settlePreIntakeDelivery(
	ctx context.Context,
	session *models.TaskSession,
	submissionID string,
	expectedState models.DeliverySubmissionState,
	expectedOutcome, blockID string,
	releaseQueue bool,
) (*models.AgentDeliveryPreIntakeSettlementResult, error) {
	settler, ok := s.repo.(repository.AgentDeliveryPreIntakeSettlementRepository)
	if !ok {
		return nil, errPreIntakeSettlementUnavailable
	}
	incarnationID := session.QueueIncarnationID
	if incarnationID == "" {
		incarnationID = session.ID
	}
	generation, err := s.currentDeliveryGeneration(ctx, session, incarnationID)
	if err != nil {
		return nil, err
	}
	result, err := settler.SettlePreIntakeAgentDeliverySubmission(ctx, &models.AgentDeliveryPreIntakeSettlementRequest{
		SessionID: session.ID, IncarnationID: incarnationID, HarnessGeneration: generation,
		SubmissionID: submissionID, ExpectedState: expectedState, ExpectedOutcome: expectedOutcome,
		BlockID: blockID,
	})
	if err != nil {
		return nil, err
	}
	if releaseQueue && !result.AlreadySettled {
		if restoreErr := s.restorePendingQueueDispatchesForRecovery(ctx, session.ID); restoreErr != nil {
			s.logger.Warn("failed to release parked queue work after pre-intake settlement",
				zap.String("session_id", session.ID), zap.Error(restoreErr))
		}
	}
	return result, nil
}

// settleRejectedPreIntakeDelivery settles a submission that agentctl
// definitively refused before journal intake. A repository failure falls back
// to the existing uncertain recovery path, which records why.
func (s *Service) settleRejectedPreIntakeDelivery(
	ctx context.Context,
	taskID, sessionID string,
	delivery *agentDeliverySubmissionRuntime,
) (bool, error) {
	if delivery == nil || delivery.id == "" {
		return false, nil
	}
	session, err := s.loadDeliveryRetrySession(ctx, taskID, sessionID)
	if err != nil {
		return false, err
	}
	if _, err = s.settlePreIntakeDelivery(ctx, session, delivery.id,
		models.DeliverySubmissionDispatching, "dispatching", "", false); err != nil {
		if errors.Is(err, repository.ErrAgentDeliveryPreIntakeSettlementConflict) ||
			errors.Is(err, repository.ErrAgentDeliveryPreIntakeSettlementStale) ||
			errors.Is(err, repository.ErrAgentDeliverySubmissionNotFound) ||
			errors.Is(err, errPreIntakeSettlementUnavailable) {
			return false, nil
		}
		return false, err
	}
	delivery.completed = true
	s.publishAgentDeliveryRecoveryState(ctx, sessionID)
	return true, nil
}
