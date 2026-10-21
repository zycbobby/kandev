package process

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/agentctl/server/config"
	"github.com/kandev/kandev/internal/task/models"
)

func creationBaseForManagerTest(ref string) models.CreationBase {
	return models.CreationBase{Version: models.CreationBaseVersion, Ref: ref, ForkSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
}

// TestManagerUpdateCreationBases_StampsRootTracker confirms a single-repo
// task's root tracker receives the creation base addressed by the legacy
// empty key, and that a cleared map removes it again.
func TestManagerUpdateCreationBases_StampsRootTracker(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	mgr := NewManager(&config.InstanceConfig{WorkDir: repoDir}, newTestLogger(t))
	root := mgr.GetWorkspaceTracker()
	if root == nil {
		t.Fatal("expected root tracker for single-repo workspace")
	}
	if _, present := root.CreationBaseResolution(); present {
		t.Fatal("precondition: root tracker starts without a creation base")
	}

	base := creationBaseForManagerTest("main")
	mgr.UpdateCreationBases(context.Background(), map[string]models.CreationBase{"": base})

	got, present := root.CreationBaseResolution()
	if !present || !got.Equal(base) {
		t.Fatalf("root tracker creation base = %#v present=%v, want %#v", got, present, base)
	}

	mgr.UpdateCreationBases(context.Background(), nil)
	if _, present := root.CreationBaseResolution(); present {
		t.Fatal("creation base survived an empty-map push")
	}
}

// TestManagerUpdateCreationBases_PersistsToCfgAndSubpathKeys verifies the
// replacement map is stored on cfg for later tracker spawns and that
// subpath-keyed entries and the empty-key fallback resolve through
// lookupCreationBase.
func TestManagerUpdateCreationBases_PersistsToCfgAndSubpathKeys(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	mgr := NewManager(&config.InstanceConfig{WorkDir: repoDir}, newTestLogger(t))
	named := creationBaseForManagerTest("develop")
	root := creationBaseForManagerTest("main")
	mgr.UpdateCreationBases(context.Background(), map[string]models.CreationBase{
		"widget": named,
		"":       root,
	})

	bases := mgr.getCreationBases()
	if got, ok := bases["widget"]; !ok || !got.Equal(named) {
		t.Fatalf("getCreationBases()[widget] = %#v ok=%v, want the named base", got, ok)
	}
	if got, ok := bases[""]; !ok || !got.Equal(root) {
		t.Fatalf("getCreationBases()[\"\"] = %#v ok=%v, want the root base", got, ok)
	}

	if got := lookupCreationBase(bases, "widget"); got == nil || !got.Equal(named) {
		t.Fatalf("lookupCreationBase(widget) = %#v, want the named base", got)
	}
	if got := lookupCreationBase(bases, "other-repo"); got == nil || !got.Equal(root) {
		t.Fatalf("lookupCreationBase(other-repo) = %#v, want the empty-key fallback", got)
	}
	if got := lookupCreationBase(nil, "widget"); got != nil {
		t.Fatalf("lookupCreationBase(nil) = %#v, want nil", got)
	}
}

// TestManagerUpdateCreationBases_ReplacesMap covers the live-update contract:
// a second push replaces the stored map wholesale, so a key removed by the
// backend (priority decision on the kandev side) disappears from the agentctl
// map even when other keys survive.
func TestManagerUpdateCreationBases_ReplacesMap(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()

	mgr := NewManager(&config.InstanceConfig{WorkDir: repoDir}, newTestLogger(t))
	first := creationBaseForManagerTest("main")
	mgr.UpdateCreationBases(context.Background(), map[string]models.CreationBase{"": first})
	second := creationBaseForManagerTest("develop")
	mgr.UpdateCreationBases(context.Background(), map[string]models.CreationBase{"widget": second})

	bases := mgr.getCreationBases()
	if len(bases) != 1 {
		t.Fatalf("stored bases = %v, want only the surviving key", bases)
	}
	if got, ok := bases["widget"]; !ok || !got.Equal(second) {
		t.Fatalf("surviving entry = %#v ok=%v, want the second base", got, ok)
	}
}
