package orchestrator

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"go.uber.org/zap"

	agentruntime "github.com/kandev/kandev/internal/agent/runtime"
	"github.com/kandev/kandev/internal/agentctl/journal"
	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository"
)

// deliveryRecordRetirementClient retires abandoned journal records through the
// runtime that owns them.
type deliveryRecordRetirementClient interface {
	RetireDeliveryRecordSubmissions(context.Context, agentruntime.DeliveryRecordRetirementRequest) (*agentruntime.DeliveryRecordRetirementResult, error)
}

// abandonedSettlementExcluded keeps Office- and automation-owned sessions on
// their existing recovery paths. This settlement only serves user-owned work.
func (s *Service) abandonedSettlementExcluded(ctx context.Context, session *models.TaskSession) (bool, error) {
	if s.automationOwnsRetrySession(ctx, session.TaskID, session.ID) {
		return true, nil
	}
	return s.lookupOfficeTask(ctx, session.TaskID)
}

// retryAbandonedJournalDelivery settles a submission whose retained journal
// record proves the historical prompt was abandoned. It reports handled=false
// when the evidence belongs to an existing recovery path (terminal history,
// an absent journal record, or a still-reconciling submission).
func (s *Service) retryAbandonedJournalDelivery(
	ctx context.Context,
	session *models.TaskSession,
	block *models.SessionRecoveryBlock,
	recovery models.AgentDeliveryRecovery,
	submission *models.AgentDeliverySubmission,
) (*SessionDeliveryRecoveryResponse, bool, error) {
	excluded, err := s.abandonedSettlementExcluded(ctx, session)
	if err != nil {
		return nil, false, err
	}
	if excluded {
		return nil, false, nil
	}
	if recovery.Phase != models.AgentDeliveryRecoveryUncertain || recovery.HistoryGap != nil ||
		!abandonedBlockMatches(block, recovery) {
		return nil, false, nil
	}
	client, ok := s.agentManager.(deliveryRecordEvidenceClient)
	if !ok {
		return nil, false, nil
	}
	retirer, ok := s.agentManager.(deliveryRecordRetirementClient)
	if !ok {
		return nil, false, nil
	}
	store, ok := s.repo.(repository.AgentDeliveryAbandonedSettlementRepository)
	if !ok {
		return nil, false, nil
	}
	retryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	evidence, err := client.InspectDeliveryRecordEvidence(retryCtx, abandonedEvidenceRequest(session, recovery))
	if err != nil {
		if evidence == nil {
			return nil, false, nil
		}
		return abandonedBlocked(session, recovery, retainedEvidenceAmbiguousReason), true, nil
	}
	target := evidence.StoredSubmission
	if evidence.SubmissionAbsent || target == nil {
		return nil, false, nil
	}
	if !abandonedTargetIdentityMatches(target, session, recovery, submission, block) {
		return abandonedBlocked(session, recovery, deliveryIdentityMismatchReason), true, nil
	}
	if !abandonedTargetStateEligible(target) || submission.State != models.DeliverySubmissionInterruptedUnknown {
		return nil, false, nil
	}
	evidence, reason, err := s.abandonedConvergedEvidence(retryCtx, client, session, recovery, target, evidence)
	if err != nil {
		return abandonedBlocked(session, recovery, deliveryRecordEvidenceReason(err)), true, nil
	}
	if reason != "" {
		return abandonedBlocked(session, recovery, reason), true, nil
	}
	if evidence.ProcessTerminated == nil || !*evidence.ProcessTerminated {
		return abandonedBlocked(session, recovery, deliveryIdentityMismatchReason), true, nil
	}
	targets, reason, err := s.abandonedRetirementTargets(retryCtx, evidence.Descriptor, target, recovery)
	if err != nil {
		return nil, true, err
	}
	if reason != "" {
		return abandonedBlocked(session, recovery, reason), true, nil
	}
	if len(targets) != 0 {
		if response, handled := s.retireAbandonedTargets(retryCtx, retirer, session, recovery, targets); handled {
			return response, true, nil
		}
	}
	return s.commitAbandonedSettlement(retryCtx, ctx, store, session, recovery, block, submission)
}

func abandonedEvidenceRequest(session *models.TaskSession, recovery models.AgentDeliveryRecovery) agentruntime.DeliveryRecordEvidenceRequest {
	return agentruntime.DeliveryRecordEvidenceRequest{
		TaskID: session.TaskID, SessionID: session.ID, ExecutionID: recovery.AgentExecutionID,
		IncarnationID: recovery.IncarnationID, HarnessGeneration: uint64(recovery.HarnessGeneration),
		StoredSubmissionID: recovery.SubmissionID, OriginalRuntime: &recovery.OriginalRuntime,
	}
}

func abandonedBlockMatches(block *models.SessionRecoveryBlock, recovery models.AgentDeliveryRecovery) bool {
	return block != nil && block.ConsumerReference == agentDeliveryConsumer &&
		block.DeliverySubmissionID == recovery.SubmissionID && block.DeliveryStreamID != "" &&
		block.DeliveryStreamID == recovery.StreamID && block.IncarnationID == recovery.IncarnationID &&
		block.ExpectedGeneration == recovery.HarnessGeneration && block.State == models.RecoveryBlockOpen
}

func abandonedTargetIdentityMatches(
	target *journal.Submission,
	session *models.TaskSession,
	recovery models.AgentDeliveryRecovery,
	submission *models.AgentDeliverySubmission,
	block *models.SessionRecoveryBlock,
) bool {
	return target != nil && target.ID == recovery.SubmissionID && target.ID == submission.ID &&
		target.ID == block.DeliverySubmissionID && target.SessionID == session.ID &&
		target.IncarnationID == recovery.IncarnationID &&
		target.HarnessGeneration == uint64(recovery.HarnessGeneration) &&
		target.StreamID != "" && target.StreamID == recovery.StreamID &&
		target.StreamID == block.DeliveryStreamID && target.Hash != "" &&
		target.Hash == submission.PayloadHash
}

// abandonedTargetStateEligible reports whether the target is the abandoned
// shape. Terminal evidence and live work stay on the existing recovery paths.
func abandonedTargetStateEligible(target *journal.Submission) bool {
	if target.TerminalEventRetained || target.TerminalSequence != 0 {
		return false
	}
	return target.State == journal.SubmissionInterruptedUnknown
}

// abandonedConvergedEvidence re-reads the retired target after a crash window
// and returns the current descriptor for the remaining unresolved inventory.
func (s *Service) abandonedConvergedEvidence(
	ctx context.Context,
	client deliveryRecordEvidenceClient,
	session *models.TaskSession,
	recovery models.AgentDeliveryRecovery,
	target *journal.Submission,
	evidence *agentruntime.DeliveryRecordEvidence,
) (*agentruntime.DeliveryRecordEvidence, string, error) {
	if !target.Retired {
		return evidence, abandonedDescriptorReason(evidence, session, recovery), nil
	}
	request := abandonedEvidenceRequest(session, recovery)
	request.StoredSubmissionID = ""
	request.RetiredSubmissionID = recovery.SubmissionID
	retiredEvidence, err := client.InspectDeliveryRecordEvidence(ctx, request)
	if err != nil {
		return nil, "", err
	}
	if retiredEvidence == nil || retiredEvidence.RetiredSubmission == nil {
		return nil, deliveryIdentityMismatchReason, nil
	}
	if !abandonedRetiredTargetMatches(retiredEvidence.RetiredSubmission, target) {
		return nil, deliveryIdentityMismatchReason, nil
	}
	if reason := abandonedDescriptorReason(retiredEvidence, session, recovery); reason != "" {
		return nil, reason, nil
	}
	return retiredEvidence, "", nil
}

func abandonedRetiredTargetMatches(retired, target *journal.Submission) bool {
	return retired != nil && target != nil && retired.ID == target.ID && retired.Retired &&
		journal.HasUnprovenOutcome(*retired) && retired.SessionID == target.SessionID &&
		retired.IncarnationID == target.IncarnationID &&
		retired.HarnessGeneration == target.HarnessGeneration &&
		retired.StreamID == target.StreamID && retired.Hash == target.Hash &&
		retired.State == target.State && !retired.TerminalEventRetained && retired.TerminalSequence == 0
}

func abandonedDescriptorReason(
	evidence *agentruntime.DeliveryRecordEvidence,
	session *models.TaskSession,
	recovery models.AgentDeliveryRecovery,
) string {
	if evidence == nil {
		return recoveryStateUnavailableReason
	}
	descriptor := evidence.Descriptor
	if !descriptor.Durable || !journal.SupportsDeliveryVersion(descriptor.Version) {
		return recoveryStateUnavailableReason
	}
	if descriptor.SubmissionsTruncated || descriptor.SubmissionCount != len(descriptor.Submissions) {
		return retainedEvidenceAmbiguousReason
	}
	if descriptor.SessionID != session.ID || descriptor.IncarnationID != recovery.IncarnationID ||
		descriptor.HarnessGeneration != uint64(recovery.HarnessGeneration) {
		return deliveryIdentityMismatchReason
	}
	return ""
}

// abandonedRetirementTargets validates the complete unresolved inventory and
// returns the records that still need retirement. A target retired by a
// previous attempt is evidence, not a new retirement candidate.
func (s *Service) abandonedRetirementTargets(
	ctx context.Context,
	descriptor journal.RecoveryDescriptor,
	target *journal.Submission,
	recovery models.AgentDeliveryRecovery,
) ([]agentruntime.DeliveryRecordRetirementTarget, string, error) {
	rows, ok := s.repo.(repository.AgentDeliveryRepository)
	if !ok {
		return nil, recoveryStateUnavailableReason, nil
	}
	seen := make(map[string]struct{}, len(descriptor.Submissions))
	targets := make([]agentruntime.DeliveryRecordRetirementTarget, 0, len(descriptor.Submissions))
	listed := false
	for _, summary := range descriptor.Submissions {
		if _, duplicate := seen[summary.ID]; duplicate {
			return nil, deliveryIdentityMismatchReason, nil
		}
		seen[summary.ID] = struct{}{}
		if summary.ID == target.ID {
			if target.Retired || !abandonedSummaryMatchesTarget(summary, target) {
				return nil, deliveryIdentityMismatchReason, nil
			}
			listed = true
			targets = append(targets, abandonedRetirementTarget(summary))
			continue
		}
		reason, err := abandonedExtraPeerReason(ctx, rows, summary, recovery)
		if err != nil {
			return nil, "", err
		}
		if reason != "" {
			return nil, reason, nil
		}
		targets = append(targets, abandonedRetirementTarget(summary))
	}
	if !target.Retired && !listed {
		return nil, deliveryIdentityMismatchReason, nil
	}
	return targets, "", nil
}

func abandonedSummaryMatchesTarget(summary journal.SubmissionSummary, target *journal.Submission) bool {
	return summary.SessionID == target.SessionID && summary.IncarnationID == target.IncarnationID &&
		summary.HarnessGeneration == target.HarnessGeneration && summary.StreamID == target.StreamID &&
		summary.Hash == target.Hash && summary.State == target.State &&
		summary.TerminalEventRetained == target.TerminalEventRetained &&
		summary.TerminalSequence == target.TerminalSequence
}

func abandonedRetirementTarget(summary journal.SubmissionSummary) agentruntime.DeliveryRecordRetirementTarget {
	return agentruntime.DeliveryRecordRetirementTarget{
		ID: summary.ID, StreamID: summary.StreamID, Hash: summary.Hash,
		HarnessGeneration: summary.HarnessGeneration, State: summary.State,
	}
}

// abandonedExtraPeerReason requires extra unresolved records to be provably
// abandoned and unowned by an active SQL submission.
func abandonedExtraPeerReason(
	ctx context.Context,
	rows repository.AgentDeliveryRepository,
	summary journal.SubmissionSummary,
	recovery models.AgentDeliveryRecovery,
) (string, error) {
	if summary.State != journal.SubmissionInterruptedUnknown || summary.TerminalEventRetained ||
		summary.TerminalSequence != 0 || summary.Hash == "" || summary.StreamID == "" ||
		summary.HarnessGeneration == 0 || summary.HarnessGeneration > uint64(recovery.HarnessGeneration) {
		return deliveryIdentityMismatchReason, nil
	}
	row, err := rows.GetAgentDeliverySubmission(ctx, summary.ID)
	switch {
	case err == nil:
		if !abandonedTerminalSQLSubmission(row.State) || row.PayloadHash != summary.Hash {
			return deliveryIdentityMismatchReason, nil
		}
		return "", nil
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, models.ErrTaskSessionNotFound),
		errors.Is(err, repository.ErrAgentDeliverySubmissionNotFound):
		return "", nil
	default:
		return "", err
	}
}

func abandonedTerminalSQLSubmission(state models.DeliverySubmissionState) bool {
	return state == models.DeliverySubmissionCompleted || state == models.DeliverySubmissionFailed ||
		state == models.DeliverySubmissionCancelled
}

// retireAbandonedTargets retires the remaining unresolved inventory. A
// non-empty response with handled=true is a blocked result.
func (s *Service) retireAbandonedTargets(
	ctx context.Context,
	retirer deliveryRecordRetirementClient,
	session *models.TaskSession,
	recovery models.AgentDeliveryRecovery,
	targets []agentruntime.DeliveryRecordRetirementTarget,
) (*SessionDeliveryRecoveryResponse, bool) {
	result, err := retirer.RetireDeliveryRecordSubmissions(ctx, agentruntime.DeliveryRecordRetirementRequest{
		TaskID: session.TaskID, SessionID: session.ID, ExecutionID: recovery.AgentExecutionID,
		IncarnationID: recovery.IncarnationID, HarnessGeneration: uint64(recovery.HarnessGeneration),
		Submissions: targets, OriginalRuntime: recovery.OriginalRuntime,
	})
	if err != nil {
		return abandonedBlocked(session, recovery, deliveryRecordEvidenceReason(err)), true
	}
	if result == nil || !result.ProcessTerminated || result.Descriptor.Unresolved ||
		result.Descriptor.SubmissionCount != 0 || len(result.Descriptor.Submissions) != 0 {
		return abandonedBlocked(session, recovery, deliveryIdentityMismatchReason), true
	}
	return nil, false
}

func (s *Service) commitAbandonedSettlement(
	retryCtx context.Context,
	ctx context.Context,
	store repository.AgentDeliveryAbandonedSettlementRepository,
	session *models.TaskSession,
	recovery models.AgentDeliveryRecovery,
	block *models.SessionRecoveryBlock,
	submission *models.AgentDeliverySubmission,
) (*SessionDeliveryRecoveryResponse, bool, error) {
	settled, err := store.SettleAbandonedAgentDeliverySubmission(retryCtx, &models.AgentDeliveryAbandonedSettlementRequest{
		SessionID: session.ID, IncarnationID: recovery.IncarnationID,
		HarnessGeneration: recovery.HarnessGeneration, SubmissionID: recovery.SubmissionID,
		DeliveryStreamID: block.DeliveryStreamID, ExpectedPayloadHash: submission.PayloadHash,
		ExpectedState: models.DeliverySubmissionInterruptedUnknown, BlockID: block.ID,
	})
	if err != nil {
		response, reason := s.abandonedSettlementError(session, recovery, err)
		if reason != "" {
			return response, true, nil
		}
		return nil, true, err
	}
	if !settled.AlreadySettled {
		if restoreErr := s.restorePendingQueueDispatchesForRecovery(ctx, session.ID); restoreErr != nil {
			s.logger.Warn("failed to release parked queue work after abandoned settlement",
				zap.String("session_id", session.ID), zap.Error(restoreErr))
		}
	}
	if err := s.clearAgentDeliveryRecoveryNotice(ctx, session.ID, recovery.IncarnationID,
		recovery.SubmissionID, recovery.HarnessGeneration); err != nil {
		s.logger.Warn("failed to clear abandoned delivery recovery notice",
			zap.String("session_id", session.ID), zap.Error(err))
	}
	s.publishAgentDeliveryRecoveryState(ctx, session.ID)
	return sessionDeliveryRecoveryResponse(session.TaskID, session.ID, SessionDeliveryRecoverySettled,
		"", recovery.Revision, recoverySuccessorIdentity(recovery)), true, nil
}

func (s *Service) abandonedSettlementError(
	session *models.TaskSession,
	recovery models.AgentDeliveryRecovery,
	err error,
) (*SessionDeliveryRecoveryResponse, string) {
	switch {
	case errors.Is(err, repository.ErrAgentDeliveryAbandonedSettlementConflict),
		errors.Is(err, repository.ErrAgentDeliverySubmissionNotFound):
		return abandonedBlocked(session, recovery, deliveryIdentityMismatchReason), deliveryIdentityMismatchReason
	case errors.Is(err, repository.ErrAgentDeliveryAbandonedSettlementStale):
		return abandonedBlocked(session, recovery, staleDeliveryRecoveryReason), staleDeliveryRecoveryReason
	default:
		return nil, ""
	}
}

func abandonedBlocked(
	session *models.TaskSession,
	recovery models.AgentDeliveryRecovery,
	reason string,
) *SessionDeliveryRecoveryResponse {
	return sessionDeliveryRecoveryResponse(session.TaskID, session.ID,
		SessionDeliveryRecoveryBlocked, reason, recovery.Revision, nil)
}
