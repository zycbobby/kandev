package backendapp

import (
	"context"
	"sync"
	"time"

	"github.com/kandev/kandev/internal/common/config"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/instances"
	"github.com/kandev/kandev/internal/instances/relay"
	"github.com/kandev/kandev/internal/integrations/secretadapter"
	"github.com/kandev/kandev/internal/system/maintenance"
	"go.uber.org/zap"
)

type remoteOccurrenceStore interface {
	relay.Store
	ListOccurrences(context.Context, string, time.Time, int) ([]relay.Occurrence, error)
	MarkOccurrenceRead(context.Context, string, string) error
	MarkOccurrenceDismissed(context.Context, string, string) error
	MarkAllOccurrencesRead(context.Context, string) error
	SweepOccurrences(context.Context, time.Time) (int64, error)
}
type instanceRelayEntry struct {
	instance instances.Instance
	relay    *relay.Relay
}

// RelayManager owns enabled-instance relays and bounded local retention.
type RelayManager struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	repo    instances.Repository
	store   remoteOccurrenceStore
	secrets *secretadapter.Adapter
	hub     relay.Broadcaster
	log     *logger.Logger
	entries map[string]instanceRelayEntry
}

func initInstanceRelays(ctx context.Context, cfg *config.Config, repos *Repositories, services *Services, hub relay.Broadcaster, log *logger.Logger) func() {
	if !cfg.Features.MultiInstance {
		return func() {}
	}
	store, ok := repos.Instances.(remoteOccurrenceStore)
	if !ok {
		log.Error("instance occurrence store unavailable")
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &RelayManager{ctx: ctx, cancel: cancel, repo: repos.Instances, store: store, hub: hub, log: log, entries: map[string]instanceRelayEntry{}}
	if repos.Secrets != nil {
		m.secrets = secretadapter.New(repos.Secrets)
	}
	services.InstanceRelays = m
	services.Instances.SetChangeNotifier(func() { services.InstanceProxies.Reconcile(); m.Reconcile() })
	m.Reconcile()
	return m.Stop
}
func (m *RelayManager) Reconcile() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		return
	}
	items, err := m.repo.ListAllInstances(m.ctx)
	if err != nil {
		m.log.Warn("instance relay registry unavailable")
		return
	}
	wanted := map[string]bool{}
	for _, item := range items {
		if !item.Enabled {
			continue
		}
		wanted[item.ID] = true
		entry, exists := m.entries[item.ID]
		if exists && proxyConfigurationEqual(entry.instance, *item) {
			continue
		}
		if exists {
			entry.relay.Stop()
			delete(m.entries, item.ID)
		}
		m.startRelay(*item)
	}
	for id, entry := range m.entries {
		if !wanted[id] {
			entry.relay.Stop()
			delete(m.entries, id)
		}
	}
}
func (m *RelayManager) startRelay(item instances.Instance) {
	credential := ""
	if item.CredentialSecretID != "" {
		if m.secrets == nil {
			m.log.Warn("instance relay credential unavailable", zap.String("instance", item.Name))
			return
		}
		value, err := m.secrets.Reveal(m.ctx, item.CredentialSecretID)
		if err != nil {
			m.log.Warn("instance relay credential unavailable", zap.String("instance", item.Name))
			return
		}
		credential = value
	}
	r := relay.NewRelay(item, credential, m.store, m.hub, m.log)
	r.Start(m.ctx)
	m.entries[item.ID] = instanceRelayEntry{instance: item, relay: r}
}
func (m *RelayManager) startRetention(guard *maintenance.Guard) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			if release, ok := guard.TryAcquire(); ok {
				_, err := m.store.SweepOccurrences(m.ctx, time.Now().UTC().Add(-7*24*time.Hour))
				release()
				if err != nil && m.ctx.Err() == nil {
					m.log.Warn("remote occurrence retention failed", zap.Error(err))
				}
			}
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
func (m *RelayManager) Stop() {
	m.cancel()
	m.mu.Lock()
	for id, entry := range m.entries {
		entry.relay.Stop()
		delete(m.entries, id)
	}
	m.mu.Unlock()
	m.wg.Wait()
}
