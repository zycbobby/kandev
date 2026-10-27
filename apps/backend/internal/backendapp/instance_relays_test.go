package backendapp

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/kandev/kandev/internal/common/config"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/instances"
	instancestore "github.com/kandev/kandev/internal/instances/store/sqlite"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"go.uber.org/zap"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestRelayAssemblyAndNotificationRoutes(t *testing.T) {
	raw, err := db.OpenSQLite(filepath.Join(t.TempDir(), "relay.db"))
	require.NoError(t, err)
	database := sqlx.NewDb(raw, "sqlite3")
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	repo, err := instancestore.NewWithDB(t.Context(), database, database)
	require.NoError(t, err)
	log, err := logger.NewFromZap(zap.NewNop())
	require.NoError(t, err)
	cfg := &config.Config{}
	cfg.Features.MultiInstance = true
	services := &Services{}
	repos := &Repositories{Instances: repo}
	stopProxy := initInstanceProxies(t.Context(), cfg, repos, services, log)
	t.Cleanup(stopProxy)
	stop := initInstanceRelays(t.Context(), cfg, repos, services, nil, log)
	t.Cleanup(stop)
	item, err := services.Instances.CreateInstance(t.Context(), "owner", "remote", "http://localhost:1", "", true)
	require.NoError(t, err)
	require.Len(t, services.InstanceRelays.entries, 1)
	disabled := false
	_, err = services.Instances.UpdateInstance(t.Context(), "owner", item.ID, instances.InstanceUpdate{Enabled: &disabled})
	require.NoError(t, err)
	require.Empty(t, services.InstanceRelays.entries)
	_, err = repo.Record(t.Context(), "default-user", "remote", "session.turn_finished", "turn", "task", "session", "title", "body", time.Now().UTC())
	require.NoError(t, err)
	router := gin.New()
	registerRemoteNotificationRoutes(router, services)
	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest("GET", "/api/v1/remote-notifications", nil))
	require.Equal(t, 200, get.Code)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(get.Body.Bytes(), &rows))
	require.Len(t, rows, 1)
	for _, path := range []string{"/" + rows[0]["id"].(string) + "/read", "/" + rows[0]["id"].(string) + "/dismiss", "/read-all"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("POST", "/api/v1/remote-notifications"+path, nil))
		require.Equal(t, 200, response.Code)
	}
}

func TestRemoteNotificationsDisabledRoutesAbsent(t *testing.T) {
	router := gin.New()
	registerRemoteNotificationRoutes(router, &Services{})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/remote-notifications", nil))
	require.Equal(t, 404, response.Code)
	services := &Services{}
	stop := initInstanceRelays(t.Context(), &config.Config{}, nil, services, nil, nil)
	stop()
	require.Nil(t, services.InstanceRelays)
}
