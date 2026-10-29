package providers

import (
	"context"
	"errors"

	"github.com/kandev/kandev/internal/notifications/models"
)

var ErrNoEligibleSubscriber = errors.New("no eligible notification subscriber")

type Message struct {
	EventType      string
	Title          string
	Body           string
	TaskID         string
	TaskSessionID  string
	OccurrenceID   string
	UserID         string
	Config         map[string]interface{}
	Payload        map[string]string
	RuntimeUpdates []models.RuntimeUpdateMember
	// OriginInstanceID and OriginInstanceName identify the federating
	// instance a relayed occurrence came from; both stay empty for local
	// events. Providers that surface origin (the system provider's title,
	// the frontend's origin label) read them; others ignore them.
	OriginInstanceID   string
	OriginInstanceName string
}

type Provider interface {
	Available() bool
	Validate(config map[string]interface{}) error
	Send(ctx context.Context, message Message) error
}
