package process

import (
	"github.com/kandev/kandev/internal/task/models"
)

// SetCreationBase installs or clears the worktree creation base recorded for
// this tracker. Unlike a comparison target there is no materialization
// lifecycle: the base is a local ref, so resolution happens lazily at
// comparison time and a cleared base simply restores the ordinary
// origin-first default.
func (wt *WorkspaceTracker) SetCreationBase(base *models.CreationBase) {
	wt.mu.Lock()
	defer wt.mu.Unlock()
	if base == nil {
		wt.creationBase = nil
		return
	}
	stored := *base
	wt.creationBase = &stored
}

// CreationBaseResolution returns a race-safe snapshot of the recorded
// creation base. present=false means no default is installed.
func (wt *WorkspaceTracker) CreationBaseResolution() (models.CreationBase, bool) {
	wt.mu.RLock()
	defer wt.mu.RUnlock()
	if wt.creationBase == nil {
		return models.CreationBase{}, false
	}
	return *wt.creationBase, true
}
