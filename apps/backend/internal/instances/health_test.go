package instances_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/instances"
)

// TestMain asserts that no goroutine from this package outlives the test
// binary. The health poller owns a background probe loop (delegated to
// internal/integrations/healthpoll) whose Start/Stop lifecycle must drain
// every goroutine it spawns.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// remoteSystemInfo mirrors the full GET /api/v1/system/info response shape
// (internal/system/info Response) so fake remotes report versions exactly
// the way real ones do.
type remoteSystemInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	BootID    string `json:"boot_id"`
	StartedAt string `json:"started_at"`
}

// newRemoteInstanceServer serves a fake remote backend whose
// /api/v1/system/info reports the supplied version.
func newRemoteInstanceServer(t *testing.T, version string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/system/info" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(remoteSystemInfo{
			Version:   version,
			Commit:    "0123456789abcdef",
			BuildTime: "2026-10-27T00:00:00Z",
			GoVersion: "go1.26.0",
			OS:        "darwin",
			Arch:      "arm64",
			BootID:    "fake-boot-id",
			StartedAt: "2026-10-27T00:00:00Z",
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// startHealthPoller starts the probe loop and stops it when the test ends.
func startHealthPoller(t *testing.T, svc *instances.Service) *instances.HealthPoller {
	t.Helper()
	poller := instances.NewHealthPoller(svc, logger.Default())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	poller.Start(ctx)
	t.Cleanup(poller.Stop)
	return poller
}

// awaitFirstCheck blocks until the poller has recorded a first report for
// the instance, then returns it. The immediate probe pass on Start fills the
// report within milliseconds; the Eventually window only absorbs scheduler
// jitter, never a full poll interval.
func awaitFirstCheck(t *testing.T, svc *instances.Service, ownerUserID, instanceID string) instances.HealthReport {
	t.Helper()
	var report instances.HealthReport
	require.Eventually(t, func() bool {
		r, err := svc.GetHealth(context.Background(), ownerUserID, instanceID)
		if err != nil {
			return false
		}
		report = r
		return !r.LastCheckedAt.IsZero()
	}, 5*time.Second, 10*time.Millisecond)
	return report
}

func TestHealthPollerMarksReachableAndVersion(t *testing.T) {
	svc, _ := newTestService(t, true)
	ctx := context.Background()
	// Real remotes stamp the git-describe tag into system/info, so the
	// supported version carries the "v" prefix the comparison must strip.
	remote := newRemoteInstanceServer(t, "v"+instances.MinRemoteVersion)
	created, err := svc.CreateInstance(ctx, "owner-a", "Laptop 3090Ti", remote.URL, "", true)
	require.NoError(t, err)

	startHealthPoller(t, svc)

	report := awaitFirstCheck(t, svc, "owner-a", created.ID)
	require.True(t, report.Reachable)
	require.Equal(t, "v"+instances.MinRemoteVersion, report.Version)
	require.True(t, report.Supported)
	require.Empty(t, report.Error)
	require.False(t, report.LastCheckedAt.IsZero())

	// Health reads stay owner-scoped like every registry surface: a foreign
	// owner sees the instance exactly like a missing one.
	_, err = svc.GetHealth(ctx, "owner-b", created.ID)
	require.ErrorIs(t, err, instances.ErrInstanceNotFound)

	// The disabled registry keeps the health surface inert too.
	inert, _ := newTestService(t, false)
	_, err = inert.GetHealth(ctx, "owner-a", created.ID)
	require.ErrorIs(t, err, instances.ErrMultiInstanceDisabled)
}

func TestVersionBelowFloorMarksUnsupported(t *testing.T) {
	svc, _ := newTestService(t, true)
	ctx := context.Background()
	// v0.97.0 predates the release this federation ships in; the describe
	// suffix form is what a remote built between tags actually reports.
	tagged := newRemoteInstanceServer(t, "v0.97.0")
	taggedInstance, err := svc.CreateInstance(ctx, "owner-a", "Old remote", tagged.URL, "", true)
	require.NoError(t, err)
	suffixed := newRemoteInstanceServer(t, "v0.97.0-5-g1a2b3c4d5")
	suffixedInstance, err := svc.CreateInstance(ctx, "owner-a", "Mid remote", suffixed.URL, "", true)
	require.NoError(t, err)

	startHealthPoller(t, svc)

	remotes := []struct {
		id      string
		version string
	}{
		{id: taggedInstance.ID, version: "v0.97.0"},
		{id: suffixedInstance.ID, version: "v0.97.0-5-g1a2b3c4d5"},
	}
	for _, remote := range remotes {
		report := awaitFirstCheck(t, svc, "owner-a", remote.id)
		require.True(t, report.Reachable, remote.id)
		require.Equal(t, remote.version, report.Version, remote.id)
		require.False(t, report.Supported, remote.id)
		require.Contains(t, report.Error, instances.MinRemoteVersion, remote.id)
	}
}

func TestUnreachableInstanceDegradedAndIsolated(t *testing.T) {
	svc, _ := newTestService(t, true)
	ctx := context.Background()
	healthy := newRemoteInstanceServer(t, "v"+instances.MinRemoteVersion)
	alive, err := svc.CreateInstance(ctx, "owner-a", "Alive", healthy.URL, "", true)
	require.NoError(t, err)
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	offline, err := svc.CreateInstance(ctx, "owner-a", "Offline", deadURL, "", true)
	require.NoError(t, err)

	startHealthPoller(t, svc)

	report := awaitFirstCheck(t, svc, "owner-a", offline.ID)
	require.False(t, report.Reachable)
	require.False(t, report.Supported)
	require.Empty(t, report.Version)
	require.NotEmpty(t, report.Error)
	require.False(t, report.LastCheckedAt.IsZero())

	// Isolation: a degraded instance never poisons a healthy sibling probed
	// in the same pass.
	sibling := awaitFirstCheck(t, svc, "owner-a", alive.ID)
	require.True(t, sibling.Reachable)
	require.True(t, sibling.Supported)
	require.Equal(t, "v"+instances.MinRemoteVersion, sibling.Version)
	require.Empty(t, sibling.Error)
}
