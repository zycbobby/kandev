package service

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/notifications/models"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	"go.uber.org/zap"
)

// The occurrence surface of the in-memory repository: recording mirrors the
// SQLite contract (CreatedAt assigned at insert, (user, event, occurrence)
// dedup) and sweeps delete rows older than the cutoff while recording each
// cutoff the service asked for.
func (r *notificationTestRepository) InsertOccurrence(_ context.Context, occurrence *models.Occurrence) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.occurrences {
		if existing.UserID == occurrence.UserID && existing.EventType == occurrence.EventType && existing.OccurrenceID == occurrence.OccurrenceID {
			return false, nil
		}
	}
	if occurrence.CreatedAt.IsZero() {
		occurrence.CreatedAt = time.Now().UTC()
	}
	r.occurrences = append(r.occurrences, occurrence)
	return true, nil
}

func (r *notificationTestRepository) ListOccurrences(_ context.Context, userID string, since time.Time, limit int) ([]*models.Occurrence, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]*models.Occurrence, 0, limit)
	for _, occurrence := range r.occurrences {
		if occurrence.UserID != userID || !occurrence.CreatedAt.After(since) {
			continue
		}
		result = append(result, occurrence)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (r *notificationTestRepository) SweepOccurrences(_ context.Context, before time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	swept := int64(0)
	kept := r.occurrences[:0]
	for _, occurrence := range r.occurrences {
		if occurrence.CreatedAt.Before(before) {
			swept++
			continue
		}
		kept = append(kept, occurrence)
	}
	r.occurrences = kept
	r.sweepCutoffs = append(r.sweepCutoffs, before)
	return swept, nil
}

// occurrenceStubs satisfies the occurrence surface of the repository for
// multiUserRepository, whose tests do not exercise occurrences.
func (r *multiUserRepository) InsertOccurrence(context.Context, *models.Occurrence) (bool, error) {
	return false, nil
}

func (r *multiUserRepository) ListOccurrences(context.Context, string, time.Time, int) ([]*models.Occurrence, error) {
	return nil, nil
}

func (r *multiUserRepository) SweepOccurrences(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func occurrenceTestTaskGetter(ownerID, title string) notificationTestTaskGetter {
	return notificationTestTaskGetter{
		task:      &taskmodels.Task{Title: title, WorkspaceID: "workspace-1"},
		workspace: &taskmodels.Workspace{ID: "workspace-1", OwnerID: ownerID},
	}
}

func TestOccurrenceRecordedAtDispatch(t *testing.T) {
	t.Setenv(desktopNativeNotificationsEnv, "true")
	log, err := logger.NewFromZap(zap.NewNop())
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}

	t.Run("task occurrence records what was dispatched", func(t *testing.T) {
		repo := &notificationTestRepository{
			providers: []*models.Provider{{ID: "provider-1", UserID: "user-1", Type: models.ProviderTypeLocal, Enabled: true}},
			subscriptions: map[string][]*models.Subscription{
				"provider-1": {{ProviderID: "provider-1", EventType: EventTaskSessionTurnFinished, Enabled: true}},
			},
		}
		service := NewService(repo, occurrenceTestTaskGetter("user-1", "Fix delivery"), nil, log, nil)
		capture := &captureProvider{}
		service.providers[models.ProviderTypeLocal] = capture

		service.HandleTaskTurnFinished(context.Background(), "task-1", "session-1", "turn-1")
		service.HandleTaskTurnFinished(context.Background(), "task-1", "session-1", "turn-1")
		service.HandleTaskTurnFinished(context.Background(), "task-1", "session-1", "turn-2")

		if len(repo.occurrences) != 2 {
			t.Fatalf("recorded occurrences = %#v, want one per distinct dispatch", repo.occurrences)
		}
		got := repo.occurrences[0]
		if got.UserID != "user-1" || got.EventType != EventTaskSessionTurnFinished || got.OccurrenceID != "turn-1" ||
			got.TaskID != "task-1" || got.SessionID != "session-1" ||
			got.Title != "Agent turn finished" || got.Body != "The agent finished a turn on \"Fix delivery\"." {
			t.Fatalf("recorded occurrence = %#v", got)
		}
		if repo.occurrences[1].OccurrenceID != "turn-2" {
			t.Fatalf("second occurrence = %#v, want turn-2", repo.occurrences[1])
		}
	})

	t.Run("instance-wide fanout records one occurrence per recipient", func(t *testing.T) {
		repo := &notificationTestRepository{
			providers: []*models.Provider{
				{ID: "local-1", UserID: "user-1", Type: models.ProviderTypeLocal, Enabled: true},
				{ID: "local-2", UserID: "user-2", Type: models.ProviderTypeLocal, Enabled: true},
			},
			subscriptions: map[string][]*models.Subscription{
				"local-1": {{ProviderID: "local-1", EventType: EventSystemUpdateAvailable, Enabled: true}},
				"local-2": {{ProviderID: "local-2", EventType: EventSystemUpdateAvailable, Enabled: true}},
			},
		}
		service := NewService(repo, nil, nil, log, nil)
		capture := &captureProvider{}
		service.providers[models.ProviderTypeLocal] = capture

		service.HandleUpdateAvailable(context.Background(), "v9.9.9", "https://example.test/v9.9.9")

		if len(repo.occurrences) != 2 {
			t.Fatalf("recorded occurrences = %#v, want one per recipient", repo.occurrences)
		}
		byUser := map[string]*models.Occurrence{}
		for _, occurrence := range repo.occurrences {
			byUser[occurrence.UserID] = occurrence
		}
		for _, userID := range []string{"user-1", "user-2"} {
			occurrence, ok := byUser[userID]
			if !ok {
				t.Fatalf("no occurrence recorded for %s: %#v", userID, repo.occurrences)
			}
			if occurrence.EventType != EventSystemUpdateAvailable || occurrence.OccurrenceID != "v9.9.9" ||
				occurrence.TaskID != "" || occurrence.SessionID != "" ||
				occurrence.Title != "Kandev update available" {
				t.Fatalf("occurrence for %s = %#v", userID, occurrence)
			}
		}
	})
}

func TestOccurrenceRetentionSweep(t *testing.T) {
	t.Setenv(desktopNativeNotificationsEnv, "true")
	log, err := logger.NewFromZap(zap.NewNop())
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	repo := &notificationTestRepository{
		providers: []*models.Provider{{ID: "provider-1", UserID: "user-1", Type: models.ProviderTypeLocal, Enabled: true}},
		subscriptions: map[string][]*models.Subscription{
			"provider-1": {{ProviderID: "provider-1", EventType: EventTaskSessionTurnFinished, Enabled: true}},
		},
		occurrences: []*models.Occurrence{
			{UserID: "user-1", EventType: EventTaskSessionTurnFinished, OccurrenceID: "stale", CreatedAt: base.Add(-8 * 24 * time.Hour)},
			{UserID: "user-1", EventType: EventTaskSessionTurnFinished, OccurrenceID: "fresh", CreatedAt: base.Add(-time.Hour)},
		},
	}
	service := NewService(repo, occurrenceTestTaskGetter("user-1", "Fix delivery"), nil, log, nil)
	capture := &captureProvider{}
	service.providers[models.ProviderTypeLocal] = capture
	now := base
	service.occurrenceSweepNow = func() time.Time { return now }

	// The first dispatch after construction sweeps immediately: the expired
	// row is removed while the fresh one survives.
	service.HandleTaskTurnFinished(context.Background(), "task-1", "session-1", "turn-1")
	for _, occurrence := range repo.occurrences {
		if occurrence.OccurrenceID == "stale" {
			t.Fatal("expired occurrence survived the dispatch-triggered sweep")
		}
	}
	foundFresh := false
	for _, occurrence := range repo.occurrences {
		if occurrence.OccurrenceID == "fresh" {
			foundFresh = true
		}
	}
	if !foundFresh {
		t.Fatalf("fresh occurrence was swept away: %#v", repo.occurrences)
	}
	if len(repo.sweepCutoffs) != 1 || !repo.sweepCutoffs[0].Equal(base.Add(-defaultOccurrenceRetention)) {
		t.Fatalf("sweep cutoffs = %#v, want one sweep at the retention window", repo.sweepCutoffs)
	}

	// The throttle suppresses an immediate second sweep.
	service.HandleTaskTurnFinished(context.Background(), "task-1", "session-1", "turn-2")
	if len(repo.sweepCutoffs) != 1 {
		t.Fatalf("sweep cutoffs = %#v, want the second dispatch throttled", repo.sweepCutoffs)
	}

	// Once the sweep interval passes, dispatching sweeps again.
	now = base.Add(occurrenceSweepInterval + time.Second)
	service.HandleTaskTurnFinished(context.Background(), "task-1", "session-1", "turn-3")
	if len(repo.sweepCutoffs) != 2 || !repo.sweepCutoffs[1].Equal(now.Add(-defaultOccurrenceRetention)) {
		t.Fatalf("sweep cutoffs = %#v, want a second sweep after the interval", repo.sweepCutoffs)
	}

	// The relay's list path drives the sweep too.
	now = now.Add(occurrenceSweepInterval + time.Second)
	if _, err := service.ListOccurrences(context.Background(), "user-1", time.Time{}, 0); err != nil {
		t.Fatalf("list occurrences: %v", err)
	}
	if len(repo.sweepCutoffs) != 3 {
		t.Fatalf("sweep cutoffs = %#v, want the list path to sweep", repo.sweepCutoffs)
	}
}
