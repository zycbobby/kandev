package models

import (
	"errors"
	"fmt"

	"github.com/kandev/kandev/internal/common/securityutil"
)

const (
	// CreationBaseMetadataKey stores the comparison anchor recorded when a
	// worktree branch was first cut for a task repository.
	CreationBaseMetadataKey = "creation_base"
	// CreationBaseVersion is the persisted schema version of a creation base.
	CreationBaseVersion = 1
)

// CreationBase records the local ref a worktree branch was cut from and the
// commit that ref pointed at, so comparison surfaces can default to the
// worktree's own work. Ref keeps the ref exactly as resolved at
// materialization time: a local base is its plain name, a remote-only base is
// "origin/<name>".
type CreationBase struct {
	Version int    `json:"version"`
	Ref     string `json:"ref"`
	ForkSHA string `json:"fork_sha"`
}

// Equal reports whether two creation bases record the same anchor.
func (c CreationBase) Equal(other CreationBase) bool {
	return c == other
}

// Validate rejects malformed data rehydrated from task metadata. Ref uses the
// default-branch allowlist because it is later resolved through a local
// rev-parse (merge-base) with no upstream disambiguation, so git's symbolic
// pseudo-refs must be rejected; an "origin/" prefix stays valid because
// remote-only base refs are recorded verbatim. ForkSHA must be a full object
// ID (40 hex chars for SHA-1, 64 for SHA-256).
func (c CreationBase) Validate() error {
	if c.Version != CreationBaseVersion {
		return fmt.Errorf("creation base version %d is not supported", c.Version)
	}
	if !securityutil.IsValidDefaultBranchName(c.Ref) {
		return errors.New("creation base ref is invalid")
	}
	if !isValidFullCommitSHA(c.ForkSHA) {
		return errors.New("creation base fork_sha is invalid")
	}
	return nil
}

// isValidFullCommitSHA accepts only full commit object IDs, unlike
// LooksLikeCommitSHA, which also admits abbreviated SHAs.
func isValidFullCommitSHA(sha string) bool {
	if len(sha) != 40 && len(sha) != 64 {
		return false
	}
	return securityutil.LooksLikeCommitSHA(sha)
}

// PutCreationBase stores a validated creation base while preserving unrelated
// task-repository metadata.
func PutCreationBase(metadata map[string]interface{}, base *CreationBase) error {
	if base == nil {
		return errors.New("creation base is required")
	}
	if err := base.Validate(); err != nil {
		return err
	}
	if metadata == nil {
		return errors.New("metadata map is required")
	}
	metadata[CreationBaseMetadataKey] = *base
	return nil
}

// LoadCreationBase decodes and validates a creation base from typed or
// JSON-rehydrated metadata. A missing key is not an error.
func LoadCreationBase(metadata map[string]interface{}) (CreationBase, bool, error) {
	return loadValidatedMetadata(
		metadata,
		CreationBaseMetadataKey,
		"creation base",
		"creation base is nil",
		CreationBase.Validate,
	)
}

// RemoveCreationBase removes a creation base when present and reports whether
// it existed. Removal is idempotent.
func RemoveCreationBase(metadata map[string]interface{}) bool {
	if metadata == nil {
		return false
	}
	_, present := metadata[CreationBaseMetadataKey]
	if present {
		delete(metadata, CreationBaseMetadataKey)
	}
	return present
}
