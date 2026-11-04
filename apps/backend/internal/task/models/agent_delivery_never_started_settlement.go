package models

import "time"

// DeliveryNeverStartedEffectType records that a durable submission was settled
// after its prompt generation produced zero turn events and the historical
// owner was proven terminated. It is deliberately a separate effect type: the
// prompt did reach the retained agentctl journal, so it must never be mistaken
// for a pre-intake settlement, and the journal record was retired rather than
// abandoned in place, so it must never be mistaken for an abandoned settlement.
const DeliveryNeverStartedEffectType = "agent_delivery.never_started_settlement"

// DeliveryNeverStartedAuthorizedAction is the operator action recorded on the
// resolved recovery block.
const DeliveryNeverStartedAuthorizedAction = "durable_delivery_never_started"

// DeliveryNeverStartedEffectKey is the idempotency key for one never-started
// settlement.
func DeliveryNeverStartedEffectKey(submissionID string) string {
	return DeliveryNeverStartedEffectType + ":" + submissionID
}

// AgentDeliveryNeverStartedSettlementRequest settles one submission whose
// prompt generation produced zero turn events after the historical owner was
// proven terminated. ExpectedState and ExpectedOutcome are the caller's
// evidence; the transaction revalidates them against the locked row.
type AgentDeliveryNeverStartedSettlementRequest struct {
	SessionID         string
	IncarnationID     string
	HarnessGeneration int64
	SubmissionID      string
	ExpectedState     DeliverySubmissionState
	ExpectedOutcome   string
	BlockID           string
	SettledAt         time.Time
}

// AgentDeliveryNeverStartedSettlementResult reports the committed settlement.
// AlreadySettled is true when a previous call had committed the same effect.
type AgentDeliveryNeverStartedSettlementResult struct {
	Settled          bool
	AlreadySettled   bool
	Submission       *AgentDeliverySubmission
	ResolvedBlockIDs []string
}
