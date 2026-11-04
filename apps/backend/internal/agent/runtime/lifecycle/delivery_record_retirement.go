package lifecycle

import (
	"context"
	"errors"
	"net/http"
	"strings"

	agentctl "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	"github.com/kandev/kandev/internal/agentctl/journal"
	"github.com/kandev/kandev/internal/common/processidentity"
)

// DeliveryRecordRetirementTarget names one exact abandoned submission. State
// is required so a stale or live record can never be retired by identity alone.
type DeliveryRecordRetirementTarget struct {
	ID                string                  `json:"id"`
	StreamID          string                  `json:"stream_id"`
	Hash              string                  `json:"hash"`
	HarnessGeneration uint64                  `json:"harness_generation"`
	State             journal.SubmissionState `json:"state"`
}

// DeliveryRecordRetirementRequest retires the complete unresolved inventory of
// one historical prompt owner. OriginalRuntime must prove the owner terminated;
// a live successor instance may still own the same session.
type DeliveryRecordRetirementRequest struct {
	TaskID            string
	SessionID         string
	ExecutionID       string
	IncarnationID     string
	HarnessGeneration uint64
	Submissions       []DeliveryRecordRetirementTarget
	OriginalRuntime   processidentity.Identity
}

// DeliveryRecordRetirementResult reports the post-retirement descriptor and
// the preserved records. It never carries prompt payloads.
type DeliveryRecordRetirementResult struct {
	Descriptor        journal.RecoveryDescriptor
	Retired           []journal.Submission
	ProcessTerminated bool
}

// RetireDeliveryRecordSubmissions marks abandoned submissions retired in the
// journal that owns them. A live instance is retired through its per-instance
// transport; an unowned session is retired through the retained control route.
// The historical owner must be proven dead and every target must match the
// current descriptor before anything changes.
func (m *Manager) RetireDeliveryRecordSubmissions(
	ctx context.Context,
	request DeliveryRecordRetirementRequest,
) (*DeliveryRecordRetirementResult, error) {
	if !validDeliveryRecordRetirementRequest(request) {
		return nil, errors.Join(ErrDeliveryOwnerMismatch, ErrDeliveryRecoveryBlocked)
	}
	lease, control, err := m.acquireDeliveryRecordEvidenceLease(ctx)
	if err != nil {
		return nil, err
	}
	defer lease.Close()
	instances, err := control.ListInstances(ctx)
	if err != nil {
		return nil, deliveryEvidenceReadError(lease, err)
	}
	instance, err := matchDeliveryRecordInstance(instances, deliveryRecordRetirementEvidenceRequest(request))
	if err != nil {
		return nil, err
	}
	if !deliveryRecordRetirementOwnerTerminated(request, instance) {
		return nil, errors.Join(ErrDeliveryOwnerMismatch, ErrDeliveryRecoveryBlocked)
	}
	if instance == nil {
		return m.retireRetainedDeliveryRecordSubmissions(ctx, control, request)
	}
	return m.retireLiveDeliveryRecordSubmissions(ctx, lease, instance, request)
}

func validDeliveryRecordRetirementRequest(request DeliveryRecordRetirementRequest) bool {
	if request.TaskID == "" || request.SessionID == "" || request.ExecutionID == "" ||
		request.IncarnationID == "" || request.HarnessGeneration == 0 || len(request.Submissions) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(request.Submissions))
	for _, target := range request.Submissions {
		if !validDeliveryRecordRetirementTarget(target, request.HarnessGeneration) {
			return false
		}
		if _, duplicate := seen[target.ID]; duplicate {
			return false
		}
		seen[target.ID] = struct{}{}
	}
	return true
}

func validDeliveryRecordRetirementTarget(target DeliveryRecordRetirementTarget, requestGeneration uint64) bool {
	return target.ID != "" && target.StreamID != "" && target.Hash != "" &&
		target.HarnessGeneration > 0 && target.HarnessGeneration <= requestGeneration &&
		target.State == journal.SubmissionInterruptedUnknown
}

func deliveryRecordRetirementEvidenceRequest(request DeliveryRecordRetirementRequest) DeliveryRecordEvidenceRequest {
	return DeliveryRecordEvidenceRequest{
		TaskID: request.TaskID, SessionID: request.SessionID, ExecutionID: request.ExecutionID,
		IncarnationID: request.IncarnationID, HarnessGeneration: request.HarnessGeneration,
		OriginalRuntime: &request.OriginalRuntime,
	}
}

func deliveryRecordRetirementOwnerTerminated(request DeliveryRecordRetirementRequest, instance *agentctl.InstanceInfo) bool {
	liveExecutionID := ""
	if instance != nil {
		liveExecutionID = instance.ID
	}
	terminated := historicalProcessTermination(DeliveryRecordEvidenceRequest{
		ExecutionID: request.ExecutionID, OriginalRuntime: &request.OriginalRuntime,
	}, liveExecutionID)
	return terminated != nil && *terminated
}

// validateDeliveryRecordRetirementTargets requires the targets to enumerate the
// complete unresolved inventory and match each summary exactly. Terminal and
// non-interrupted records fail closed instead of being retired.
func validateDeliveryRecordRetirementTargets(descriptor journal.RecoveryDescriptor, request DeliveryRecordRetirementRequest) error {
	if descriptor.SubmissionsTruncated {
		return errors.Join(ErrDeliveryRecordEvidenceTruncated, ErrDeliveryRecoveryBlocked)
	}
	inventory := make(map[string]journal.SubmissionSummary, len(descriptor.Submissions))
	for _, summary := range descriptor.Submissions {
		if _, duplicate := inventory[summary.ID]; duplicate {
			return errors.Join(ErrDeliveryRecordEvidenceConflict, ErrDeliveryRecoveryBlocked)
		}
		inventory[summary.ID] = summary
	}
	for _, target := range request.Submissions {
		summary, listed := inventory[target.ID]
		if !listed || !deliveryRecordRetirementTargetMatches(target, summary) {
			return errors.Join(ErrDeliveryRecordEvidenceConflict, ErrDeliveryRecoveryBlocked)
		}
		delete(inventory, target.ID)
	}
	if len(inventory) != 0 {
		return errors.Join(ErrDeliveryRecordEvidenceConflict, ErrDeliveryRecoveryBlocked)
	}
	return nil
}

func deliveryRecordRetirementTargetMatches(target DeliveryRecordRetirementTarget, summary journal.SubmissionSummary) bool {
	if summary.StreamID != target.StreamID || summary.Hash != target.Hash ||
		summary.HarnessGeneration != target.HarnessGeneration || summary.State != target.State ||
		summary.State != journal.SubmissionInterruptedUnknown {
		return false
	}
	return !summary.TerminalEventRetained && summary.TerminalSequence == 0
}

func (m *Manager) retireRetainedDeliveryRecordSubmissions(
	ctx context.Context,
	control *agentctl.ControlClient,
	request DeliveryRecordRetirementRequest,
) (*DeliveryRecordRetirementResult, error) {
	evidence, err := control.ReadRetainedReconstructionEvidence(ctx, journal.RetainedReconstructionEvidenceRequest{
		Root: m.dataDir, SessionID: request.SessionID, IncarnationID: request.IncarnationID,
		HarnessGeneration: request.HarnessGeneration, OriginalRuntime: &request.OriginalRuntime,
	})
	if err != nil {
		return nil, deliveryRetirementControlError(err)
	}
	if err := validateDeliveryRecordDescriptor(evidence.Descriptor, deliveryRecordRetirementEvidenceRequest(request)); err != nil {
		return nil, err
	}
	if err := validateDeliveryRecordRetirementTargets(evidence.Descriptor, request); err != nil {
		return nil, err
	}
	response, err := control.RetireRetainedDelivery(ctx, journal.RetainedRetirementRequest{
		Root: m.dataDir, SessionID: request.SessionID, ExecutionID: request.ExecutionID,
		IncarnationID: request.IncarnationID, HarnessGeneration: request.HarnessGeneration,
		Submissions:     retainedRetirementTargets(request.Submissions),
		OriginalRuntime: request.OriginalRuntime,
	})
	if err != nil {
		return nil, deliveryRetirementControlError(err)
	}
	if !response.ProcessTerminated || response.Descriptor.Unresolved ||
		response.Descriptor.SubmissionCount != 0 || len(response.Descriptor.Submissions) != 0 {
		return nil, errors.Join(ErrDeliveryRecordEvidenceConflict, ErrDeliveryRecoveryBlocked)
	}
	return &DeliveryRecordRetirementResult{
		Descriptor: response.Descriptor, Retired: response.Submissions, ProcessTerminated: true,
	}, nil
}

func retainedRetirementTargets(targets []DeliveryRecordRetirementTarget) []journal.RetainedRetirementTarget {
	result := make([]journal.RetainedRetirementTarget, 0, len(targets))
	for _, target := range targets {
		result = append(result, journal.RetainedRetirementTarget{
			ID: target.ID, StreamID: target.StreamID, Hash: target.Hash,
			HarnessGeneration: target.HarnessGeneration, State: target.State,
		})
	}
	return result
}

func (m *Manager) retireLiveDeliveryRecordSubmissions(
	ctx context.Context,
	lease *agentctl.RuntimeLease,
	instance *agentctl.InstanceInfo,
	request DeliveryRecordRetirementRequest,
) (*DeliveryRecordRetirementResult, error) {
	if !instance.ListenerActive || instance.Port <= 0 {
		return nil, ErrDeliveryRecoveryBlocked
	}
	client := lease.NewBoundInstanceClient(instance.Port, m.logger)
	if client == nil {
		return nil, ErrDeliveryTransportUnavailable
	}
	defer client.Close()
	descriptor, err := client.GetDeliveryStatus(ctx, "")
	if err != nil {
		return nil, deliveryRetirementControlError(err)
	}
	if err := validateDeliveryRecordDescriptor(*descriptor, deliveryRecordRetirementEvidenceRequest(request)); err != nil {
		return nil, err
	}
	if err := validateDeliveryRecordRetirementTargets(*descriptor, request); err != nil {
		return nil, err
	}
	for _, target := range request.Submissions {
		if err := client.RetireDeliverySubmission(ctx, target.ID); err != nil {
			return nil, deliveryRetirementControlError(err)
		}
	}
	return confirmLiveDeliveryRecordRetirement(ctx, client, request)
}

func confirmLiveDeliveryRecordRetirement(
	ctx context.Context,
	client *agentctl.Client,
	request DeliveryRecordRetirementRequest,
) (*DeliveryRecordRetirementResult, error) {
	descriptor, err := client.GetDeliveryStatus(ctx, "")
	if err != nil {
		return nil, deliveryRetirementControlError(err)
	}
	if descriptor.Unresolved || descriptor.SubmissionCount != 0 || len(descriptor.Submissions) != 0 {
		return nil, errors.Join(ErrDeliveryRecordEvidenceConflict, ErrDeliveryRecoveryBlocked)
	}
	retired := make([]journal.Submission, 0, len(request.Submissions))
	for _, target := range request.Submissions {
		record, err := client.GetDeliverySubmission(ctx, target.ID)
		if err != nil {
			return nil, deliveryRetirementControlError(err)
		}
		if !deliveryRecordRetirementRecordPreserved(record, target) {
			return nil, errors.Join(ErrDeliveryRecordEvidenceConflict, ErrDeliveryRecoveryBlocked)
		}
		record.Payload = nil
		retired = append(retired, *record)
	}
	return &DeliveryRecordRetirementResult{
		Descriptor: *descriptor, Retired: retired, ProcessTerminated: true,
	}, nil
}

func deliveryRecordRetirementRecordPreserved(record *journal.Submission, target DeliveryRecordRetirementTarget) bool {
	return record != nil && record.Retired && record.StreamID == target.StreamID &&
		record.Hash == target.Hash && record.HarnessGeneration == target.HarnessGeneration &&
		record.State == target.State && !record.TerminalEventRetained && record.TerminalSequence == 0
}

func deliveryRetirementControlError(err error) error {
	var httpErr *agentctl.DeliveryHTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case http.StatusConflict:
			if strings.Contains(httpErr.Body, "OWNER_MISMATCH") {
				return errors.Join(ErrDeliveryOwnerMismatch, ErrDeliveryRecoveryBlocked, err)
			}
			return errors.Join(ErrDeliveryRecordEvidenceConflict, ErrDeliveryRecoveryBlocked, err)
		case http.StatusNotFound:
			return errors.Join(ErrDeliveryRecordEvidenceStale, ErrDeliveryRecoveryBlocked, err)
		case http.StatusRequestEntityTooLarge:
			return errors.Join(ErrDeliveryRecordEvidenceConflict, ErrDeliveryRecoveryBlocked, err)
		}
		return errors.Join(ErrDeliveryRecoveryBlocked, err)
	}
	return err
}
