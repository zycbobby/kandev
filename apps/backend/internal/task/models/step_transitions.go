package models

import "time"

// StepTransition is the public-safe recorded movement of one task.
type StepTransition struct {
	ID                 int64
	FromWorkflowID     *string
	FromWorkflowStepID *string
	ToWorkflowID       *string
	ToWorkflowStepID   *string
	Trigger            string
	// ActorKind and ActorID carry the ledger row's attribution: what kind of
	// actor caused the move and its opaque identifier. ActorID is empty when
	// the row stored NULL.
	ActorKind  string
	ActorID    string
	OccurredAt time.Time
}

// TransitionGroup is one route through, into, or out of a workflow.
type TransitionGroup struct {
	Kind       string
	FromStepID *string
	ToStepID   *string
	Count      int64
}
