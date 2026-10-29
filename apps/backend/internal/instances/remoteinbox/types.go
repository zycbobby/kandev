// Package remoteinbox fans one registered remote instance's own
// clarification-inbox and merged needs-you surfaces out into single
// aggregated instance pages and forwards answer/sidecar writes back to it.
// It is a request-scoped HTTP client with no goroutines and no lifecycle:
// every read is bounded by one deadline, every write is one forwarded
// request. Remote rows never enter local persistence; the aggregated page is
// a computed view.
package remoteinbox

import (
	"encoding/json"
	"time"
)

// Reason is the closed set of degraded explanations one instance page can
// carry (cross-instance-inbox design, "Contracts").
type Reason string

const (
	// ReasonUnreachable covers dial failures and deadlines.
	ReasonUnreachable Reason = "unreachable"
	// ReasonUnauthorized covers remote 401/403 and local credential reads
	// that cannot produce a usable PAT.
	ReasonUnauthorized Reason = "unauthorized"
	// ReasonInboxDisabled covers a remote whose clarification-inbox routes
	// are absent (features.needsYouInbox off remotely).
	ReasonInboxDisabled Reason = "inbox_disabled"
	// ReasonError is every other non-2xx remote outcome.
	ReasonError Reason = "error"
)

// ReadResult is one instance-page read: exactly one of Page (status ok) or
// Degraded (status degraded) is meaningful.
type ReadResult struct {
	Page     *Page
	Degraded Reason
}

// Bundle is one remote inbox row: the remote's own inbox bundle wire shape
// plus the instance and origin-workspace identity the local Inbox renders.
// Messages is carried undecoded so a newer remote's message fields survive
// the fan-out unchanged. State and SnoozeUntil are populated only by the
// hidden enumeration.
type Bundle struct {
	PendingID    string          `json:"pending_id"`
	TaskID       string          `json:"task_id"`
	SessionID    string          `json:"session_id"`
	SessionState string          `json:"session_state"`
	TaskTitle    string          `json:"task_title"`
	CreatedAt    string          `json:"created_at"`
	Context      string          `json:"context"`
	Messages     json.RawMessage `json:"messages"`
	State        string          `json:"state,omitempty"`
	SnoozeUntil  *string         `json:"snooze_until,omitempty"`
	InstanceID   string          `json:"instance_id"`
	WorkspaceID  string          `json:"workspace_id"`
}

// Page is the aggregated instance page. Count always equals len(Bundles)
// post-cap; HiddenCount sums each workspace page's hidden_count; Total is the
// hidden enumeration's instance-wide total (sum of per-workspace totals).
// NextSnoozeExpiry is the earliest expiry across workspaces.
type Page struct {
	Bundles          []Bundle
	Count            int
	HiddenCount      int
	Total            int
	NextSnoozeExpiry *time.Time
	HasMore          bool
}

// Row-kind discriminants of the remote's merged needs-you rows, matching the
// local aggregated endpoint's row tagging (clarification needs-you handlers).
const (
	// RowKindQuestion tags a clarification question row.
	RowKindQuestion = "question"
	// RowKindWaiting tags a manual-step waiting row.
	RowKindWaiting = "waiting"
)

// NeedsYouRow is one row of the remote's merged needs-you page. The kind
// discriminant says which fields carry meaning: a question row embeds the
// remote's bundle view (pending_id through messages), a waiting row carries
// the task and manual step (id, step_id, step_name, reference_time).
// Messages is carried undecoded so a newer remote's message fields survive
// the fan-out unchanged. State and SnoozeUntil are populated only by the
// hidden enumeration. InstanceID and WorkspaceID are stamped by aggregation,
// never trusted from the remote row.
type NeedsYouRow struct {
	Kind          string          `json:"kind"`
	PendingID     string          `json:"pending_id"`
	TaskID        string          `json:"task_id"`
	SessionID     string          `json:"session_id"`
	SessionState  string          `json:"session_state"`
	TaskTitle     string          `json:"task_title"`
	CreatedAt     string          `json:"created_at"`
	Context       string          `json:"context"`
	Messages      json.RawMessage `json:"messages"`
	ID            string          `json:"id"`
	StepID        string          `json:"step_id"`
	StepName      string          `json:"step_name"`
	ReferenceTime string          `json:"reference_time"`
	State         string          `json:"state,omitempty"`
	SnoozeUntil   *string         `json:"snooze_until,omitempty"`
	InstanceID    string          `json:"instance_id"`
	WorkspaceID   string          `json:"workspace_id"`
}

// NeedsYouPage is the aggregated instance page of the remote's merged
// needs-you read. Count always equals len(Rows) post-cap; HiddenCount sums
// each workspace page's hidden_count; Total is the hidden enumeration's
// instance-wide total (sum of per-workspace totals). NextSnoozeExpiry is the
// earliest expiry across workspaces.
type NeedsYouPage struct {
	Rows             []NeedsYouRow
	Count            int
	HiddenCount      int
	Total            int
	NextSnoozeExpiry *time.Time
	HasMore          bool
}

// NeedsYouReadResult is one instance-page merged needs-you read: exactly one
// of Page (status ok) or Degraded (status degraded) is meaningful.
type NeedsYouReadResult struct {
	Page     *NeedsYouPage
	Degraded Reason
}
