package backendapp

import (
	"context"
	"fmt"

	"github.com/kandev/kandev/internal/common/config"
	"github.com/kandev/kandev/internal/db"
	"github.com/kandev/kandev/internal/statsync"
	taskservice "github.com/kandev/kandev/internal/task/service"
)

// initStatsyncWiring builds the cross-instance statistics ingest service.
// The mirror schema itself initializes unconditionally in storage.go (the
// tables are part of the database contract); the service — and therefore the
// route — exists only when statsync.ingestSecret is configured.
func initStatsyncWiring(
	cfg *config.Config,
	dbPool *db.Pool,
	repos *Repositories,
	taskSvc *taskservice.Service,
) (*statsync.Service, error) {
	if repos.Statsync == nil {
		return nil, fmt.Errorf("statsync repository was not initialized")
	}
	if cfg.Statsync.IngestSecret == "" {
		return nil, nil
	}
	// v1 default mapping: sources without an explicit workspace mapping are
	// filed under the instance's primary (first) workspace, so the merged
	// statistics stay complete without per-source configuration.
	defaultWorkspace := func(ctx context.Context) (string, error) {
		workspaces, err := taskSvc.ListWorkspaces(ctx)
		if err != nil {
			return "", err
		}
		if len(workspaces) == 0 {
			return "", nil
		}
		return workspaces[0].ID, nil
	}
	return statsync.NewService(
		repos.Statsync,
		dbPool.Reader(),
		cfg.Statsync.IngestSecret,
		cfg.Statsync.WorkspaceMappings,
		defaultWorkspace,
	), nil
}
