// Package statsync ingests versioned raw-row payloads pushed by other
// Kandev instances and stores them in dedicated mirror tables, so the
// analytics queries can render merged statistics without touching core
// task tables.
package statsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SchemaVersion is the payload contract version this build ingests. A
// payload declaring any other version is rejected as a whole.
const SchemaVersion = 1

// HeaderName is the shared-secret header the ingest endpoint authenticates.
const HeaderName = "X-Statsync-Secret"

// Mode distinguishes incremental pushes from periodic full reconciliations.
type Mode string

const (
	ModeDelta     Mode = "delta"
	ModeReconcile Mode = "reconcile"
)

// Contract validation errors. Handlers map them to HTTP statuses; the
// sentinel identity keeps the mapping out of the parsing code.
var (
	ErrUnsupportedSchemaVersion = errors.New("unsupported schema_version")
	ErrMissingSourceInstance    = errors.New("source_instance is required")
	ErrInvalidMode              = errors.New("mode must be delta or reconcile")
	ErrMissingInventory         = errors.New("mode=reconcile requires an inventory")
	ErrInvalidRow               = errors.New("invalid row")
)

// RepositoryRow mirrors one source-side repositories row. Matching into a
// local repository happens at ingest by name; the stored uid only needs to
// stay stable across pushes.
type RepositoryRow struct {
	UID       string `json:"uid"`
	Name      string `json:"name"`
	RemoteURL string `json:"remote_url,omitempty"`

	// ResolvedWorkspaceID is the local workspace id the ingest service
	// resolved for this source. Filled in-process, not part of the wire
	// contract; it scopes remote-only repositories to the mapped workspace.
	ResolvedWorkspaceID string `json:"-"`
	// MatchedLocalID is set when the remote repository matched a local
	// repository by name. Matched rows are not mirrored — their statistics
	// merge through the local repository id — so only remote-only rows
	// (empty MatchedLocalID) land in statsync_repositories.
	MatchedLocalID string `json:"-"`
}

// TaskRow mirrors one source-side tasks row. OrigWorkspaceID carries the
// remote workspace identity; the local (mapped) workspace id is resolved at
// ingest and never travels in the payload.
type TaskRow struct {
	UID             string  `json:"uid"`
	OrigWorkspaceID string  `json:"orig_workspace_id"`
	Title           string  `json:"title,omitempty"`
	State           string  `json:"state,omitempty"`
	ArchivedAt      *string `json:"archived_at,omitempty"`
	// Completed is the remote's best-effort completed flag (archived, or the
	// current step is the workflow's last). Nil falls back to the archived
	// mirror, matching the local last-step rule's unresolvable arm.
	Completed *bool  `json:"completed,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`

	// ResolvedWorkspaceID is the local workspace id the ingest service
	// resolved from the source mapping. It is filled in-process, not part of
	// the wire contract, and is what gets stored beside OrigWorkspaceID.
	ResolvedWorkspaceID string `json:"-"`
}

// WorkspaceID returns the resolved local workspace id for storage.
func (row TaskRow) WorkspaceID() string {
	return row.ResolvedWorkspaceID
}

// TaskRepositoryRow mirrors one source-side task_repositories link.
// RepositoryID is the local repository id when the remote repository matched
// one by name, otherwise the mirror repository uid.
type TaskRepositoryRow struct {
	UID          string `json:"uid"`
	TaskUID      string `json:"task_uid"`
	RepositoryID string `json:"repository_id"`
	CreatedAt    string `json:"created_at,omitempty"`
}

// SessionRow mirrors one source-side task_sessions row. The snapshot is
// already trimmed by the sender to the analytics-relevant keys
// ({model, model_name, llm}); conversation content never travels.
type SessionRow struct {
	UID                  string          `json:"uid"`
	TaskUID              string          `json:"task_uid"`
	StartedAt            string          `json:"started_at"`
	CompletedAt          *string         `json:"completed_at,omitempty"`
	RepositoryID         string          `json:"repository_id,omitempty"`
	AgentProfileSnapshot json.RawMessage `json:"agent_profile_snapshot,omitempty"`
}

// TurnRow mirrors one source-side task_session_turns row.
type TurnRow struct {
	UID         string  `json:"uid"`
	SessionUID  string  `json:"session_uid"`
	StartedAt   string  `json:"started_at"`
	CompletedAt *string `json:"completed_at,omitempty"`
}

// MessageRow mirrors one source-side task_session_messages row without the
// message content, which no statistics section consumes.
type MessageRow struct {
	UID        string `json:"uid"`
	SessionUID string `json:"session_uid"`
	TurnUID    string `json:"turn_id,omitempty"`
	AuthorType string `json:"author_type"`
	Type       string `json:"type"`
	CreatedAt  string `json:"created_at"`
}

// CommitRow mirrors one source-side task_session_commits row.
type CommitRow struct {
	UID          string `json:"uid"`
	SessionUID   string `json:"session_uid"`
	CommitSHA    string `json:"commit_sha,omitempty"`
	CommittedAt  string `json:"committed_at"`
	FilesChanged *int64 `json:"files_changed,omitempty"`
	Insertions   *int64 `json:"insertions,omitempty"`
	Deletions    *int64 `json:"deletions,omitempty"`
}

// GitSnapshotRow mirrors one source-side task_session_git_snapshots row.
// Snapshots are optional in the payload: older source builds may be unable to
// produce them, in which case the peak-LOC section has no mirrored data.
type GitSnapshotRow struct {
	UID          string          `json:"uid"`
	SessionUID   string          `json:"session_uid"`
	SnapshotType string          `json:"snapshot_type,omitempty"`
	Files        json.RawMessage `json:"files,omitempty"`
	CreatedAt    string          `json:"created_at"`
}

// Inventory is the full uid inventory a reconcile push carries: every uid the
// source still holds, per row category. Mirror rows of the pushing source
// outside these lists are deleted so remote deletions converge.
type Inventory struct {
	Repositories     []string `json:"repositories,omitempty"`
	Tasks            []string `json:"tasks,omitempty"`
	TaskRepositories []string `json:"task_repositories,omitempty"`
	Sessions         []string `json:"sessions,omitempty"`
	Turns            []string `json:"turns,omitempty"`
	Messages         []string `json:"messages,omitempty"`
	Commits          []string `json:"commits,omitempty"`
	GitSnapshots     []string `json:"git_snapshots,omitempty"`
}

// Envelope is the versioned ingest payload. Timestamps are RFC3339 strings
// from the source instance, preserved verbatim.
type Envelope struct {
	SchemaVersion    int                 `json:"schema_version"`
	SourceInstance   string              `json:"source_instance"`
	Mode             Mode                `json:"mode"`
	Repositories     []RepositoryRow     `json:"repositories,omitempty"`
	Tasks            []TaskRow           `json:"tasks,omitempty"`
	TaskRepositories []TaskRepositoryRow `json:"task_repositories,omitempty"`
	Sessions         []SessionRow        `json:"sessions,omitempty"`
	Turns            []TurnRow           `json:"turns,omitempty"`
	Messages         []MessageRow        `json:"messages,omitempty"`
	Commits          []CommitRow         `json:"commits,omitempty"`
	GitSnapshots     []GitSnapshotRow    `json:"git_snapshots,omitempty"`
	Inventory        *Inventory          `json:"inventory,omitempty"`
}

// ValidateEnvelope checks the envelope contract as a whole: header fields,
// per-row required columns, timestamp parseability, and uid ownership.
// Nothing here touches the database — the repository write path is a
// separate concern.
func ValidateEnvelope(env *Envelope) error {
	if env.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: got %d, want %d", ErrUnsupportedSchemaVersion, env.SchemaVersion, SchemaVersion)
	}
	if env.SourceInstance == "" {
		return ErrMissingSourceInstance
	}
	if env.Mode != ModeDelta && env.Mode != ModeReconcile {
		return fmt.Errorf("%w: got %q", ErrInvalidMode, env.Mode)
	}
	if env.Mode == ModeReconcile && env.Inventory == nil {
		return ErrMissingInventory
	}
	if err := validateInventory(env.SourceInstance, env.Inventory); err != nil {
		return err
	}
	return validateEnvelopeRows(env)
}

// validateInventory checks that every inventory uid belongs to the source.
// This is its own pass because the inventory is the reconcile deletion
// scope: a foreign uid there would delete rows the source never owned.
func validateInventory(source string, inv *Inventory) error {
	if inv == nil {
		return nil
	}
	prefix := source + ":"
	for i, uid := range flattenInventory(inv) {
		if err := validateOwnedUID(prefix, uid); err != nil {
			return fmt.Errorf("%w: inventory[%d]: %s", ErrInvalidRow, i, err)
		}
	}
	return nil
}

func validateEnvelopeRows(env *Envelope) error {
	prefix := env.SourceInstance + ":"
	for i, row := range env.Repositories {
		if err := validateRepositoryRow(prefix, row); err != nil {
			return rowError("repositories", i, err)
		}
	}
	for i, row := range env.Tasks {
		if err := validateTaskRow(prefix, row); err != nil {
			return rowError("tasks", i, err)
		}
	}
	for i, row := range env.TaskRepositories {
		if err := validateTaskRepositoryRow(prefix, row); err != nil {
			return rowError("task_repositories", i, err)
		}
	}
	for i, row := range env.Sessions {
		if err := validateSessionRow(prefix, row); err != nil {
			return rowError("sessions", i, err)
		}
	}
	return validateChildRows(prefix, env)
}

// validateChildRows validates the session-owned categories (turns, messages,
// commits, git snapshots). Split from the task-owned categories only to keep
// each validator's complexity bounded; the contract is one envelope.
func validateChildRows(prefix string, env *Envelope) error {
	for i, row := range env.Turns {
		if err := validateTurnRow(prefix, row); err != nil {
			return rowError("turns", i, err)
		}
	}
	for i, row := range env.Messages {
		if err := validateMessageRow(prefix, row); err != nil {
			return rowError("messages", i, err)
		}
	}
	for i, row := range env.Commits {
		if err := validateCommitRow(prefix, row); err != nil {
			return rowError("commits", i, err)
		}
	}
	for i, row := range env.GitSnapshots {
		if err := validateGitSnapshotRow(prefix, row); err != nil {
			return rowError("git_snapshots", i, err)
		}
	}
	return nil
}

// ParseTimestamp parses an RFC3339 payload timestamp. Stored mirror rows and
// core rows share the same timestamp shape, so the write path must hand the
// driver a real time.Time, not the wire string.
func ParseTimestamp(value string) (time.Time, error) {
	return time.Parse(time.RFC3339, value)
}

func validateRepositoryRow(prefix string, row RepositoryRow) error {
	return validateOwnedUID(prefix, row.UID)
}

func validateTaskRow(prefix string, row TaskRow) error {
	if err := firstError(
		validateOwnedUID(prefix, row.UID),
		requireNonEmpty("orig_workspace_id", row.OrigWorkspaceID),
		requireNonEmpty("created_at", row.CreatedAt),
		requireNonEmpty("updated_at", row.UpdatedAt),
	); err != nil {
		return err
	}
	return validateTimestamps(rowTimestampFields(row.ArchivedAt, row.CreatedAt, row.UpdatedAt))
}

func validateTaskRepositoryRow(prefix string, row TaskRepositoryRow) error {
	if err := firstError(
		validateOwnedUID(prefix, row.UID),
		requireNonEmpty("task_uid", row.TaskUID),
		requireNonEmpty("repository_id", row.RepositoryID),
	); err != nil {
		return err
	}
	return validateTimestamps([]string{row.CreatedAt})
}

func validateSessionRow(prefix string, row SessionRow) error {
	if err := firstError(
		validateOwnedUID(prefix, row.UID),
		requireNonEmpty("task_uid", row.TaskUID),
		requireNonEmpty("started_at", row.StartedAt),
	); err != nil {
		return err
	}
	if len(row.AgentProfileSnapshot) > 0 && !json.Valid(row.AgentProfileSnapshot) {
		return errors.New("agent_profile_snapshot is not valid JSON")
	}
	return validateTimestamps(rowTimestampFields(row.CompletedAt, row.StartedAt))
}

func validateTurnRow(prefix string, row TurnRow) error {
	if err := firstError(
		validateOwnedUID(prefix, row.UID),
		requireNonEmpty("session_uid", row.SessionUID),
		requireNonEmpty("started_at", row.StartedAt),
	); err != nil {
		return err
	}
	return validateTimestamps(rowTimestampFields(row.CompletedAt, row.StartedAt))
}

func validateMessageRow(prefix string, row MessageRow) error {
	if err := firstError(
		validateOwnedUID(prefix, row.UID),
		requireNonEmpty("session_uid", row.SessionUID),
		requireNonEmpty("author_type", row.AuthorType),
		requireNonEmpty("type", row.Type),
		requireNonEmpty("created_at", row.CreatedAt),
	); err != nil {
		return err
	}
	return validateTimestamps([]string{row.CreatedAt})
}

func validateCommitRow(prefix string, row CommitRow) error {
	if err := firstError(
		validateOwnedUID(prefix, row.UID),
		requireNonEmpty("session_uid", row.SessionUID),
		requireNonEmpty("committed_at", row.CommittedAt),
	); err != nil {
		return err
	}
	return validateTimestamps([]string{row.CommittedAt})
}

func validateGitSnapshotRow(prefix string, row GitSnapshotRow) error {
	if err := firstError(
		validateOwnedUID(prefix, row.UID),
		requireNonEmpty("session_uid", row.SessionUID),
		requireNonEmpty("created_at", row.CreatedAt),
	); err != nil {
		return err
	}
	if len(row.Files) > 0 && !json.Valid(row.Files) {
		return errors.New("files is not valid JSON")
	}
	return validateTimestamps([]string{row.CreatedAt})
}

// firstError returns the first non-nil error, so a row's problems surface in
// field order instead of the last one checked winning.
func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func requireNonEmpty(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	return nil
}

func validateOwnedUID(prefix, uid string) error {
	if uid == "" {
		return errors.New("uid is required")
	}
	if len(uid) <= len(prefix) || uid[:len(prefix)] != prefix {
		return fmt.Errorf("uid %q must be namespaced as %q + original id", uid, prefix[:len(prefix)-1])
	}
	return nil
}

func validateTimestamps(values []string) error {
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, err := ParseTimestamp(value); err != nil {
			return fmt.Errorf("timestamp %q is not RFC3339: %w", value, err)
		}
	}
	return nil
}

func rowTimestampFields(optional *string, required ...string) []string {
	fields := required
	if optional != nil {
		fields = append(fields, *optional)
	}
	return fields
}

func rowError(category string, index int, err error) error {
	return fmt.Errorf("%w: %s[%d]: %s", ErrInvalidRow, category, index, err)
}

func flattenInventory(inv *Inventory) []string {
	if inv == nil {
		return nil
	}
	out := make([]string, 0, len(inv.Repositories)+len(inv.Tasks)+len(inv.TaskRepositories)+
		len(inv.Sessions)+len(inv.Turns)+len(inv.Messages)+len(inv.Commits)+len(inv.GitSnapshots))
	out = append(out, inv.Repositories...)
	out = append(out, inv.Tasks...)
	out = append(out, inv.TaskRepositories...)
	out = append(out, inv.Sessions...)
	out = append(out, inv.Turns...)
	out = append(out, inv.Messages...)
	out = append(out, inv.Commits...)
	out = append(out, inv.GitSnapshots...)
	return out
}
