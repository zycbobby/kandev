package relay

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
	"time"
)

func TestReconcileStopsAtStuckSecondBoundary(t *testing.T) {
	rows := make([]Occurrence, 200)
	for i := range rows {
		rows[i] = occurrence()
	}
	requests := 0
	r, _, _ := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		require.NoError(t, json.NewEncoder(w).Encode(rows))
	}))
	require.NoError(t, r.reconcile(t.Context()))
	require.Equal(t, 2, requests)
	require.Equal(t, occurrence().CreatedAt.Add(time.Second), r.cursor)
}

func TestReconcileRecoversMissedWindow(t *testing.T) {
	first := occurrence()
	missed := first
	missed.OccurrenceID = "missed"
	missed.CreatedAt = first.CreatedAt.Add(time.Second)
	latest := first
	latest.OccurrenceID = "live"
	latest.CreatedAt = first.CreatedAt.Add(2 * time.Second)
	rows := []Occurrence{first}
	r, s, n := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		since, err := time.Parse(time.RFC3339, req.URL.Query().Get("since"))
		require.NoError(t, err)
		result := []Occurrence{}
		for _, o := range rows {
			if o.CreatedAt.After(since) {
				result = append(result, o)
			}
		}
		require.NoError(t, json.NewEncoder(w).Encode(result))
	}))
	require.NoError(t, r.reconcile(t.Context()))
	// A disconnected event precedes the first event received live after reconnect.
	rows = append(rows, missed, latest)
	require.NoError(t, r.record(t.Context(), latest))
	require.NoError(t, r.reconcile(t.Context()))
	require.Len(t, s.rows, 3)
	require.Len(t, n.recorded(), 3)
}
