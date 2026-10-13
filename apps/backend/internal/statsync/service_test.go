package statsync

import (
	"context"
	"errors"
	"testing"

	"github.com/jmoiron/sqlx"
)

type serviceFixture struct {
	db   *sqlx.DB
	repo *Repository
	svc  *Service
}

func newServiceFixture(t *testing.T, mappings map[string]string, fallback DefaultWorkspaceFunc) *serviceFixture {
	t.Helper()
	db := testDB(t)
	// The v_stats_* views reference the core tables, so the shared minimal
	// core fixture comes first; the service cases then add the workspace
	// rows they resolve against.
	seedCoreAnalyticsFixture(t, db)
	statements := []string{
		`CREATE TABLE IF NOT EXISTS workspaces (id TEXT PRIMARY KEY)`,
		`INSERT INTO workspaces (id) VALUES ('ws-mapped'), ('ws-primary')`,
		`INSERT INTO repositories (id, workspace_id, name) VALUES ('mapped-repo-1', 'ws-mapped', 'kandev')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(db.Rebind(statement)); err != nil {
			t.Fatalf("seed service fixture: %v", err)
		}
	}
	repo := newTestRepository(t, db)
	return &serviceFixture{
		db:   db,
		repo: repo,
		svc:  NewService(repo, db, "test-secret", mappings, fallback),
	}
}

func TestAuthorizeAcceptsMatchingSecret(t *testing.T) {
	fx := newServiceFixture(t, map[string]string{}, nil)
	if err := fx.svc.Authorize("test-secret"); err != nil {
		t.Fatalf("Authorize() = %v, want nil", err)
	}
}

func TestAuthorizeRejectsWrongAndMissingSecret(t *testing.T) {
	fx := newServiceFixture(t, map[string]string{}, nil)
	for _, secret := range []string{"", "wrong", "test-secret "} {
		if err := fx.svc.Authorize(secret); !errors.Is(err, ErrInvalidSecret) {
			t.Fatalf("Authorize(%q) = %v, want ErrInvalidSecret", secret, err)
		}
	}
}

func TestIngestRejectsUnmappedSource(t *testing.T) {
	fx := newServiceFixture(t, map[string]string{}, nil)
	env := validEnvelope()
	if _, err := fx.svc.Ingest(context.Background(), env); !errors.Is(err, ErrUnmappedSource) {
		t.Fatalf("Ingest() = %v, want ErrUnmappedSource", err)
	}
}

func TestIngestUsesExplicitMapping(t *testing.T) {
	fx := newServiceFixture(t, map[string]string{"remote-1": "ws-mapped"}, nil)
	result, err := fx.svc.Ingest(context.Background(), validEnvelope())
	if err != nil {
		t.Fatalf("Ingest() = %v, want nil", err)
	}
	if result.Upserts == 0 {
		t.Fatal("Ingest() upserted nothing")
	}
	var workspaceID string
	if err := fx.db.Get(&workspaceID, `SELECT workspace_id FROM statsync_tasks WHERE uid = 'remote-1:task-1'`); err != nil {
		t.Fatalf("read mirrored task: %v", err)
	}
	if workspaceID != "ws-mapped" {
		t.Fatalf("workspace_id = %q, want ws-mapped", workspaceID)
	}
}

func TestIngestFallsBackToPrimaryWorkspace(t *testing.T) {
	fallback := func(context.Context) (string, error) { return "ws-primary", nil }
	fx := newServiceFixture(t, map[string]string{}, fallback)
	if _, err := fx.svc.Ingest(context.Background(), validEnvelope()); err != nil {
		t.Fatalf("Ingest() = %v, want nil", err)
	}
	var workspaceID string
	if err := fx.db.Get(&workspaceID, `SELECT workspace_id FROM statsync_tasks WHERE uid = 'remote-1:task-1'`); err != nil {
		t.Fatalf("read mirrored task: %v", err)
	}
	if workspaceID != "ws-primary" {
		t.Fatalf("workspace_id = %q, want ws-primary (default fallback)", workspaceID)
	}
}

func TestIngestRejectsMappingToMissingWorkspace(t *testing.T) {
	fx := newServiceFixture(t, map[string]string{"remote-1": "ws-gone"}, nil)
	env := validEnvelope()
	if _, err := fx.svc.Ingest(context.Background(), env); !errors.Is(err, ErrUnmappedSource) {
		t.Fatalf("Ingest() = %v, want ErrUnmappedSource", err)
	}
}

func TestIngestMatchesRepositoryByNameAndRewritesReferences(t *testing.T) {
	fx := newServiceFixture(t, map[string]string{"remote-1": "ws-mapped"}, nil)
	env := validEnvelope()
	// The payload names its repository "kandev", which exists in ws-mapped
	// as local-repo-1; sessions and links must point at the local id.
	env.Repositories[0].Name = "kandev"
	if _, err := fx.svc.Ingest(context.Background(), env); err != nil {
		t.Fatalf("Ingest() = %v, want nil", err)
	}
	var repositoryID string
	if err := fx.db.Get(&repositoryID, `SELECT repository_id FROM statsync_sessions WHERE uid = 'remote-1:sess-1'`); err != nil {
		t.Fatalf("read mirrored session: %v", err)
	}
	if repositoryID != "mapped-repo-1" {
		t.Fatalf("session repository_id = %q, want mapped-repo-1 (name match)", repositoryID)
	}
	var linkRepositoryID string
	if err := fx.db.Get(&linkRepositoryID, `SELECT repository_id FROM statsync_task_repositories WHERE uid = 'remote-1:tr-1'`); err != nil {
		t.Fatalf("read mirrored link: %v", err)
	}
	if linkRepositoryID != "mapped-repo-1" {
		t.Fatalf("link repository_id = %q, want mapped-repo-1", linkRepositoryID)
	}
	// A name-matched repository must not also land in the mirror set.
	var mirrorRepos int
	if err := fx.db.Get(&mirrorRepos, `SELECT COUNT(*) FROM statsync_repositories`); err != nil {
		t.Fatal(err)
	}
	if mirrorRepos != 0 {
		t.Fatalf("statsync_repositories has %d rows, want 0 (name matched into local repositories)", mirrorRepos)
	}
}

func TestIngestKeepsUnmatchedRepositoryAsMirror(t *testing.T) {
	fx := newServiceFixture(t, map[string]string{"remote-1": "ws-mapped"}, nil)
	env := validEnvelope()
	env.Repositories[0].Name = "remote-only-repo"
	if _, err := fx.svc.Ingest(context.Background(), env); err != nil {
		t.Fatalf("Ingest() = %v, want nil", err)
	}
	var workspaceID, repositoryID string
	if err := fx.db.QueryRowx(`SELECT workspace_id, uid FROM statsync_repositories`).
		Scan(&workspaceID, &repositoryID); err != nil {
		t.Fatalf("read mirror repository: %v", err)
	}
	if workspaceID != "ws-mapped" || repositoryID != "remote-1:repo-1" {
		t.Fatalf("mirror repository = (%s, %s), want (ws-mapped, remote-1:repo-1)", workspaceID, repositoryID)
	}
	var sessionRepositoryID string
	if err := fx.db.Get(&sessionRepositoryID, `SELECT repository_id FROM statsync_sessions WHERE uid = 'remote-1:sess-1'`); err != nil {
		t.Fatal(err)
	}
	if sessionRepositoryID != "remote-1:repo-1" {
		t.Fatalf("session repository_id = %q, want the mirror uid", sessionRepositoryID)
	}
}

func TestIngestRejectsInvalidPayloadBeforeTouchingDB(t *testing.T) {
	fx := newServiceFixture(t, map[string]string{"remote-1": "ws-mapped"}, nil)
	env := validEnvelope()
	env.SchemaVersion = 99
	if _, err := fx.svc.Ingest(context.Background(), env); !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("Ingest() = %v, want ErrUnsupportedSchemaVersion", err)
	}
}
