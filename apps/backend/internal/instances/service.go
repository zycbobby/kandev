package instances

import (
	"context"
	"errors"
	"strings"
)

// ErrInstanceNotFound reports that no instance with the requested ID is
// visible to the requesting owner. An instance owned by another user is
// reported the same way as one that does not exist, so probing IDs reveals
// nothing about other users' configuration.
var ErrInstanceNotFound = errors.New("instance not found")

// ErrMultiInstanceDisabled reports that the operation was refused because
// features.multiInstance is off. Every service method fails closed with it
// while the flag is disabled, so the registry stays inert even for a caller
// that reaches the service directly.
var ErrMultiInstanceDisabled = errors.New("multi-instance feature is disabled")

// Repository is the persistence contract behind the instance registry. Reads
// and deletes are owner-scoped: a row belonging to another owner is reported
// exactly like a missing one, through ErrInstanceNotFound. ListAllInstances
// is the one exception: it backs the internal health poller, which has no
// caller identity; user-facing reads must go through ListInstancesByOwner.
type Repository interface {
	CreateInstance(ctx context.Context, instance *Instance) error
	GetInstance(ctx context.Context, ownerUserID, id string) (*Instance, error)
	ListInstancesByOwner(ctx context.Context, ownerUserID string) ([]*Instance, error)
	ListAllInstances(ctx context.Context) ([]*Instance, error)
	UpdateInstance(ctx context.Context, instance *Instance) error
	DeleteInstance(ctx context.Context, ownerUserID, id string) error
	UpdateProxyPort(ctx context.Context, id string, port int) error
}

// MultiInstanceEnabled reports whether features.multiInstance is currently
// effective. It is a predicate rather than a snapshot so restart-required
// overrides apply on the next boot; nil means disabled and every method
// fails closed.
type MultiInstanceEnabled func() bool

// Service owns the per-owner remote-instance registry. All methods take the
// calling user's ID and scope every read and write to it. The in-process
// health tracker it carries is written by the HealthPoller (health.go) and
// read by GetHealth.
type Service struct {
	repo           Repository
	enabled        MultiInstanceEnabled
	health         *healthTracker
	changeNotifier func()
}

// NewService builds the instance registry service. The enabled predicate
// gates every method; pass nil only when the feature must stay inert.
func NewService(repo Repository, enabled MultiInstanceEnabled) *Service {
	return &Service{repo: repo, enabled: enabled, health: newHealthTracker()}
}

// SetChangeNotifier installs the callback used to reconcile instance bridges.
// Configure it during assembly, before serving requests.
func (s *Service) SetChangeNotifier(notify func()) { s.changeNotifier = notify }

func (s *Service) notifyChanged() {
	if s.changeNotifier == nil {
		return
	}
	// A notification failure cannot change an already-committed write's result.
	defer func() { _ = recover() }()
	s.changeNotifier()
}

func (s *Service) checkEnabled() error {
	if s.enabled == nil || !s.enabled() {
		return ErrMultiInstanceDisabled
	}
	return nil
}

// CreateInstance registers a new remote instance owned by callerUserID. The
// store assigns the ID and timestamps.
func (s *Service) CreateInstance(
	ctx context.Context, callerUserID, name, baseURL, credentialSecretID string, enabled bool,
) (*Instance, error) {
	if err := s.checkEnabled(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("instance name is required")
	}
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("instance base URL is required")
	}
	instance := &Instance{
		OwnerUserID:        callerUserID,
		Name:               name,
		BaseURL:            baseURL,
		CredentialSecretID: credentialSecretID,
		Enabled:            enabled,
	}
	if err := s.repo.CreateInstance(ctx, instance); err != nil {
		return nil, err
	}
	s.notifyChanged()
	return instance, nil
}

// ListInstances returns every instance owned by callerUserID, oldest first.
func (s *Service) ListInstances(ctx context.Context, callerUserID string) ([]*Instance, error) {
	if err := s.checkEnabled(); err != nil {
		return nil, err
	}
	return s.repo.ListInstancesByOwner(ctx, callerUserID)
}

// InstanceUpdate patches an instance; nil fields preserve the stored value.
type InstanceUpdate struct {
	Name               *string
	BaseURL            *string
	CredentialSecretID *string
	Enabled            *bool
	ProxyPort          *int
}

// UpdateInstance applies the patch to the instance owned by callerUserID.
// A foreign or missing ID surfaces as ErrInstanceNotFound.
func (s *Service) UpdateInstance(
	ctx context.Context, callerUserID, id string, update InstanceUpdate,
) (*Instance, error) {
	if err := s.checkEnabled(); err != nil {
		return nil, err
	}
	current, err := s.repo.GetInstance(ctx, callerUserID, id)
	if err != nil {
		return nil, err
	}
	if err := applyInstanceUpdate(current, update); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateInstance(ctx, current); err != nil {
		return nil, err
	}
	s.notifyChanged()
	return current, nil
}

func applyInstanceUpdate(current *Instance, update InstanceUpdate) error {
	if update.Name != nil {
		if strings.TrimSpace(*update.Name) == "" {
			return errors.New("instance name is required")
		}
		current.Name = *update.Name
	}
	if update.BaseURL != nil {
		if strings.TrimSpace(*update.BaseURL) == "" {
			return errors.New("instance base URL is required")
		}
		current.BaseURL = *update.BaseURL
	}
	if update.CredentialSecretID != nil {
		current.CredentialSecretID = *update.CredentialSecretID
	}
	if update.Enabled != nil {
		current.Enabled = *update.Enabled
	}
	if update.ProxyPort != nil {
		current.ProxyPort = *update.ProxyPort
	}
	return nil
}

// DeleteInstance removes the instance owned by callerUserID. A foreign or
// missing ID surfaces as ErrInstanceNotFound.
func (s *Service) DeleteInstance(ctx context.Context, callerUserID, id string) error {
	if err := s.checkEnabled(); err != nil {
		return err
	}
	if err := s.repo.DeleteInstance(ctx, callerUserID, id); err != nil {
		return err
	}
	s.notifyChanged()
	return nil
}
