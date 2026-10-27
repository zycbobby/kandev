// Package instances owns the per-owner registry of remote Kandev instances:
// the Instance record, the owner-scoped service in front of it, and the
// persistence contract the SQLite store implements.
package instances

import "time"

// Instance is one registered remote Kandev instance owned by a single local
// user. BaseURL is the remote backend origin, CredentialSecretID names the
// stored secret used to authenticate to it, and ProxyPort is the local port
// the per-instance proxy bound (0 while unbound).
type Instance struct {
	ID                 string
	OwnerUserID        string
	Name               string
	BaseURL            string
	CredentialSecretID string
	Enabled            bool
	ProxyPort          int
	CreatedAt          time.Time
	UpdatedAt          time.Time
}
