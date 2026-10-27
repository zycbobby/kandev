// Package sqlite persists the per-owner remote-instance registry and the
// deduplicated remote notification occurrences in the shared SQL database.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/instances"
	"github.com/kandev/kandev/internal/instances/relay"
)

// Store is the SQLite implementation of instances.Repository.
type Store struct {
	db *sqlx.DB // writer
	ro *sqlx.DB // reader
}

var _ instances.Repository = (*Store)(nil)

// Provide creates the instance-registry store using separate writer and
// reader pools. This is the assembly point backendapp wires at boot; the
// schema initializes even when features.multiInstance is disabled, because
// the tables are part of the database contract.
func Provide(ctx context.Context, writer, reader *sqlx.DB) (*Store, func() error, error) {
	store, err := NewWithDB(ctx, writer, reader)
	if err != nil {
		return nil, nil, err
	}
	return store, store.Close, nil
}

// NewWithDB initializes the store schema over the supplied pools.
func NewWithDB(ctx context.Context, writer, reader *sqlx.DB) (*Store, error) {
	store := &Store{db: writer, ro: reader}
	if err := store.initSchema(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize instances schema: %w", err)
	}
	return store, nil
}

// Close releases the store. The pools are owned by the caller, so this is a
// no-op kept for the Provide cleanup contract.
func (s *Store) Close() error {
	return nil
}

// initSchema creates both registry tables. The DDL is idempotent: replaying
// it against an existing database is a no-op, and no column is ever added to
// an existing table here, so migrations stay separate.
func (s *Store) initSchema(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS instances (
		id TEXT PRIMARY KEY,
		owner_user_id TEXT NOT NULL,
		name TEXT NOT NULL,
		base_url TEXT NOT NULL,
		credential_secret_id TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 0,
		proxy_port INTEGER NOT NULL DEFAULT 0,
		created_at TIMESTAMP NOT NULL,
		updated_at TIMESTAMP NOT NULL
	);

	CREATE TABLE IF NOT EXISTS remote_notification_occurrences (
		id TEXT PRIMARY KEY,
		owner_user_id TEXT NOT NULL,
		instance_id TEXT NOT NULL,
		event TEXT NOT NULL,
		occurrence_id TEXT NOT NULL,
        task_id TEXT NOT NULL DEFAULT '',
        session_id TEXT NOT NULL DEFAULT '',
        title TEXT NOT NULL DEFAULT '',
        body TEXT NOT NULL DEFAULT '',
        is_read INTEGER NOT NULL DEFAULT 0,
        dismissed INTEGER NOT NULL DEFAULT 0,
		created_at TIMESTAMP NOT NULL,
		UNIQUE(instance_id, event, occurrence_id)
	);

	CREATE INDEX IF NOT EXISTS idx_instances_owner ON instances(owner_user_id);
	CREATE INDEX IF NOT EXISTS idx_remote_notification_occurrences_instance
		ON remote_notification_occurrences(instance_id);
	`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return err
	}
	return nil
}

const instanceColumns = `id, owner_user_id, name, base_url, credential_secret_id, enabled, proxy_port, created_at, updated_at`

// CreateInstance inserts the instance, assigning the ID and timestamps when
// the caller left them unset.
func (s *Store) CreateInstance(ctx context.Context, instance *instances.Instance) error {
	if instance.ID == "" {
		instance.ID = uuid.New().String()
	}
	if instance.CreatedAt.IsZero() {
		instance.CreatedAt = time.Now().UTC()
	}
	if instance.UpdatedAt.IsZero() {
		instance.UpdatedAt = instance.CreatedAt
	}
	_, err := s.db.ExecContext(ctx, s.db.Rebind(`
		INSERT INTO instances (id, owner_user_id, name, base_url, credential_secret_id, enabled, proxy_port, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`), instance.ID, instance.OwnerUserID, instance.Name, instance.BaseURL,
		instance.CredentialSecretID, dialect.BoolToInt(instance.Enabled), instance.ProxyPort,
		instance.CreatedAt, instance.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert instance: %w", err)
	}
	return nil
}

// GetInstance reads one instance owned by ownerUserID. An instance belonging
// to another owner is reported exactly like a missing one.
func (s *Store) GetInstance(ctx context.Context, ownerUserID, id string) (*instances.Instance, error) {
	row := s.ro.QueryRowContext(ctx, s.ro.Rebind(`
		SELECT `+instanceColumns+` FROM instances WHERE id = ? AND owner_user_id = ?
	`), id, ownerUserID)
	instance, err := scanInstance(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, instances.ErrInstanceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get instance: %w", err)
	}
	return instance, nil
}

// ListInstancesByOwner returns every instance owned by ownerUserID, oldest
// first.
func (s *Store) ListInstancesByOwner(ctx context.Context, ownerUserID string) ([]*instances.Instance, error) {
	return s.listInstances(ctx, `
		SELECT `+instanceColumns+` FROM instances WHERE owner_user_id = ? ORDER BY created_at ASC
	`, ownerUserID)
}

// ListAllInstances returns every registered instance across owners, oldest
// first. It backs the internal health poller, which has no caller identity;
// user-facing reads must go through ListInstancesByOwner so rows stay
// owner-scoped.
func (s *Store) ListAllInstances(ctx context.Context) ([]*instances.Instance, error) {
	return s.listInstances(ctx, `
		SELECT `+instanceColumns+` FROM instances ORDER BY created_at ASC
	`)
}

func (s *Store) listInstances(ctx context.Context, query string, args ...any) ([]*instances.Instance, error) {
	rows, err := s.ro.QueryContext(ctx, s.ro.Rebind(query), args...)
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []*instances.Instance
	for rows.Next() {
		instance, err := scanInstance(rows)
		if err != nil {
			return nil, fmt.Errorf("scan instance: %w", err)
		}
		result = append(result, instance)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}
	return result, nil
}

// UpdateInstance writes the instance back, keyed by its ID, and advances
// UpdatedAt. A missing ID surfaces as instances.ErrInstanceNotFound.
func (s *Store) UpdateInstance(ctx context.Context, instance *instances.Instance) error {
	instance.UpdatedAt = time.Now().UTC()
	result, err := s.db.ExecContext(ctx, s.db.Rebind(`
		UPDATE instances
		SET owner_user_id = ?, name = ?, base_url = ?, credential_secret_id = ?, enabled = ?, proxy_port = ?, created_at = ?, updated_at = ?
		WHERE id = ?
	`), instance.OwnerUserID, instance.Name, instance.BaseURL, instance.CredentialSecretID,
		dialect.BoolToInt(instance.Enabled), instance.ProxyPort, instance.CreatedAt,
		instance.UpdatedAt, instance.ID)
	if err != nil {
		return fmt.Errorf("update instance: %w", err)
	}
	return errIfNoRows(result)
}

// DeleteInstance removes one instance owned by ownerUserID. A foreign or
// nonexistent ID is reported identically as ErrInstanceNotFound.
func (s *Store) DeleteInstance(ctx context.Context, ownerUserID, id string) error {
	result, err := s.db.ExecContext(ctx, s.db.Rebind(`
		DELETE FROM instances WHERE id = ? AND owner_user_id = ?
	`), id, ownerUserID)
	if err != nil {
		return fmt.Errorf("delete instance: %w", err)
	}
	return errIfNoRows(result)
}

// UpdateProxyPort records the port the per-instance proxy actually bound.
func (s *Store) UpdateProxyPort(ctx context.Context, id string, port int) error {
	result, err := s.db.ExecContext(ctx, s.db.Rebind(`
		UPDATE instances SET proxy_port = ?, updated_at = ? WHERE id = ?
	`), port, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("update instance proxy port: %w", err)
	}
	return errIfNoRows(result)
}

// errIfNoRows turns a write that matched nothing into ErrInstanceNotFound,
// so an unauthorized target and a missing one are indistinguishable.
func errIfNoRows(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return instances.ErrInstanceNotFound
	}
	return nil
}

// Record persists a semantic occurrence once across live and reconciliation paths.
func (s *Store) Record(ctx context.Context, owner, instanceID, event, occurrenceID, taskID, sessionID, title, body string, at time.Time) (bool, error) {
	if occurrenceID == "" {
		return false, errors.New("occurrence ID is required")
	}
	result, err := s.db.ExecContext(ctx, s.db.Rebind(`INSERT INTO remote_notification_occurrences
 (id,owner_user_id,instance_id,event,occurrence_id,task_id,session_id,title,body,created_at)
 VALUES (?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`), relay.OccurrenceID(instanceID, event, occurrenceID), owner, instanceID, event, occurrenceID, taskID, sessionID, title, body, at.UTC())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (s *Store) ListOccurrences(ctx context.Context, owner string, since time.Time, limit int) ([]relay.Occurrence, error) {
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, 200)
	rows, err := s.ro.QueryContext(ctx, s.ro.Rebind(`SELECT id,instance_id,event,occurrence_id,task_id,session_id,title,body,created_at,is_read,dismissed
 FROM remote_notification_occurrences WHERE owner_user_id=? AND created_at>? ORDER BY created_at ASC,id ASC LIMIT ?`), owner, since.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []relay.Occurrence{}
	for rows.Next() {
		var o relay.Occurrence
		var read, dismissed int
		if err := rows.Scan(&o.ID, &o.InstanceID, &o.Event, &o.OccurrenceID, &o.TaskID, &o.SessionID, &o.Title, &o.Body, &o.CreatedAt, &read, &dismissed); err != nil {
			return nil, err
		}
		o.Read = read != 0
		o.Dismissed = dismissed != 0
		result = append(result, o)
	}
	return result, rows.Err()
}

func (s *Store) LatestOccurrenceTime(ctx context.Context, instanceID string) (time.Time, error) {
	var at time.Time
	err := s.ro.QueryRowContext(ctx, s.ro.Rebind(`SELECT created_at FROM remote_notification_occurrences WHERE instance_id=? ORDER BY created_at DESC LIMIT 1`), instanceID).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	return at, err
}
func (s *Store) MarkOccurrenceRead(ctx context.Context, owner, id string) error {
	result, err := s.db.ExecContext(ctx, s.db.Rebind(`UPDATE remote_notification_occurrences SET is_read=1 WHERE owner_user_id=? AND id=?`), owner, id)
	if err != nil {
		return err
	}
	return errIfNoRows(result)
}
func (s *Store) MarkOccurrenceDismissed(ctx context.Context, owner, id string) error {
	result, err := s.db.ExecContext(ctx, s.db.Rebind(`UPDATE remote_notification_occurrences SET dismissed=1 WHERE owner_user_id=? AND id=?`), owner, id)
	if err != nil {
		return err
	}
	return errIfNoRows(result)
}
func (s *Store) MarkAllOccurrencesRead(ctx context.Context, owner string) error {
	_, err := s.db.ExecContext(ctx, s.db.Rebind(`UPDATE remote_notification_occurrences SET is_read=1 WHERE owner_user_id=?`), owner)
	return err
}
func (s *Store) SweepOccurrences(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, s.db.Rebind(`DELETE FROM remote_notification_occurrences WHERE created_at<?`), before.UTC())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func scanInstance(scanner interface{ Scan(dest ...any) error }) (*instances.Instance, error) {
	instance := &instances.Instance{}
	var enabledInt int
	if err := scanner.Scan(
		&instance.ID,
		&instance.OwnerUserID,
		&instance.Name,
		&instance.BaseURL,
		&instance.CredentialSecretID,
		&enabledInt,
		&instance.ProxyPort,
		&instance.CreatedAt,
		&instance.UpdatedAt,
	); err != nil {
		return nil, err
	}
	instance.Enabled = enabledInt == 1
	return instance, nil
}
