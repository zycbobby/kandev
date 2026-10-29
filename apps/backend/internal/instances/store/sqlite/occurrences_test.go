package sqlite

import (
	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/db"
	"github.com/stretchr/testify/require"
	"path/filepath"
	"testing"
	"time"
)

func TestOccurrenceLifecycle(t *testing.T) {
	raw, err := db.OpenSQLite(filepath.Join(t.TempDir(), "instances.db"))
	require.NoError(t, err)
	database := sqlx.NewDb(raw, "sqlite3")
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	s, err := NewWithDB(t.Context(), database, database)
	require.NoError(t, err)
	_, err = NewWithDB(t.Context(), database, database)
	require.NoError(t, err)
	now := time.Now().UTC()
	inserted, err := s.Record(t.Context(), "owner", "instance", "session.turn_finished", "turn", "task", "session", "title", "body", now)
	require.NoError(t, err)
	require.True(t, inserted)
	inserted, err = s.Record(t.Context(), "owner", "instance", "session.turn_finished", "turn", "task", "session", "title", "body", now)
	require.NoError(t, err)
	require.False(t, inserted)
	latest, err := s.LatestOccurrenceTime(t.Context(), "instance")
	require.NoError(t, err)
	require.WithinDuration(t, now, latest, time.Microsecond)
	n, err := s.SweepOccurrences(t.Context(), now.Add(time.Second))
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
}
