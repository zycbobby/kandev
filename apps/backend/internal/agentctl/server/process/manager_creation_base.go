package process

import (
	"context"

	"github.com/kandev/kandev/internal/task/models"
)

// getCreationBases returns a snapshot of cfg.CreationBases so stamp loops and
// spawn-time lookups read a consistent map while a concurrent
// UpdateCreationBases writer publishes a replacement.
func (m *Manager) getCreationBases() map[string]models.CreationBase {
	m.creationBasesMu.RLock()
	defer m.creationBasesMu.RUnlock()
	if m.cfg == nil || len(m.cfg.CreationBases) == 0 {
		return nil
	}
	out := make(map[string]models.CreationBase, len(m.cfg.CreationBases))
	for key, base := range m.cfg.CreationBases {
		out[key] = base
	}
	return out
}

// setCreationBases replaces cfg.CreationBases under creationBasesMu. An empty
// map clears the stored defaults so the next stamp removes every tracker's
// creation base.
func (m *Manager) setCreationBases(bases map[string]models.CreationBase) {
	m.creationBasesMu.Lock()
	defer m.creationBasesMu.Unlock()
	if m.cfg == nil {
		return
	}
	if len(bases) == 0 {
		m.cfg.CreationBases = nil
		return
	}
	replacement := make(map[string]models.CreationBase, len(bases))
	for key, base := range bases {
		replacement[key] = base
	}
	m.cfg.CreationBases = replacement
}

// lookupCreationBase reads the recorded creation base for a repository name
// from the per-instance map. The empty key "" addresses the single-repo /
// root tracker; a named repo falls back to it when it has no entry of its own,
// preserving single-repo behavior for tasks that record only the root slot.
func lookupCreationBase(bases map[string]models.CreationBase, repoName string) *models.CreationBase {
	if len(bases) == 0 {
		return nil
	}
	if base, ok := bases[repoName]; ok {
		return &base
	}
	if repoName != "" {
		if base, ok := bases[""]; ok {
			return &base
		}
	}
	return nil
}

// UpdateCreationBases replaces the per-repo creation-base map and re-stamps
// every active tracker with the value for its key, then triggers a
// non-blocking RefreshGitStatus on each so the UI sees the new comparison
// base without waiting for the next poll tick. Pushing an empty map clears
// every tracker's creation base, restoring the origin-first default.
//
// Newly-spawned trackers (rescan path, lazy subpath lookup) read the updated
// map via lookupCreationBase — no second push needed for them.
func (m *Manager) UpdateCreationBases(ctx context.Context, bases map[string]models.CreationBase) {
	m.setCreationBases(bases)
	current := m.getCreationBases()

	m.repoTrackersMu.RLock()
	root := m.workspaceTracker
	trackers := make([]*WorkspaceTracker, len(m.repoTrackers))
	copy(trackers, m.repoTrackers)
	m.repoTrackersMu.RUnlock()

	m.workspaceTrackersMu.Lock()
	bySubpath := make(map[string]*WorkspaceTracker, len(m.workspaceTrackersBySubpath))
	for k, v := range m.workspaceTrackersBySubpath {
		bySubpath[k] = v
	}
	m.workspaceTrackersMu.Unlock()

	// Stamp synchronously so the field is visible to the next poll; the git
	// refreshes run detached for the same reason as UpdateBaseBranches.
	if root != nil {
		root.SetCreationBase(lookupCreationBase(current, root.RepositoryName()))
	}
	for _, t := range trackers {
		t.SetCreationBase(lookupCreationBase(current, t.RepositoryName()))
	}
	for subpath, t := range bySubpath {
		t.SetCreationBase(lookupCreationBase(current, subpath))
	}
	go m.refreshTrackersDetached(root, trackers, bySubpath)
}
