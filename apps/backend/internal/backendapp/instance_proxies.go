package backendapp

import (
	"context"
	"sync"

	"github.com/kandev/kandev/internal/common/config"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/instances"
	"github.com/kandev/kandev/internal/instances/proxy"
	"github.com/kandev/kandev/internal/integrations/secretadapter"
	"go.uber.org/zap"
)

type instanceProxyEntry struct {
	instance instances.Instance
	server   *proxy.Server
}

// ProxyManager reconciles at boot and after successful registry writes, without polling.
type ProxyManager struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	repo    instances.Repository
	secrets *secretadapter.Adapter
	authOn  bool
	log     *logger.Logger
	servers map[string]instanceProxyEntry
}

func initInstanceProxies(ctx context.Context, cfg *config.Config, repos *Repositories, services *Services, log *logger.Logger) func() {
	if !cfg.Features.MultiInstance {
		return func() {}
	}
	managerCtx, cancel := context.WithCancel(ctx)
	manager := &ProxyManager{ctx: managerCtx, cancel: cancel, repo: repos.Instances, authOn: cfg.Features.Auth, log: log, servers: make(map[string]instanceProxyEntry)}
	if repos.Secrets != nil {
		manager.secrets = secretadapter.New(repos.Secrets)
	}
	services.Instances = instances.NewService(repos.Instances, func() bool { return cfg.Features.MultiInstance })
	services.InstanceProxies = manager
	services.Instances.SetChangeNotifier(manager.Reconcile)
	manager.Reconcile()
	return manager.Stop
}

// Server returns the live bridge for a previously authorized instance ID.
// Callers must authorize through the owner-scoped instance service first.
func (m *ProxyManager) Server(id string) *proxy.Server {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.servers[id].server
}

// Reconcile applies the current registry snapshot synchronously after committed writes.
func (m *ProxyManager) Reconcile() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		return
	}
	items, err := m.repo.ListAllInstances(m.ctx)
	if err != nil {
		m.log.Warn("instance proxy registry unavailable")
		return
	}
	wanted := make(map[string]bool, len(items))
	for _, item := range items {
		if !item.Enabled {
			continue
		}
		wanted[item.ID] = true
		entry, exists := m.servers[item.ID]
		if exists && proxyConfigurationEqual(entry.instance, *item) {
			continue
		}
		if exists {
			entry.server.Stop()
			delete(m.servers, item.ID)
		}
		m.startServer(*item)
	}
	for id, entry := range m.servers {
		if !wanted[id] {
			entry.server.Stop()
			delete(m.servers, id)
		}
	}
}

func proxyConfigurationEqual(a, b instances.Instance) bool {
	return a.Name == b.Name && a.BaseURL == b.BaseURL && a.CredentialSecretID == b.CredentialSecretID && a.OwnerUserID == b.OwnerUserID && a.ProxyPort == b.ProxyPort
}

func (m *ProxyManager) startServer(item instances.Instance) {
	credential := func() (string, error) {
		if item.CredentialSecretID == "" {
			return "", nil
		}
		if m.secrets == nil {
			return "", instances.ErrInstanceNotFound
		}
		return m.secrets.Reveal(m.ctx, item.CredentialSecretID)
	}
	server := proxy.NewServer(item, credential, m.authOn, m.log)
	if err := server.Start(m.ctx); err != nil {
		m.log.Warn("instance proxy start failed", zap.String("instance", item.Name))
		return
	}
	if err := m.repo.UpdateProxyPort(m.ctx, item.ID, server.Port()); err != nil {
		server.Stop()
		m.log.Warn("instance proxy port persistence failed", zap.String("instance", item.Name))
		return
	}
	item.ProxyPort = server.Port()
	m.servers[item.ID] = instanceProxyEntry{instance: item, server: server}
	m.log.Info("instance proxy started", zap.String("instance", item.Name), zap.Int("port", server.Port()))
}

// Stop prevents subsequent reconciliation and drains every owned bridge.
func (m *ProxyManager) Stop() {
	m.cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, entry := range m.servers {
		entry.server.Stop()
		delete(m.servers, id)
	}
}
