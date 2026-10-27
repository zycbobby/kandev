package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/kandev/kandev/internal/notifications/models"
)

// DefaultOccurrenceRetention bounds how long dispatched notification
// occurrences stay queryable for relay reconciliation.
const DefaultOccurrenceRetention = 7 * 24 * time.Hour

const notificationOccurrenceColumns = `user_id, event_type, occurrence_id, task_id, session_id, title, body, created_at`

// InsertOccurrence records one dispatched semantic occurrence, assigning the
// row ID and creation time. The (user_id, event_type, occurrence_id) unique
// key deduplicates a replayed dispatch, mirroring the delivery claim; it
// reports whether the row was newly inserted.
func (r *sqliteRepository) InsertOccurrence(ctx context.Context, occurrence *models.Occurrence) (bool, error) {
	if occurrence.OccurrenceID == "" {
		return false, errors.New("notification occurrence ID is required")
	}
	occurrence.ID = uuid.New().String()
	occurrence.CreatedAt = time.Now().UTC()
	result, err := r.db.ExecContext(ctx, r.db.Rebind(`
		INSERT INTO notification_occurrences (id, `+notificationOccurrenceColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING
	`), occurrence.ID, occurrence.UserID, occurrence.EventType, occurrence.OccurrenceID,
		occurrence.TaskID, occurrence.SessionID, occurrence.Title, occurrence.Body, occurrence.CreatedAt)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

// ListOccurrences returns the occurrences dispatched to userID strictly newer
// than since, oldest first, capped at limit. It is the read side a federating
// peer reconciles against, so it stays scoped to one user's rows.
func (r *sqliteRepository) ListOccurrences(ctx context.Context, userID string, since time.Time, limit int) ([]*models.Occurrence, error) {
	rows, err := r.ro.QueryContext(ctx, r.ro.Rebind(`
		SELECT `+notificationOccurrenceColumns+`
		FROM notification_occurrences
		WHERE user_id = ? AND created_at > ?
		ORDER BY created_at ASC
		LIMIT ?
	`), userID, since.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var occurrences []*models.Occurrence
	for rows.Next() {
		occurrence, err := scanOccurrence(rows)
		if err != nil {
			return nil, err
		}
		occurrences = append(occurrences, occurrence)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return occurrences, nil
}

// SweepOccurrences deletes occurrence rows older than before and returns how
// many rows were removed, keeping the table bounded without user action.
func (r *sqliteRepository) SweepOccurrences(ctx context.Context, before time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx, r.db.Rebind(`
		DELETE FROM notification_occurrences WHERE created_at < ?
	`), before.UTC())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func scanOccurrence(scanner interface{ Scan(dest ...any) error }) (*models.Occurrence, error) {
	occurrence := &models.Occurrence{}
	if err := scanner.Scan(
		&occurrence.UserID,
		&occurrence.EventType,
		&occurrence.OccurrenceID,
		&occurrence.TaskID,
		&occurrence.SessionID,
		&occurrence.Title,
		&occurrence.Body,
		&occurrence.CreatedAt,
	); err != nil {
		return nil, err
	}
	return occurrence, nil
}
