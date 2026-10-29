package models

import (
	"strconv"
	"strings"
	"time"
)

// manualStepWaitIDPrefix namespaces the manual-step wait episode identity
// inside the per-user needs-you sidecar keys. Shared by the orchestrator's
// occurrence ids and the derived waiting-row ids so one episode has exactly
// one identity everywhere (needs-you-inbox-waiting-rows design D2).
const manualStepWaitIDPrefix = "mw"

// ManualStepWaitRow is one derived manual-step waiting row: a task whose
// current workflow step neither starts work on entry nor completes the task,
// with no executing or queued turn. Derived per read; never persisted.
type ManualStepWaitRow struct {
	// ID is the episode identity "mw:<taskID>:<stepID>:<transitionID>", built
	// from the newest task_step_transitions row for the current step.
	ID            string
	TaskID        string
	TaskTitle     string
	WorkspaceID   string
	StepID        string
	StepName      string
	ReferenceTime time.Time
	// SidecarState is this operator's hiding state for the row, or "" when no
	// sidecar row exists. SnoozeUntil is set only while snoozed.
	SidecarState ClarificationSidecarState
	SnoozeUntil  *time.Time
}

// ManualStepWaitOptions filters and paginates ListManualStepWaits.
type ManualStepWaitOptions struct {
	WorkspaceID string
	Limit       int
	// Sidecar selects one operator's dismiss/snooze sidecar rows and a
	// direction, exactly like ListClarificationBundlesOptions.Sidecar.
	Sidecar *ClarificationSidecarFilter
	// OnlyHidden keeps exactly the rows the sidecar is hiding (the hidden
	// enumeration); false excludes them (the main list).
	OnlyHidden bool
}

// ManualStepWaitPage is one page of ListManualStepWaits plus the hidden
// summary the merged read needs from the same source.
type ManualStepWaitPage struct {
	Rows []ManualStepWaitRow
	// HiddenCount is the workspace-wide count of waiting rows this operator's
	// sidecar is hiding, and NextSnoozeExpiry the earliest expiry among them.
	HiddenCount      int
	HasMore          bool
	NextSnoozeExpiry *time.Time
}

// ManualStepWaitRowID builds the episode identity from the immutable ledger
// row id. transitionID 0 is the documented fallback for a step whose entry has
// no ledger row.
func ManualStepWaitRowID(taskID, stepID string, transitionID int64) string {
	return manualStepWaitIDPrefix + ":" + taskID + ":" + stepID + ":" + strconv.FormatInt(transitionID, 10)
}

// ParseManualStepWaitRowID splits a waiting-row id back into its task, step,
// and transition identity. ok is false for anything that is not a well-formed
// manual-step wait id.
func ParseManualStepWaitRowID(id string) (taskID, stepID string, transitionID int64, ok bool) {
	rest, found := strings.CutPrefix(id, manualStepWaitIDPrefix+":")
	if !found {
		return "", "", 0, false
	}
	parts := strings.Split(rest, ":")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return "", "", 0, false
	}
	parsed, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || parsed < 0 {
		return "", "", 0, false
	}
	return parts[0], parts[1], parsed, true
}
