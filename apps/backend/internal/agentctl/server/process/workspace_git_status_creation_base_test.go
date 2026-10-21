package process

import (
	"context"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
)

// TestGitStatusCaptureCarriesCreationBaseDisplay pins the display contract:
// a tracker with an installed creation base publishes the mother branch name
// on the basic status capture, mirroring the comparison-target display path.
func TestGitStatusCaptureCarriesCreationBaseDisplay(t *testing.T) {
	repoDir, cleanup := setupTestRepo(t)
	defer cleanup()
	// Local main ahead of origin reproduces the creation-base scenario.
	for i := 1; i <= 1; i++ {
		writeFile(t, repoDir, "mother.txt", "mother\n")
		runGit(t, repoDir, "add", ".")
		runGit(t, repoDir, "commit", "-m", "mother")
	}
	fork := strings.TrimSpace(runGit(t, repoDir, "rev-parse", "main"))
	runGit(t, repoDir, "checkout", "-b", "feature/capture")
	writeFile(t, repoDir, "own.txt", "own\n")
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "own")

	tracker := NewWorkspaceTracker(repoDir, newTestLogger(t))
	base := models.CreationBase{Version: models.CreationBaseVersion, Ref: "main", ForkSHA: fork}
	tracker.SetCreationBase(&base)

	capture, err := tracker.captureBasicGitStatus(context.Background())
	if err != nil {
		t.Fatalf("captureBasicGitStatus: %v", err)
	}
	if capture.status.CreationBase != "main" {
		t.Fatalf("captured status CreationBase = %q, want the mother branch name", capture.status.CreationBase)
	}
	if capture.status.CreationBaseErrorCode != "" {
		t.Fatalf("captured status CreationBaseErrorCode = %q, want empty", capture.status.CreationBaseErrorCode)
	}

	// An explicit comparison target outranks the creation-base display.
	target := comparisonTargetProcessTestTarget()
	tracker.SetComparisonTarget(&target)
	capture, err = tracker.captureBasicGitStatus(context.Background())
	if err != nil {
		t.Fatalf("captureBasicGitStatus with target: %v", err)
	}
	if capture.status.CreationBase != "" {
		t.Fatalf("explicit target must suppress the creation-base display, got %q", capture.status.CreationBase)
	}

	// Cleared base restores the empty display.
	tracker.SetComparisonTarget(nil)
	tracker.SetCreationBase(nil)
	capture, err = tracker.captureBasicGitStatus(context.Background())
	if err != nil {
		t.Fatalf("captureBasicGitStatus cleared: %v", err)
	}
	if capture.status.CreationBase != "" {
		t.Fatalf("cleared tracker must not display a creation base, got %q", capture.status.CreationBase)
	}
}
