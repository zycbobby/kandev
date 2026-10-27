package backendapp

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/common/config"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/instances"
	instancestore "github.com/kandev/kandev/internal/instances/store/sqlite"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestInstanceProxyAssemblyReconcilesServiceWrites(t *testing.T) {
	raw, err := db.OpenSQLite(filepath.Join(t.TempDir(), "instances.db"))
	require.NoError(t, err)
	database := sqlx.NewDb(raw, "sqlite3")
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	repo, cleanup, err := instancestore.Provide(t.Context(), database, database)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cleanup()) })
	log, err := logger.NewFromZap(zap.NewNop())
	require.NoError(t, err)
	cfg := &config.Config{}
	services := &Services{}
	stop := initInstanceProxies(t.Context(), cfg, &Repositories{Instances: repo}, services, log)
	t.Cleanup(stop)
	require.Nil(t, services.InstanceProxies)
	require.Nil(t, services.Instances)
	// A row present at boot must start without requiring a subsequent write.
	existing := &instances.Instance{OwnerUserID: "owner", Name: "existing", BaseURL: "http://localhost:1", Enabled: true}
	require.NoError(t, repo.CreateInstance(t.Context(), existing))
	cfg.Features.MultiInstance = true
	stop = initInstanceProxies(t.Context(), cfg, &Repositories{Instances: repo}, services, log)
	t.Cleanup(stop)
	require.NotNil(t, services.InstanceProxies.Server(existing.ID))
	item, err := services.Instances.CreateInstance(t.Context(), "owner", "remote", "http://localhost:1", "", true)
	require.NoError(t, err)
	server := services.InstanceProxies.Server(item.ID)
	require.NotNil(t, server)
	stored, err := repo.GetInstance(t.Context(), "owner", item.ID)
	require.NoError(t, err)
	require.Equal(t, server.Port(), stored.ProxyPort)
	port := server.Port()
	disabled := false
	_, err = services.Instances.UpdateInstance(t.Context(), "owner", item.ID, instances.InstanceUpdate{Enabled: &disabled})
	require.NoError(t, err)
	require.Nil(t, services.InstanceProxies.Server(item.ID))
	client := &http.Client{}
	res, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/healthz")
	if res != nil {
		require.NoError(t, res.Body.Close())
	}
	require.Error(t, err)
	enabled := true
	_, err = services.Instances.UpdateInstance(t.Context(), "owner", item.ID, instances.InstanceUpdate{Enabled: &enabled})
	require.NoError(t, err)
	require.NotNil(t, services.InstanceProxies.Server(item.ID))
	require.NoError(t, services.Instances.DeleteInstance(context.Background(), "owner", item.ID))
	require.Nil(t, services.InstanceProxies.Server(item.ID))
	stop()
	require.Nil(t, services.InstanceProxies.Server(existing.ID))
}
