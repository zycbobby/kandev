package statsync

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
)

// Service validates and resolves ingest payloads, then hands them to the
// mirror repository. It owns the shared-secret check, the source-to-workspace
// mapping, and the remote-repository-to-local-repository name matching.
type Service struct {
	repo             *Repository
	ro               *sqlx.DB
	secret           string
	mappings         map[string]string
	defaultWorkspace DefaultWorkspaceFunc
}

// DefaultWorkspaceFunc resolves the fallback workspace for sources without an
// explicit mapping. v1 maps them to the instance's primary workspace so the
// merged statistics stay complete without per-source configuration.
type DefaultWorkspaceFunc func(ctx context.Context) (string, error)

// Ingest resolution errors. The handler maps them to HTTP statuses: invalid
// payloads to 400, unauthorized secrets to 401, unmapped sources to 403.
var (
	ErrUnmappedSource = errors.New("source instance has no workspace mapping")
	ErrInvalidSecret  = errors.New("invalid statsync secret")
)

// NewService builds the ingest service. secret empty disables the feature —
// the wiring registers no route in that case.
func NewService(repo *Repository, reader *sqlx.DB, secret string, mappings map[string]string, defaultWorkspace DefaultWorkspaceFunc) *Service {
	return &Service{
		repo:             repo,
		ro:               reader,
		secret:           secret,
		mappings:         mappings,
		defaultWorkspace: defaultWorkspace,
	}
}

// Authorize checks the shared secret in constant time so a wrong guess
// cannot be recovered byte by byte from timing differences.
func (s *Service) Authorize(secret string) error {
	if s.secret == "" || subtle.ConstantTimeCompare([]byte(secret), []byte(s.secret)) != 1 {
		return ErrInvalidSecret
	}
	return nil
}

// Ingest validates the envelope, resolves identities into local ids, and
// applies the payload to the mirror tables in one transaction.
func (s *Service) Ingest(ctx context.Context, env *Envelope) (*IngestResult, error) {
	if err := ValidateEnvelope(env); err != nil {
		return nil, err
	}
	workspaceID, err := s.resolveWorkspace(ctx, env.SourceInstance)
	if err != nil {
		return nil, err
	}
	for i := range env.Tasks {
		env.Tasks[i].ResolvedWorkspaceID = workspaceID
	}
	if err := s.resolveRepositories(ctx, env, workspaceID); err != nil {
		return nil, err
	}
	switch env.Mode {
	case ModeDelta:
		return s.repo.ApplyDelta(ctx, env)
	case ModeReconcile:
		return s.repo.ApplyReconcile(ctx, env)
	default:
		return nil, ErrInvalidMode
	}
}

// resolveWorkspace maps the source instance to a local workspace id:
// explicitly configured mappings win, otherwise the instance's primary
// workspace. A mapping that does not name a real workspace is treated as
// unmapped rather than silently dropping the pushed rows into a void.
func (s *Service) resolveWorkspace(ctx context.Context, source string) (string, error) {
	candidates := []string{source}
	if lowered := strings.ToLower(source); lowered != source {
		candidates = append(candidates, lowered)
	}
	for _, candidate := range candidates {
		if id := s.mappings[candidate]; id != "" {
			if err := s.requireWorkspace(ctx, id); err != nil {
				return "", err
			}
			return id, nil
		}
	}
	if s.defaultWorkspace == nil {
		return "", fmt.Errorf("%w: %q", ErrUnmappedSource, source)
	}
	id, err := s.defaultWorkspace(ctx)
	if err != nil || id == "" {
		return "", fmt.Errorf("%w: %q (no default workspace available)", ErrUnmappedSource, source)
	}
	return id, nil
}

func (s *Service) requireWorkspace(ctx context.Context, workspaceID string) error {
	var exists bool
	err := s.ro.GetContext(ctx, &exists, s.ro.Rebind(
		`SELECT EXISTS (SELECT 1 FROM workspaces WHERE id = ?)`,
	), workspaceID)
	if err != nil {
		return fmt.Errorf("check mapped workspace %s: %w", workspaceID, err)
	}
	if !exists {
		return fmt.Errorf("%w: mapped workspace %q does not exist", ErrUnmappedSource, workspaceID)
	}
	return nil
}

// resolveRepositories rewrites remote repository references into local ids.
// A remote repository whose name matches a repository in the mapped
// workspace maps to that local id, so its sessions and commits merge into
// the local repository's statistics; anything else keeps its mirror uid and
// stays a statsync_repositories row scoped to the mapped workspace.
func (s *Service) resolveRepositories(ctx context.Context, env *Envelope, workspaceID string) error {
	resolved := make(map[string]string, len(env.Repositories))
	for i := range env.Repositories {
		row := &env.Repositories[i]
		row.ResolvedWorkspaceID = workspaceID
		localID, err := s.findRepositoryID(ctx, workspaceID, row.Name)
		if err != nil {
			return err
		}
		if localID != "" {
			row.MatchedLocalID = localID
			resolved[row.UID] = localID
		} else {
			resolved[row.UID] = row.UID
		}
	}
	for i := range env.Sessions {
		if id, ok := resolved[env.Sessions[i].RepositoryID]; ok {
			env.Sessions[i].RepositoryID = id
		}
	}
	for i := range env.TaskRepositories {
		if id, ok := resolved[env.TaskRepositories[i].RepositoryID]; ok {
			env.TaskRepositories[i].RepositoryID = id
		}
	}
	return nil
}

func (s *Service) findRepositoryID(ctx context.Context, workspaceID, name string) (string, error) {
	if name == "" {
		return "", nil
	}
	var id string
	err := s.ro.GetContext(ctx, &id, s.ro.Rebind(`
SELECT id FROM repositories
WHERE workspace_id = ? AND name = ? AND deleted_at IS NULL
ORDER BY created_at, id
LIMIT 1
`), workspaceID, name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("match repository %q in workspace %s: %w", name, workspaceID, err)
	}
	return id, nil
}
