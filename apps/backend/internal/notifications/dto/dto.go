package dto

import (
	"time"

	"github.com/kandev/kandev/internal/notifications/models"
)

type NotificationProviderDTO struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Type      string                 `json:"type"`
	Config    map[string]interface{} `json:"config"`
	Enabled   bool                   `json:"enabled"`
	Events    []string               `json:"events"`
	CreatedAt string                 `json:"created_at"`
	UpdatedAt string                 `json:"updated_at"`
}

type NotificationProvidersResponse struct {
	Providers        []NotificationProviderDTO `json:"providers"`
	AppriseAvailable bool                      `json:"apprise_available"`
	Events           []string                  `json:"events"`
}

type UpsertProviderRequest struct {
	Name    string                 `json:"name"`
	Type    string                 `json:"type"`
	Config  map[string]interface{} `json:"config,omitempty"`
	Enabled *bool                  `json:"enabled,omitempty"`
	Events  *[]string              `json:"events,omitempty"`
}

type UpdateProviderRequest struct {
	Name    *string                `json:"name,omitempty"`
	Type    *string                `json:"type,omitempty"`
	Config  map[string]interface{} `json:"config,omitempty"`
	Enabled *bool                  `json:"enabled,omitempty"`
	Events  *[]string              `json:"events,omitempty"`
}

// NotificationOccurrenceDTO is one dispatched occurrence on the
// reconciliation feed: the exact fields a federating peer consumes, keyed on
// the semantic occurrence identity.
type NotificationOccurrenceDTO struct {
	Event        string `json:"event"`
	OccurrenceID string `json:"occurrence_id"`
	TaskID       string `json:"task_id"`
	SessionID    string `json:"session_id"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	CreatedAt    string `json:"created_at"`
}

func FromProvider(provider *models.Provider, events []string) NotificationProviderDTO {
	return NotificationProviderDTO{
		ID:        provider.ID,
		Name:      provider.Name,
		Type:      string(provider.Type),
		Config:    provider.Config,
		Enabled:   provider.Enabled,
		Events:    events,
		CreatedAt: provider.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: provider.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func FromOccurrence(occurrence *models.Occurrence) NotificationOccurrenceDTO {
	return NotificationOccurrenceDTO{
		Event:        occurrence.EventType,
		OccurrenceID: occurrence.OccurrenceID,
		TaskID:       occurrence.TaskID,
		SessionID:    occurrence.SessionID,
		Title:        occurrence.Title,
		Body:         occurrence.Body,
		CreatedAt:    occurrence.CreatedAt.UTC().Format(time.RFC3339),
	}
}
