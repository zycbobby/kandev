package lifecycle

import (
	"context"
	"errors"

	agentctl "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	"github.com/kandev/kandev/internal/agentctl/journal"
)

func (m *Manager) inspectDeliveryRecordTransport(ctx context.Context, lease *agentctl.RuntimeLease, control *agentctl.ControlClient, instance *agentctl.InstanceInfo, request DeliveryRecordEvidenceRequest) (*DeliveryRecordEvidence, error) {
	if instance == nil {
		return m.inspectRetainedDeliveryRecord(ctx, lease, control, request)
	}
	return m.inspectLiveDeliveryRecord(ctx, lease, instance, request)
}

func (m *Manager) inspectRetainedDeliveryRecord(ctx context.Context, lease *agentctl.RuntimeLease, control *agentctl.ControlClient, request DeliveryRecordEvidenceRequest) (*DeliveryRecordEvidence, error) {
	input := journal.RetainedReconstructionEvidenceRequest{
		Root: m.dataDir, SessionID: request.SessionID, IncarnationID: request.IncarnationID,
		HarnessGeneration: request.HarnessGeneration, OriginalRuntime: request.OriginalRuntime, RetiredSubmissionID: request.RetiredSubmissionID, StoredSubmissionID: request.StoredSubmissionID,
		AbsentSubmissionID: request.AbsentSubmissionID,
	}
	retained, readErr := control.ReadRetainedReconstructionEvidence(ctx, input)
	if readErr != nil {
		return nil, deliveryEvidenceReadError(lease, readErr)
	}
	if request.AbsentSubmissionID != "" {
		second, secondErr := control.ReadRetainedReconstructionEvidence(ctx, input)
		if secondErr != nil {
			return nil, deliveryEvidenceReadError(lease, secondErr)
		}
		if !sameDeliveryRecordDescriptor(retained.Descriptor, second.Descriptor) || retained.SubmissionAbsent != second.SubmissionAbsent {
			return nil, errors.Join(ErrDeliveryRecordEvidenceStale, ErrDeliveryRecoveryBlocked)
		}
	}
	return &DeliveryRecordEvidence{
		Descriptor: retained.Descriptor, ProcessTerminated: retained.ProcessTerminated, RetiredSubmission: retained.RetiredSubmission, StoredSubmission: retained.StoredSubmission,
		SubmissionAbsent: retained.SubmissionAbsent,
	}, nil
}

func (m *Manager) inspectLiveDeliveryRecord(ctx context.Context, lease *agentctl.RuntimeLease, instance *agentctl.InstanceInfo, request DeliveryRecordEvidenceRequest) (*DeliveryRecordEvidence, error) {

	if !instance.ListenerActive || instance.Port <= 0 {
		return nil, ErrDeliveryRecoveryBlocked
	}
	client := lease.NewBoundInstanceClient(instance.Port, m.logger)
	if client == nil {
		return nil, ErrDeliveryTransportUnavailable
	}
	defer client.Close()
	descriptor, readErr := client.GetDeliveryStatus(ctx, "")
	if readErr != nil {
		return nil, deliveryEvidenceReadError(lease, readErr)
	}
	result := &DeliveryRecordEvidence{
		Descriptor: *descriptor, LiveExecutionID: instance.ID,
		ProcessTerminated: historicalProcessTermination(request, instance.ID),
	}
	if err := m.readLiveStoredEvidence(ctx, lease, client, request, result); err != nil {
		return nil, err
	}
	return result, nil
}

func validDeliveryRecordSubmissionRequest(request DeliveryRecordSubmissionRequest) bool {
	return request.TaskID != "" && request.SessionID != "" && request.IncarnationID != "" &&
		request.HarnessGeneration != 0 && request.Candidate.ID != "" &&
		request.Candidate.SessionID == request.SessionID && request.Candidate.IncarnationID == request.IncarnationID &&
		request.Candidate.HarnessGeneration == request.HarnessGeneration
}

type deliveryRecordRead struct {
	before, after journal.RecoveryDescriptor
	submission    *journal.Submission
}

func (m *Manager) readDeliveryRecordTransport(ctx context.Context, lease *agentctl.RuntimeLease, control *agentctl.ControlClient, instance *agentctl.InstanceInfo, request DeliveryRecordSubmissionRequest) (*deliveryRecordRead, error) {
	if instance == nil {
		return m.readRetainedDeliveryRecord(ctx, lease, control, request)
	}
	return m.readLiveDeliveryRecord(ctx, lease, instance, request)
}

func (m *Manager) readRetainedDeliveryRecord(ctx context.Context, lease *agentctl.RuntimeLease, control *agentctl.ControlClient, request DeliveryRecordSubmissionRequest) (*deliveryRecordRead, error) {
	var before, after journal.RecoveryDescriptor
	var submission *journal.Submission
	var err error
	input := journal.RetainedReconstructionEvidenceRequest{
		Root: m.dataDir, SessionID: request.SessionID,
		IncarnationID: request.IncarnationID, HarnessGeneration: request.HarnessGeneration,
	}
	first, readErr := control.ReadRetainedReconstructionEvidence(ctx, input)
	if readErr != nil {
		return nil, deliveryEvidenceReadError(lease, readErr)
	}
	before = first.Descriptor
	if err = validateSelectedDeliveryRecord(before, request); err != nil {
		return nil, err
	}
	submission, err = control.ReadRetainedReconstructionSubmission(ctx, journal.RetainedReconstructionSubmissionRequest{
		Root: m.dataDir, SessionID: request.SessionID, IncarnationID: request.IncarnationID,
		HarnessGeneration: request.HarnessGeneration, Candidate: request.Candidate,
	})
	if err != nil {
		return nil, deliveryEvidenceReadError(lease, err)
	}
	second, readErr := control.ReadRetainedReconstructionEvidence(ctx, input)
	if readErr != nil {
		return nil, deliveryEvidenceReadError(lease, readErr)
	}
	after = second.Descriptor
	return &deliveryRecordRead{before: before, after: after, submission: submission}, nil
}

func (m *Manager) readLiveDeliveryRecord(ctx context.Context, lease *agentctl.RuntimeLease, instance *agentctl.InstanceInfo, request DeliveryRecordSubmissionRequest) (*deliveryRecordRead, error) {
	var before, after journal.RecoveryDescriptor
	var submission *journal.Submission
	var err error

	if !instance.ListenerActive || instance.Port <= 0 {
		return nil, ErrDeliveryRecoveryBlocked
	}
	client := lease.NewBoundInstanceClient(instance.Port, m.logger)
	if client == nil {
		return nil, ErrDeliveryTransportUnavailable
	}
	defer client.Close()
	first, readErr := client.GetDeliveryStatus(ctx, "")
	if readErr != nil {
		return nil, deliveryEvidenceReadError(lease, readErr)
	}
	before = *first
	if err = validateSelectedDeliveryRecord(before, request); err != nil {
		return nil, err
	}
	submission, err = client.GetDeliverySubmission(ctx, request.Candidate.ID)
	if err != nil {
		return nil, deliveryEvidenceReadError(lease, err)
	}
	if err = lease.CheckCurrent(); err != nil {
		return nil, err
	}
	second, readErr := client.GetDeliveryStatus(ctx, "")
	if readErr != nil {
		return nil, deliveryEvidenceReadError(lease, readErr)
	}
	after = *second
	return &deliveryRecordRead{before: before, after: after, submission: submission}, nil
}

func (m *Manager) readLiveStoredEvidence(ctx context.Context, lease *agentctl.RuntimeLease, client *agentctl.Client, request DeliveryRecordEvidenceRequest, result *DeliveryRecordEvidence) error {
	for _, id := range []string{request.RetiredSubmissionID, request.StoredSubmissionID} {
		if id == "" {
			continue
		}
		record, err := readLiveSubmissionMetadata(ctx, client, request, id)
		if err != nil {
			return deliveryEvidenceReadError(lease, err)
		}
		if id == request.RetiredSubmissionID {
			if !record.Retired || !journal.HasUnprovenOutcome(*record) {
				return ErrDeliveryOwnerMismatch
			}
			result.RetiredSubmission = record
		}
		if id == request.StoredSubmissionID {
			result.StoredSubmission = record
		}
	}
	if request.AbsentSubmissionID != "" {
		if err := readLiveAbsentSubmission(ctx, client, request, result); err != nil {
			return deliveryEvidenceReadError(lease, err)
		}
	}
	if request.RetiredSubmissionID == "" && request.StoredSubmissionID == "" && request.AbsentSubmissionID == "" {
		return nil
	}
	after, err := client.GetDeliveryStatus(ctx, "")
	if err != nil {
		return deliveryEvidenceReadError(lease, err)
	}
	if !sameNativeResumeProofStatus(&result.Descriptor, after) {
		return ErrDeliveryRecordEvidenceStale
	}
	return nil
}

func readLiveSubmissionMetadata(ctx context.Context, client *agentctl.Client, request DeliveryRecordEvidenceRequest, id string) (*journal.Submission, error) {
	record, err := client.GetDeliverySubmission(ctx, id)
	if err != nil {
		return nil, err
	}
	if record == nil || record.SessionID != request.SessionID || record.IncarnationID != request.IncarnationID || record.HarnessGeneration == 0 || record.HarnessGeneration > request.HarnessGeneration {
		return nil, ErrDeliveryOwnerMismatch
	}
	record.Payload = nil
	return record, nil
}

// readLiveAbsentSubmission marks the exact submission ID as absent only when
// the live owner explicitly denies it with a submission-not-found response.
func readLiveAbsentSubmission(ctx context.Context, client *agentctl.Client, request DeliveryRecordEvidenceRequest, result *DeliveryRecordEvidence) error {
	record, err := client.GetDeliverySubmission(ctx, request.AbsentSubmissionID)
	if agentctl.IsDeliverySubmissionNotFound(err) {
		result.SubmissionAbsent = true
		return nil
	}
	if err != nil {
		return err
	}
	if record == nil {
		return nil
	}
	if record.SessionID != request.SessionID || record.IncarnationID != request.IncarnationID ||
		record.HarnessGeneration == 0 || record.HarnessGeneration > request.HarnessGeneration {
		return ErrDeliveryOwnerMismatch
	}
	return nil
}
