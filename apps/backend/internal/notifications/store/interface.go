package store

import (
	"context"
	"errors"
	"time"

	"github.com/kandev/kandev/internal/notifications/models"
)

// ErrProviderNotFound reports that no provider with the requested ID is
// visible to the requesting user. A provider owned by another user is
// reported the same way as one that does not exist, so an ID probe cannot
// confirm the existence of someone else's provider.
var ErrProviderNotFound = errors.New("notification provider not found")

type Repository interface {
	// CreateProviderWithSubscriptions saves the complete new configuration atomically.
	CreateProviderWithSubscriptions(ctx context.Context, provider *models.Provider, events []string) error
	// UpdateProviderWithSubscriptions is owner-scoped; nil events preserve subscriptions.
	UpdateProviderWithSubscriptions(ctx context.Context, provider *models.Provider, events *[]string) error
	CreateProvider(ctx context.Context, provider *models.Provider) error
	// UpdateProvider is scoped to provider.UserID.
	UpdateProvider(ctx context.Context, provider *models.Provider) error
	GetProvider(ctx context.Context, userID, id string) (*models.Provider, error)
	ListProvidersByUser(ctx context.Context, userID string) ([]*models.Provider, error)
	ListProviderUserIDs(ctx context.Context) ([]string, error)
	DeleteProvider(ctx context.Context, userID, id string) error

	ListSubscriptionsByProvider(ctx context.Context, providerID string) ([]*models.Subscription, error)
	ReplaceSubscriptions(ctx context.Context, providerID, userID string, events []string) error

	InsertDelivery(ctx context.Context, delivery *models.Delivery) (bool, error)
	DeleteDelivery(ctx context.Context, providerID, eventType, occurrenceID string) error

	// InsertOccurrence records one dispatched semantic occurrence for its
	// recipient, deduplicated on (user, event, occurrence); it reports whether
	// the row was newly inserted.
	InsertOccurrence(ctx context.Context, occurrence *models.Occurrence) (bool, error)
	// ListOccurrences returns the occurrences dispatched to userID strictly
	// newer than since, oldest first, capped at limit.
	ListOccurrences(ctx context.Context, userID string, since time.Time, limit int) ([]*models.Occurrence, error)
	// SweepOccurrences deletes occurrence rows older than before and returns
	// how many rows were removed.
	SweepOccurrences(ctx context.Context, before time.Time) (int64, error)

	Close() error
}
