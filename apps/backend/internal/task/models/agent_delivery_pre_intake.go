package models

import "time"

// DeliveryPreIntakeEffectType records that a durable submission was proven
// never to have entered the retained agentctl journal. It is deliberately a
// separate effect type: a pre-intake settlement has no terminal event, stream,
// sequence, or turn, so it must never be mistaken for a terminal settlement.
const DeliveryPreIntakeEffectType = "agent_delivery.pre_intake_settlement"

// DeliveryPreIntakeEffectKey is the idempotency key for one settled absence.
func DeliveryPreIntakeEffectKey(submissionID string) string {
	return DeliveryPreIntakeEffectType + ":" + submissionID
}

// AgentDeliveryPreIntakeSettlementRequest settles one submission that never
// reached the agentctl journal. ExpectedState and ExpectedOutcome are the
// caller's evidence; the transaction revalidates them against the locked row.
type AgentDeliveryPreIntakeSettlementRequest struct {
	SessionID         string
	IncarnationID     string
	HarnessGeneration int64
	SubmissionID      string
	ExpectedState     DeliverySubmissionState
	ExpectedOutcome   string
	BlockID           string
	SettledAt         time.Time
}

// AgentDeliveryPreIntakeSettlementResult reports the committed settlement.
// AlreadySettled is true when a previous call had committed the same effect.
type AgentDeliveryPreIntakeSettlementResult struct {
	Settled          bool
	AlreadySettled   bool
	Submission       *AgentDeliverySubmission
	ResolvedBlockIDs []string
}
