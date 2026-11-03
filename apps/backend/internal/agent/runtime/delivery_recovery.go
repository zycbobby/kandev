package runtime

import (
	agentctl "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	"github.com/kandev/kandev/internal/agent/runtime/lifecycle"
)

// Delivery recovery types preserve the lifecycle contract at the runtime boundary.
type AgentDeliveryRecoveryIdentity = lifecycle.AgentDeliveryRecoveryIdentity
type DeliveryReconciliationOutcome = lifecycle.DeliveryReconciliationOutcome

const (
	DeliveryReconciliationRunningAttached      = lifecycle.DeliveryReconciliationRunningAttached
	DeliveryReconciliationTerminalSettled      = lifecycle.DeliveryReconciliationTerminalSettled
	DeliveryReconciliationUncertain            = lifecycle.DeliveryReconciliationUncertain
	DeliveryReconciliationBlocked              = lifecycle.DeliveryReconciliationBlocked
	DeliveryReconciliationOwnerMismatch        = lifecycle.DeliveryReconciliationOwnerMismatch
	DeliveryReconciliationTransportUnavailable = lifecycle.DeliveryReconciliationTransportUnavailable
)

var (
	ErrDeliveryRecoveryBlocked = lifecycle.ErrDeliveryRecoveryBlocked
	ErrDeliveryOwnerMismatch   = lifecycle.ErrDeliveryOwnerMismatch
	ErrUncertainPromptDelivery = lifecycle.ErrUncertainPromptDelivery
	// ErrDurableDeliveryAdmissionRejected means the peer refused the prompt
	// before writing it, so no journal recovery is required.
	ErrDurableDeliveryAdmissionRejected = agentctl.ErrDurableDeliveryAdmissionRejected
)
