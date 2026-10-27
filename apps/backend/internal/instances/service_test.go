package instances_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/instances"
	instancestore "github.com/kandev/kandev/internal/instances/store/sqlite"
	"github.com/kandev/kandev/internal/profiles"
)

// newTestService builds the service over a real SQLite store in a temp
// directory, with the feature gate fixed to the supplied value.
func newTestService(t *testing.T, enabled bool) (*instances.Service, *sqlx.DB) {
	t.Helper()
	raw, err := db.OpenSQLite(filepath.Join(t.TempDir(), "instances.db"))
	require.NoError(t, err)
	database := sqlx.NewDb(raw, "sqlite3")
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	repo, cleanup, err := instancestore.Provide(context.Background(), database, database)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cleanup()) })
	return instances.NewService(repo, func() bool { return enabled }), database
}

func TestCreateListUpdateDeleteInstanceOwnedByCaller(t *testing.T) {
	svc, _ := newTestService(t, true)
	ctx := context.Background()

	created, err := svc.CreateInstance(ctx, "owner-a", "Laptop 3090Ti",
		"https://kandev-remote.example.test", "secret-1", true)
	require.NoError(t, err)
	require.NotEmpty(t, created.ID)
	require.Equal(t, "owner-a", created.OwnerUserID)
	require.Equal(t, "Laptop 3090Ti", created.Name)
	require.Equal(t, "https://kandev-remote.example.test", created.BaseURL)
	require.Equal(t, "secret-1", created.CredentialSecretID)
	require.True(t, created.Enabled)
	require.Zero(t, created.ProxyPort)
	require.False(t, created.CreatedAt.IsZero())
	require.False(t, created.UpdatedAt.Before(created.CreatedAt))

	listed, err := svc.ListInstances(ctx, "owner-a")
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, created.ID, listed[0].ID)

	name := "Workstation"
	baseURL := "https://kandev-workstation.example.test"
	credentialSecretID := "secret-2"
	enabled := false
	proxyPort := 39001
	updated, err := svc.UpdateInstance(ctx, "owner-a", created.ID, instances.InstanceUpdate{
		Name: &name, BaseURL: &baseURL, CredentialSecretID: &credentialSecretID,
		Enabled: &enabled, ProxyPort: &proxyPort,
	})
	require.NoError(t, err)
	require.Equal(t, name, updated.Name)
	require.Equal(t, baseURL, updated.BaseURL)
	require.Equal(t, credentialSecretID, updated.CredentialSecretID)
	require.False(t, updated.Enabled)
	require.Equal(t, proxyPort, updated.ProxyPort)
	require.False(t, updated.UpdatedAt.Before(created.UpdatedAt))

	persisted, err := svc.ListInstances(ctx, "owner-a")
	require.NoError(t, err)
	require.Len(t, persisted, 1)
	require.Equal(t, name, persisted[0].Name)
	require.Equal(t, proxyPort, persisted[0].ProxyPort)

	require.NoError(t, svc.DeleteInstance(ctx, "owner-a", created.ID))
	remaining, err := svc.ListInstances(ctx, "owner-a")
	require.NoError(t, err)
	require.Empty(t, remaining)
}

func TestInstanceInvisibleToOtherOwner(t *testing.T) {
	svc, _ := newTestService(t, true)
	ctx := context.Background()

	created, err := svc.CreateInstance(ctx, "owner-a", "Remote",
		"https://kandev-remote.example.test", "", true)
	require.NoError(t, err)

	otherList, err := svc.ListInstances(ctx, "owner-b")
	require.NoError(t, err)
	require.Empty(t, otherList)

	name := "Hijacked"
	_, err = svc.UpdateInstance(ctx, "owner-b", created.ID, instances.InstanceUpdate{Name: &name})
	require.ErrorIs(t, err, instances.ErrInstanceNotFound)

	require.ErrorIs(t, svc.DeleteInstance(ctx, "owner-b", created.ID), instances.ErrInstanceNotFound)

	// A rejected hijack must leave the owning row untouched.
	stillThere, err := svc.ListInstances(ctx, "owner-a")
	require.NoError(t, err)
	require.Len(t, stillThere, 1)
	require.Equal(t, "Remote", stillThere[0].Name)
}

func TestDefaultFlagKeepsRegistryInert(t *testing.T) {
	defaults, err := profiles.FeatureFlagDefaults()
	require.NoError(t, err)
	require.Equal(t, "false", defaults["multi_instance"],
		"features.multiInstance must ship off in every profile")

	svc, database := newTestService(t, defaults["multi_instance"] == "true")
	ctx := context.Background()

	_, err = svc.CreateInstance(ctx, "owner-a", "Remote", "https://kandev-remote.example.test", "", true)
	require.ErrorIs(t, err, instances.ErrMultiInstanceDisabled)
	_, err = svc.ListInstances(ctx, "owner-a")
	require.ErrorIs(t, err, instances.ErrMultiInstanceDisabled)
	_, err = svc.UpdateInstance(ctx, "owner-a", "missing", instances.InstanceUpdate{})
	require.ErrorIs(t, err, instances.ErrMultiInstanceDisabled)
	require.ErrorIs(t, svc.DeleteInstance(ctx, "owner-a", "missing"), instances.ErrMultiInstanceDisabled)

	var count int
	require.NoError(t, database.Get(&count, `SELECT COUNT(*) FROM instances`))
	require.Zero(t, count, "the disabled registry must persist nothing")
}
