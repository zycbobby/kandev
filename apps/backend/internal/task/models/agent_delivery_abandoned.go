package models

import "time"

// DeliveryAbandonedEffectType records that a durable submission was proven to
// be abandoned inside the retained agentctl journal and its records were
// retired. It is deliberately separate from the pre-intake effect: the prompt
// did enter the journal and its history stays retained, so it must never be
// mistaken for a prompt that never arrived.
const DeliveryAbandonedEffectType = "agent_delivery.abandoned_journal_settlement"

// DeliveryAbandonedAuthorizedAction is the operator action recorded on the
// resolved recovery block.
const DeliveryAbandonedAuthorizedAction = "durable_delivery_abandoned"

// DeliveryAbandonedEffectKey is the idempotency key for one abandoned
// settlement.
func DeliveryAbandonedEffectKey(submissionID string) string {
	return DeliveryAbandonedEffectType + ":" + submissionID
}

// AgentDeliveryAbandonedSettlementRequest settles one submission whose
// retained journal records were retired after the historical owner was proven
// dead. Every identity field is evidence the transaction revalidates against
// the locked row and its bound block.
type AgentDeliveryAbandonedSettlementRequest struct {
	SessionID           string
	IncarnationID       string
	HarnessGeneration   int64
	SubmissionID        string
	DeliveryStreamID    string
	ExpectedPayloadHash string
	ExpectedState       DeliverySubmissionState
	BlockID             string
	SettledAt           time.Time
}

// AgentDeliveryAbandonedSettlementResult reports the committed settlement.
// AlreadySettled is true when a previous call had committed the same effect.
type AgentDeliveryAbandonedSettlementResult struct {
	Settled          bool
	AlreadySettled   bool
	Submission       *AgentDeliverySubmission
	ResolvedBlockIDs []string
}
