package instances

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/integrations/healthpoll"
)

// MinRemoteVersion is the oldest remote Kandev version this local instance
// federates: the release this multi-instance change ships with, because the
// notification-occurrences API the federation consumes lands in that release.
// A remote reporting an older version is reachable but unsupported.
const MinRemoteVersion = "0.98.0"

const (
	// systemInfoPath is the remote endpoint every probe reads; its response
	// carries the version the remote reports (internal/system/info).
	systemInfoPath = "/api/v1/system/info"
	// probeTimeout bounds one probe so a hung remote cannot stall a pass.
	probeTimeout = 10 * time.Second
	// maxSystemInfoBodyBytes bounds the decoded payload; the real response
	// is a few hundred bytes.
	maxSystemInfoBodyBytes = 64 << 10
)

// HealthReport is the last observed health of one registered remote
// instance. Reachable reports whether the probe round-trip succeeded,
// Version is what the remote reported, Supported compares Version against
// MinRemoteVersion, and Error explains any degraded state (probe failure or
// a version below the floor). LastCheckedAt is the probe time, so a report
// predating an instance's registration reads as never checked.
type HealthReport struct {
	Reachable     bool
	Version       string
	Error         string
	Supported     bool
	LastCheckedAt time.Time
}

// healthTracker is the in-process health state: the poller writes per-
// instance reports, Service.GetHealth reads them. It is deliberately not
// persisted — health is an observation of the moment, not registry data.
type healthTracker struct {
	mu      sync.RWMutex
	reports map[string]HealthReport
}

func newHealthTracker() *healthTracker {
	return &healthTracker{reports: make(map[string]HealthReport)}
}

func (t *healthTracker) set(instanceID string, report HealthReport) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reports[instanceID] = report
}

func (t *healthTracker) report(instanceID string) HealthReport {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.reports[instanceID]
}

// GetHealth returns the last polled health of the instance owned by
// callerUserID. A foreign or missing ID surfaces as ErrInstanceNotFound,
// like every registry read; before the first probe completes the report is
// the zero value (never checked).
func (s *Service) GetHealth(ctx context.Context, callerUserID, instanceID string) (HealthReport, error) {
	if err := s.checkEnabled(); err != nil {
		return HealthReport{}, err
	}
	if _, err := s.repo.GetInstance(ctx, callerUserID, instanceID); err != nil {
		return HealthReport{}, err
	}
	return s.health.report(instanceID), nil
}

// HealthPoller probes every registered remote instance on the shared
// healthpoll cadence (90s, first pass immediately on Start) and records the
// results on the service's health tracker. The loop semantics are delegated
// to internal/integrations/healthpoll; this type owns the probing itself.
type HealthPoller struct {
	service *Service
	client  *http.Client
	logger  *logger.Logger
	loop    *healthpoll.Poller
}

// NewHealthPoller builds the poller over the registry service. The logger
// must be non-nil, matching the healthpoll contract.
func NewHealthPoller(service *Service, log *logger.Logger) *HealthPoller {
	poller := &HealthPoller{
		service: service,
		client:  &http.Client{Timeout: probeTimeout},
		logger:  log,
	}
	poller.loop = healthpoll.New("instances", healthProber{poller: poller}, log)
	return poller
}

// Start launches the probe loop; the first pass runs immediately so the
// settings UI has a status without waiting a full interval. Start without
// an intervening Stop is a no-op.
func (p *HealthPoller) Start(ctx context.Context) {
	p.loop.Start(ctx)
}

// Stop cancels the probe loop and waits for it to drain.
func (p *HealthPoller) Stop() {
	p.loop.Stop()
}

// probeAll probes every registered instance and records each result. One
// instance's failure never aborts the pass: health is per-instance and
// isolated by design. While the feature flag is off the pass is a no-op, so
// a mis-wired Start cannot make the disabled registry observable.
func (p *HealthPoller) probeAll(ctx context.Context) {
	if err := p.service.checkEnabled(); err != nil {
		return
	}
	registered, err := p.service.repo.ListAllInstances(ctx)
	if err != nil {
		p.logger.Warn("instances health poller: list instances failed", zap.Error(err))
		return
	}
	for _, instance := range registered {
		if ctx.Err() != nil {
			return
		}
		p.service.health.set(instance.ID, p.probeInstance(ctx, instance))
	}
}

func (p *HealthPoller) probeInstance(ctx context.Context, instance *Instance) HealthReport {
	report := HealthReport{LastCheckedAt: time.Now().UTC()}
	info, err := p.fetchSystemInfo(ctx, instance.BaseURL)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	report.Reachable = true
	report.Version = info.Version
	if !versionSupported(info.Version) {
		report.Error = fmt.Sprintf(
			"remote version %s is below the minimum supported %s", info.Version, MinRemoteVersion)
		return report
	}
	report.Supported = true
	return report
}

// remoteSystemInfo is the version-bearing subset of the remote
// GET /api/v1/system/info response.
type remoteSystemInfo struct {
	Version string `json:"version"`
}

func (p *HealthPoller) fetchSystemInfo(ctx context.Context, baseURL string) (remoteSystemInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(baseURL, "/")+systemInfoPath, nil)
	if err != nil {
		return remoteSystemInfo{}, fmt.Errorf("build system info request: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return remoteSystemInfo{}, fmt.Errorf("request remote system info: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return remoteSystemInfo{}, fmt.Errorf("system info returned status %d", resp.StatusCode)
	}
	var info remoteSystemInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxSystemInfoBodyBytes)).Decode(&info); err != nil {
		return remoteSystemInfo{}, fmt.Errorf("decode system info response: %w", err)
	}
	return info, nil
}

// versionSupported reports whether a version reported by a remote meets
// MinRemoteVersion. Parsing follows the shapes the backend actually stamps
// into system/info (git describe): an optional "v" prefix, a
// major.minor.patch triple, and describe or prerelease suffixes ("-3-g<sha>",
// "-dirty") that only ever describe commits on top of the tagged triple.
// Anything unparseable ("dev") fails closed: an unverifiable remote counts
// as unsupported.
func versionSupported(reported string) bool {
	remote, ok := parseVersionTriple(reported)
	if !ok {
		return false
	}
	floor, ok := parseVersionTriple(MinRemoteVersion)
	return ok && !versionLess(remote, floor)
}

func parseVersionTriple(version string) ([3]int, bool) {
	var triple [3]int
	version = strings.TrimPrefix(version, "v")
	if i := strings.IndexByte(version, '-'); i >= 0 {
		version = version[:i]
	}
	parts := strings.Split(version, ".")
	if len(parts) != len(triple) {
		return triple, false
	}
	for i, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil {
			return triple, false
		}
		triple[i] = number
	}
	return triple, true
}

func versionLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// healthProber adapts the poller to the healthpoll.Prober contract, the
// same seam internal/jira uses for its auth-health loop.
type healthProber struct {
	poller *HealthPoller
}

// HasConfig reports whether any remote instance is registered; an empty
// registry means there is nothing worth probing.
func (h healthProber) HasConfig(ctx context.Context) (bool, error) {
	registered, err := h.poller.service.repo.ListAllInstances(ctx)
	if err != nil {
		return false, err
	}
	return len(registered) > 0, nil
}

// RecordAuthHealth runs one probe pass over every registered instance.
func (h healthProber) RecordAuthHealth(ctx context.Context) {
	h.poller.probeAll(ctx)
}
