package statsync

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/db/dialect"
)

// Repository owns the statsync mirror schema: dedicated tables for rows
// pushed by other instances plus the v_stats_* compatibility views the
// analytics queries read. Writes never touch core task tables and publish
// no task events.
type Repository struct {
	db *sqlx.DB
	ro *sqlx.DB
}

// NewWithDB initializes the mirror tables on the shared writer and returns
// the repository. Safe to replay: every statement is idempotent. The
// v_stats_* views are NOT created here — they union core tables that other
// stores' migrations may still rebuild — boot calls EnsureViews after every
// store schema has settled.
func NewWithDB(writer, reader *sqlx.DB) (*Repository, error) {
	r := &Repository{db: writer, ro: reader}
	if err := r.initSchema(); err != nil {
		return nil, fmt.Errorf("init statsync schema: %w", err)
	}
	return r, nil
}

// EnsureViews creates the v_stats_* compatibility views. Boot calls this
// once every store's schema (including table-rebuild migrations) has been
// applied: a view created earlier would reference a table a later rebuild
// drops, breaking that rebuild. Idempotent on both engines.
func EnsureViews(writer *sqlx.DB) error {
	driver := writer.DriverName()
	for _, view := range mirrorViews {
		if _, err := writer.Exec(viewCreateStatement(driver, view)); err != nil {
			return fmt.Errorf("create %s: %w", view.name, err)
		}
	}
	return nil
}

// mirrorSchemaDDL creates the mirror tables. {{bool}} and {{timestamp}}
// expand per driver (dialect.RenderSchema) so the same text replays on
// SQLite and PostgreSQL. Foreign keys stay inside the mirror set: a
// reconcile deleting tasks or sessions cascades to their children without
// ever touching core rows.
const mirrorSchemaDDL = `
CREATE TABLE IF NOT EXISTS statsync_repositories (
	uid TEXT PRIMARY KEY,
	source_instance TEXT NOT NULL,
	name TEXT NOT NULL,
	remote_url TEXT DEFAULT '',
	workspace_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS statsync_tasks (
	uid TEXT PRIMARY KEY,
	source_instance TEXT NOT NULL,
	orig_workspace_id TEXT NOT NULL DEFAULT '',
	workspace_id TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL DEFAULT '',
	archived_at {{timestamp}},
	completed {{bool}},
	created_at {{timestamp}} NOT NULL,
	updated_at {{timestamp}} NOT NULL
);
CREATE TABLE IF NOT EXISTS statsync_task_repositories (
	uid TEXT PRIMARY KEY,
	source_instance TEXT NOT NULL,
	task_uid TEXT NOT NULL,
	repository_id TEXT NOT NULL,
	created_at {{timestamp}},
	FOREIGN KEY (task_uid) REFERENCES statsync_tasks(uid) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS statsync_sessions (
	uid TEXT PRIMARY KEY,
	source_instance TEXT NOT NULL,
	task_uid TEXT NOT NULL,
	started_at {{timestamp}} NOT NULL,
	completed_at {{timestamp}},
	repository_id TEXT DEFAULT '',
	agent_profile_snapshot TEXT DEFAULT '{}',
	FOREIGN KEY (task_uid) REFERENCES statsync_tasks(uid) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS statsync_turns (
	uid TEXT PRIMARY KEY,
	source_instance TEXT NOT NULL,
	session_uid TEXT NOT NULL,
	started_at {{timestamp}} NOT NULL,
	completed_at {{timestamp}},
	FOREIGN KEY (session_uid) REFERENCES statsync_sessions(uid) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS statsync_messages (
	uid TEXT PRIMARY KEY,
	source_instance TEXT NOT NULL,
	session_uid TEXT NOT NULL,
	turn_id TEXT DEFAULT '',
	author_type TEXT NOT NULL,
	type TEXT NOT NULL,
	created_at {{timestamp}} NOT NULL,
	FOREIGN KEY (session_uid) REFERENCES statsync_sessions(uid) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS statsync_commits (
	uid TEXT PRIMARY KEY,
	source_instance TEXT NOT NULL,
	session_uid TEXT NOT NULL,
	commit_sha TEXT DEFAULT '',
	committed_at {{timestamp}} NOT NULL,
	files_changed INTEGER DEFAULT 0,
	insertions INTEGER DEFAULT 0,
	deletions INTEGER DEFAULT 0,
	FOREIGN KEY (session_uid) REFERENCES statsync_sessions(uid) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS statsync_git_snapshots (
	uid TEXT PRIMARY KEY,
	source_instance TEXT NOT NULL,
	session_uid TEXT NOT NULL,
	snapshot_type TEXT NOT NULL DEFAULT '',
	files TEXT DEFAULT '{}',
	created_at {{timestamp}} NOT NULL,
	FOREIGN KEY (session_uid) REFERENCES statsync_sessions(uid) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_statsync_tasks_workspace ON statsync_tasks(workspace_id);
CREATE INDEX IF NOT EXISTS idx_statsync_sessions_task ON statsync_sessions(task_uid);
CREATE INDEX IF NOT EXISTS idx_statsync_turns_session ON statsync_turns(session_uid);
CREATE INDEX IF NOT EXISTS idx_statsync_messages_session ON statsync_messages(session_uid);
CREATE INDEX IF NOT EXISTS idx_statsync_commits_session ON statsync_commits(session_uid, committed_at);
CREATE INDEX IF NOT EXISTS idx_statsync_git_snapshots_session ON statsync_git_snapshots(session_uid, created_at);
`

// viewDefinition is one v_stats_* compatibility view: core rows UNION ALL
// mirror rows, with the mirror branch emitting the constants the analytics
// inclusion rules expect (is_ephemeral=0, origin='manual').
type viewDefinition struct {
	name string
	body string
}

// mirrorViews lists the views the analytics repository swaps in for its core
// table names. v_stats_repositories additionally unions remote-only mirror
// repositories (matched ones surface through their local repository id), so
// the repositories/git sections see both populations through one name.
// Column sets are the exact columns the analytics queries reference — keep
// them in sync with internal/analytics/repository/sqlite/stats.go.
var mirrorViews = []viewDefinition{
	{
		name: "v_stats_tasks",
		body: `
SELECT t.id, t.workspace_id, t.workflow_id, t.workflow_step_id, t.title, t.state,
	t.is_ephemeral, t.origin, t.archived_at, t.created_at, t.updated_at,
	(t.archived_at IS NOT NULL
	 OR ws.position = (SELECT MAX(ws2.position) FROM workflow_steps ws2 WHERE ws2.workflow_id = ws.workflow_id)
	) AS completed
FROM tasks t
LEFT JOIN workflow_steps ws ON ws.id = t.workflow_step_id
UNION ALL
SELECT st.uid, st.workspace_id, '', '', st.title, st.state,
	0, 'manual', st.archived_at, st.created_at, st.updated_at,
	COALESCE(st.completed, st.archived_at IS NOT NULL)
FROM statsync_tasks st`,
	},
	{
		name: "v_stats_repositories",
		body: `
SELECT r.id, r.name, r.workspace_id, r.deleted_at
FROM repositories r
UNION ALL
SELECT sr.uid, sr.name, sr.workspace_id, NULL
FROM statsync_repositories sr`,
	},
	{
		name: "v_stats_task_repositories",
		body: `
SELECT tr.id, tr.task_id, tr.repository_id
FROM task_repositories tr
UNION ALL
SELECT str.uid, str.task_uid, str.repository_id
FROM statsync_task_repositories str`,
	},
	{
		name: "v_stats_sessions",
		body: `
SELECT s.id, s.task_id, s.started_at, s.completed_at, s.repository_id, s.agent_profile_snapshot
FROM task_sessions s
UNION ALL
SELECT ss.uid, ss.task_uid, ss.started_at, ss.completed_at, ss.repository_id, ss.agent_profile_snapshot
FROM statsync_sessions ss`,
	},
	{
		name: "v_stats_turns",
		body: `
SELECT turn.id, turn.task_session_id, turn.started_at, turn.completed_at
FROM task_session_turns turn
UNION ALL
SELECT st.uid, st.session_uid, st.started_at, st.completed_at
FROM statsync_turns st`,
	},
	{
		name: "v_stats_messages",
		body: `
SELECT m.id, m.task_session_id, m.turn_id, m.author_type, m.type, m.created_at
FROM task_session_messages m
UNION ALL
SELECT sm.uid, sm.session_uid, sm.turn_id, sm.author_type, sm.type, sm.created_at
FROM statsync_messages sm`,
	},
	{
		name: "v_stats_commits",
		body: `
SELECT c.id, c.session_id, c.commit_sha, c.committed_at, c.files_changed, c.insertions, c.deletions
FROM task_session_commits c
UNION ALL
SELECT sc.uid, sc.session_uid, sc.commit_sha, sc.committed_at, sc.files_changed, sc.insertions, sc.deletions
FROM statsync_commits sc`,
	},
	{
		name: "v_stats_git_snapshots",
		body: `
SELECT g.id, g.session_id, g.snapshot_type, g.files, g.created_at
FROM task_session_git_snapshots g
UNION ALL
SELECT sg.uid, sg.session_uid, sg.snapshot_type, sg.files, sg.created_at
FROM statsync_git_snapshots sg`,
	},
}

func (r *Repository) initSchema() error {
	rendered, err := dialect.RenderSchema(r.db.DriverName(), mirrorSchemaDDL)
	if err != nil {
		return err
	}
	statements := strings.Split(rendered, ";")
	for _, statement := range statements {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := r.db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

// viewCreateStatement branches on engine: SQLite understands IF NOT EXISTS,
// PostgreSQL only understands OR REPLACE — both replay idempotently for an
// unchanged definition.
func viewCreateStatement(driver string, view viewDefinition) string {
	if dialect.IsPostgres(driver) {
		return "CREATE OR REPLACE VIEW " + view.name + " AS " + view.body
	}
	return "CREATE VIEW IF NOT EXISTS " + view.name + " AS " + view.body
}

// IngestResult reports what one ingest payload changed.
type IngestResult struct {
	Upserts int64
	Deleted int64
}

// ApplyDelta upserts every row of the payload in one transaction. Re-pushing
// rows already stored updates them in place, so overlapping watermarks are
// harmless.
func (r *Repository) ApplyDelta(ctx context.Context, env *Envelope) (*IngestResult, error) {
	return r.applyEnvelope(ctx, env, false)
}

// ApplyReconcile applies the payload and then deletes the source's mirror
// rows that the inventory does not list, so remote deletions converge.
func (r *Repository) ApplyReconcile(ctx context.Context, env *Envelope) (*IngestResult, error) {
	return r.applyEnvelope(ctx, env, true)
}

func (r *Repository) applyEnvelope(ctx context.Context, env *Envelope, reconcile bool) (*IngestResult, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin statsync ingest: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result := &IngestResult{}
	upserters := []func(*sqlx.Tx, *Envelope) (int64, error){
		upsertRepositories,
		upsertTasks,
		upsertTaskRepositories,
		upsertSessions,
		upsertTurns,
		upsertMessages,
		upsertCommits,
		upsertGitSnapshots,
	}
	for _, upsert := range upserters {
		count, err := upsert(tx, env)
		if err != nil {
			return nil, err
		}
		result.Upserts += count
	}
	if reconcile {
		deleted, err := deleteOutsideInventory(tx, env.SourceInstance, env.Inventory)
		if err != nil {
			return nil, err
		}
		result.Deleted = deleted
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit statsync ingest: %w", err)
	}
	return result, nil
}

// inventoryChunkSize bounds each NOT IN clause well under both engines'
// bound-parameter ceilings; categories larger than one chunk chain several
// clauses.
const inventoryChunkSize = 500

// deleteOutsideInventory removes, per category, the source's mirror rows the
// inventory does not list. An empty category inventory deletes the whole
// category for that source. Cascades inside the mirror set remove children
// of deleted parents; the per-category passes still run so rows whose
// parent survived are reconciled too.
func deleteOutsideInventory(tx *sqlx.Tx, source string, inv *Inventory) (int64, error) {
	categories := []struct {
		table string
		uids  []string
	}{
		{"statsync_tasks", inv.Tasks},
		{"statsync_task_repositories", inv.TaskRepositories},
		{"statsync_sessions", inv.Sessions},
		{"statsync_turns", inv.Turns},
		{"statsync_messages", inv.Messages},
		{"statsync_commits", inv.Commits},
		{"statsync_git_snapshots", inv.GitSnapshots},
		{"statsync_repositories", inv.Repositories},
	}
	var deleted int64
	for _, category := range categories {
		query := "DELETE FROM " + category.table + " WHERE source_instance = ?"
		args := []any{source}
		for start := 0; start < len(category.uids); start += inventoryChunkSize {
			end := start + inventoryChunkSize
			if end > len(category.uids) {
				end = len(category.uids)
			}
			query += " AND uid NOT IN (" + strings.TrimRight(strings.Repeat("?,", end-start), ",") + ")"
			for _, uid := range category.uids[start:end] {
				args = append(args, uid)
			}
		}
		result, err := tx.Exec(tx.Rebind(query), args...)
		if err != nil {
			return 0, fmt.Errorf("reconcile %s: %w", category.table, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("reconcile %s rows affected: %w", category.table, err)
		}
		deleted += affected
	}
	return deleted, nil
}

func upsertRepositories(tx *sqlx.Tx, env *Envelope) (int64, error) {
	const query = `
INSERT INTO statsync_repositories (uid, source_instance, name, remote_url, workspace_id)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(uid) DO UPDATE SET
	source_instance = excluded.source_instance,
	name = excluded.name,
	remote_url = excluded.remote_url,
	workspace_id = excluded.workspace_id`
	return upsertEach(tx, env.Repositories, func(row RepositoryRow) ([]any, error) {
		// Name-matched repositories merge through their local id and must not
		// also appear as mirror rows — v_stats_repositories would list the
		// same remote repository twice.
		if row.MatchedLocalID != "" {
			return nil, nil
		}
		return []any{row.UID, env.SourceInstance, row.Name, row.RemoteURL, row.ResolvedWorkspaceID}, nil
	}, query)
}

func upsertTasks(tx *sqlx.Tx, env *Envelope) (int64, error) {
	const query = `
INSERT INTO statsync_tasks
	(uid, source_instance, orig_workspace_id, workspace_id, title, state, archived_at, completed, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(uid) DO UPDATE SET
	source_instance = excluded.source_instance,
	orig_workspace_id = excluded.orig_workspace_id,
	workspace_id = excluded.workspace_id,
	title = excluded.title,
	state = excluded.state,
	archived_at = excluded.archived_at,
	completed = excluded.completed,
	created_at = excluded.created_at,
	updated_at = excluded.updated_at`
	return upsertEach(tx, env.Tasks, func(row TaskRow) ([]any, error) {
		archivedAt, err := optionalTimestamp(row.ArchivedAt)
		if err != nil {
			return nil, err
		}
		createdAt, err := ParseTimestamp(row.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("created_at: %w", err)
		}
		updatedAt, err := ParseTimestamp(row.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("updated_at: %w", err)
		}
		return []any{row.UID, env.SourceInstance, row.OrigWorkspaceID, row.WorkspaceID(),
			row.Title, row.State, archivedAt, row.Completed, createdAt, updatedAt}, nil
	}, query)
}

func upsertTaskRepositories(tx *sqlx.Tx, env *Envelope) (int64, error) {
	const query = `
INSERT INTO statsync_task_repositories (uid, source_instance, task_uid, repository_id, created_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(uid) DO UPDATE SET
	source_instance = excluded.source_instance,
	task_uid = excluded.task_uid,
	repository_id = excluded.repository_id,
	created_at = excluded.created_at`
	return upsertEach(tx, env.TaskRepositories, func(row TaskRepositoryRow) ([]any, error) {
		createdAt, err := optionalTimestamp(nilString(row.CreatedAt))
		if err != nil {
			return nil, err
		}
		return []any{row.UID, env.SourceInstance, row.TaskUID, row.RepositoryID, createdAt}, nil
	}, query)
}

func upsertSessions(tx *sqlx.Tx, env *Envelope) (int64, error) {
	const query = `
INSERT INTO statsync_sessions
	(uid, source_instance, task_uid, started_at, completed_at, repository_id, agent_profile_snapshot)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(uid) DO UPDATE SET
	source_instance = excluded.source_instance,
	task_uid = excluded.task_uid,
	started_at = excluded.started_at,
	completed_at = excluded.completed_at,
	repository_id = excluded.repository_id,
	agent_profile_snapshot = excluded.agent_profile_snapshot`
	return upsertEach(tx, env.Sessions, func(row SessionRow) ([]any, error) {
		startedAt, err := ParseTimestamp(row.StartedAt)
		if err != nil {
			return nil, fmt.Errorf("started_at: %w", err)
		}
		completedAt, err := optionalTimestamp(row.CompletedAt)
		if err != nil {
			return nil, err
		}
		snapshot := row.AgentProfileSnapshot
		if len(snapshot) == 0 {
			snapshot = []byte("{}")
		}
		return []any{row.UID, env.SourceInstance, row.TaskUID, startedAt, completedAt,
			row.RepositoryID, snapshot}, nil
	}, query)
}

func upsertTurns(tx *sqlx.Tx, env *Envelope) (int64, error) {
	const query = `
INSERT INTO statsync_turns (uid, source_instance, session_uid, started_at, completed_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(uid) DO UPDATE SET
	source_instance = excluded.source_instance,
	session_uid = excluded.session_uid,
	started_at = excluded.started_at,
	completed_at = excluded.completed_at`
	return upsertEach(tx, env.Turns, func(row TurnRow) ([]any, error) {
		startedAt, err := ParseTimestamp(row.StartedAt)
		if err != nil {
			return nil, fmt.Errorf("started_at: %w", err)
		}
		completedAt, err := optionalTimestamp(row.CompletedAt)
		if err != nil {
			return nil, err
		}
		return []any{row.UID, env.SourceInstance, row.SessionUID, startedAt, completedAt}, nil
	}, query)
}

func upsertMessages(tx *sqlx.Tx, env *Envelope) (int64, error) {
	const query = `
INSERT INTO statsync_messages (uid, source_instance, session_uid, turn_id, author_type, type, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(uid) DO UPDATE SET
	source_instance = excluded.source_instance,
	session_uid = excluded.session_uid,
	turn_id = excluded.turn_id,
	author_type = excluded.author_type,
	type = excluded.type,
	created_at = excluded.created_at`
	return upsertEach(tx, env.Messages, func(row MessageRow) ([]any, error) {
		createdAt, err := ParseTimestamp(row.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("created_at: %w", err)
		}
		return []any{row.UID, env.SourceInstance, row.SessionUID, row.TurnUID,
			row.AuthorType, row.Type, createdAt}, nil
	}, query)
}

func upsertCommits(tx *sqlx.Tx, env *Envelope) (int64, error) {
	const query = `
INSERT INTO statsync_commits
	(uid, source_instance, session_uid, commit_sha, committed_at, files_changed, insertions, deletions)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(uid) DO UPDATE SET
	source_instance = excluded.source_instance,
	session_uid = excluded.session_uid,
	commit_sha = excluded.commit_sha,
	committed_at = excluded.committed_at,
	files_changed = excluded.files_changed,
	insertions = excluded.insertions,
	deletions = excluded.deletions`
	return upsertEach(tx, env.Commits, func(row CommitRow) ([]any, error) {
		committedAt, err := ParseTimestamp(row.CommittedAt)
		if err != nil {
			return nil, fmt.Errorf("committed_at: %w", err)
		}
		return []any{row.UID, env.SourceInstance, row.SessionUID, row.CommitSHA, committedAt,
			row.FilesChanged, row.Insertions, row.Deletions}, nil
	}, query)
}

func upsertGitSnapshots(tx *sqlx.Tx, env *Envelope) (int64, error) {
	const query = `
INSERT INTO statsync_git_snapshots (uid, source_instance, session_uid, snapshot_type, files, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(uid) DO UPDATE SET
	source_instance = excluded.source_instance,
	session_uid = excluded.session_uid,
	snapshot_type = excluded.snapshot_type,
	files = excluded.files,
	created_at = excluded.created_at`
	return upsertEach(tx, env.GitSnapshots, func(row GitSnapshotRow) ([]any, error) {
		createdAt, err := ParseTimestamp(row.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("created_at: %w", err)
		}
		files := row.Files
		if len(files) == 0 {
			files = []byte("{}")
		}
		return []any{row.UID, env.SourceInstance, row.SessionUID, row.SnapshotType, files, createdAt}, nil
	}, query)
}

// upsertEach runs one parameterized upsert per row inside the caller's
// transaction and returns the number of statements executed. An args
// callback returning nil values skips the row (e.g. a repository that
// matched a local one and must not be mirrored).
func upsertEach[T any](tx *sqlx.Tx, rows []T, args func(T) ([]any, error), query string) (int64, error) {
	var count int64
	for _, row := range rows {
		values, err := args(row)
		if err != nil {
			return count, err
		}
		if values == nil {
			continue
		}
		if _, err := tx.Exec(tx.Rebind(query), values...); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func optionalTimestamp(value *string) (*time.Time, error) {
	if value == nil || *value == "" {
		return nil, nil
	}
	parsed, err := ParseTimestamp(*value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func nilString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
