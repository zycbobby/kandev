package process

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
)

// creationBaseFixture builds the "pushed 39" shape: origin/main holds only
// the baseline, local main is 3 commits ahead, and a work branch is cut from
// the local main tip with one commit of its own.
type creationBaseFixture struct {
	dir        string
	cleanup    func()
	originMain string
	forkSHA    string
	workTip    string
	tracker    *WorkspaceTracker
}

func commitFile(t *testing.T, dir, name, content, message string) {
	t.Helper()
	writeFile(t, dir, name, content)
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", message)
}

func newCreationBaseFixture(t *testing.T) *creationBaseFixture {
	t.Helper()
	dir, cleanup := setupTestRepo(t)
	originMain := strings.TrimSpace(runGit(t, dir, "rev-parse", "origin/main"))
	for i := 1; i <= 3; i++ {
		commitFile(t, dir, fmt.Sprintf("mother-%d.txt", i), fmt.Sprintf("mother %d\n", i), fmt.Sprintf("mother %d", i))
	}
	forkSHA := strings.TrimSpace(runGit(t, dir, "rev-parse", "main"))
	runGit(t, dir, "checkout", "-b", "feature/work")
	commitFile(t, dir, "work.txt", "work one\n", "work one")
	workTip := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))
	return &creationBaseFixture{
		dir:        dir,
		cleanup:    cleanup,
		originMain: originMain,
		forkSHA:    forkSHA,
		workTip:    workTip,
		tracker:    NewWorkspaceTracker(dir, newTestLogger(t)),
	}
}

func (f *creationBaseFixture) creationBase() models.CreationBase {
	return models.CreationBase{Version: models.CreationBaseVersion, Ref: "main", ForkSHA: f.forkSHA}
}

func (f *creationBaseFixture) revParse(t *testing.T, ref string) string {
	t.Helper()
	return strings.TrimSpace(runGit(t, f.dir, "rev-parse", ref))
}

// TestResolveCreationBaseAnchor_Matrix covers the D5 resolution contract on
// a real repository: verbatim local merge-base, stable anchor after the
// mother advances, range shrink after rebase, fork fallback when the mother
// is deleted or rewritten, explicit unavailability when the fork point is
// gone, and remote-only refs resolving as recorded.
func TestResolveCreationBaseAnchor_Matrix(t *testing.T) {
	t.Run("mother ahead of origin anchors at the fork point", func(t *testing.T) {
		f := newCreationBaseFixture(t)
		defer f.cleanup()
		f.tracker.SetCreationBase(&models.CreationBase{Version: models.CreationBaseVersion, Ref: "main", ForkSHA: f.forkSHA})

		sha, code := f.tracker.ResolveCreationBaseAnchor(context.Background())
		if code != "" || sha != f.forkSHA {
			t.Fatalf("anchor = (%q, %q), want (%q, \"\")", sha, code, f.forkSHA)
		}
	})

	t.Run("mother advancing keeps the anchor stable", func(t *testing.T) {
		f := newCreationBaseFixture(t)
		defer f.cleanup()
		commitFile(t, f.dir, "mother-late.txt", "late\n", "mother late")

		f.tracker.SetCreationBase(&models.CreationBase{Version: models.CreationBaseVersion, Ref: "main", ForkSHA: f.forkSHA})
		sha, code := f.tracker.ResolveCreationBaseAnchor(context.Background())
		if code != "" || sha != f.forkSHA {
			t.Fatalf("anchor = (%q, %q), want the stable fork point (%q, \"\")", sha, code, f.forkSHA)
		}
	})

	t.Run("rebase onto the new mother tip shrinks the range", func(t *testing.T) {
		f := newCreationBaseFixture(t)
		defer f.cleanup()
		commitFile(t, f.dir, "mother-late.txt", "late\n", "mother late")
		newTip := f.revParse(t, "main")
		runGit(t, f.dir, "rebase", "main")

		f.tracker.SetCreationBase(&models.CreationBase{Version: models.CreationBaseVersion, Ref: "main", ForkSHA: f.forkSHA})
		sha, code := f.tracker.ResolveCreationBaseAnchor(context.Background())
		if code != "" || sha != newTip {
			t.Fatalf("anchor = (%q, %q), want the rebased base (%q, \"\")", sha, code, newTip)
		}
	})

	t.Run("deleted mother falls back to the fork SHA", func(t *testing.T) {
		f := newCreationBaseFixture(t)
		defer f.cleanup()
		runGit(t, f.dir, "branch", "-D", "main")

		f.tracker.SetCreationBase(&models.CreationBase{Version: models.CreationBaseVersion, Ref: "main", ForkSHA: f.forkSHA})
		sha, code := f.tracker.ResolveCreationBaseAnchor(context.Background())
		if code != "" || sha != f.forkSHA {
			t.Fatalf("anchor = (%q, %q), want the fork fallback (%q, \"\")", sha, code, f.forkSHA)
		}
	})

	t.Run("rewritten mother whose merge-base regressed falls back to the fork SHA", func(t *testing.T) {
		f := newCreationBaseFixture(t)
		defer f.cleanup()
		// Reset the mother to the pushed baseline: the live merge-base is now
		// strictly behind the recorded fork point.
		runGit(t, f.dir, "branch", "-f", "main", f.originMain)

		f.tracker.SetCreationBase(&models.CreationBase{Version: models.CreationBaseVersion, Ref: "main", ForkSHA: f.forkSHA})
		sha, code := f.tracker.ResolveCreationBaseAnchor(context.Background())
		if code != "" || sha != f.forkSHA {
			t.Fatalf("anchor = (%q, %q), want the fork fallback (%q, \"\")", sha, code, f.forkSHA)
		}
	})

	t.Run("unresolvable fork point reports the bounded unavailable code", func(t *testing.T) {
		f := newCreationBaseFixture(t)
		defer f.cleanup()
		runGit(t, f.dir, "branch", "-D", "main")
		f.tracker.SetCreationBase(&models.CreationBase{Version: models.CreationBaseVersion, Ref: "main", ForkSHA: strings.Repeat("d", 40)})

		sha, code := f.tracker.ResolveCreationBaseAnchor(context.Background())
		if sha != "" || code != creationBaseErrorRefUnavailable {
			t.Fatalf("anchor = (%q, %q), want (\"\", %q)", sha, code, creationBaseErrorRefUnavailable)
		}
	})

	t.Run("remote-only ref resolves verbatim", func(t *testing.T) {
		f := newCreationBaseFixture(t)
		defer f.cleanup()
		// Push a develop branch that only exists on the remote side.
		runGit(t, f.dir, "push", "origin", "main:refs/heads/develop")
		remoteTip := f.revParse(t, "origin/develop")
		f.tracker.SetCreationBase(&models.CreationBase{Version: models.CreationBaseVersion, Ref: "origin/develop", ForkSHA: f.forkSHA})

		sha, code := f.tracker.ResolveCreationBaseAnchor(context.Background())
		if code != "" || sha != remoteTip {
			t.Fatalf("anchor = (%q, %q), want the remote-tracking tip (%q, \"\")", sha, code, remoteTip)
		}
	})

	t.Run("no recorded base resolves nothing", func(t *testing.T) {
		f := newCreationBaseFixture(t)
		defer f.cleanup()
		sha, code := f.tracker.ResolveCreationBaseAnchor(context.Background())
		if sha != "" || code != "" {
			t.Fatalf("anchor = (%q, %q), want empty without a recorded base", sha, code)
		}
	})
}

// TestResolveBaseCommitFlipsFromOriginFirstToCreationBase reproduces the
// "pushed 39" symptom and its reversal: without a creation base the anchor
// follows origin/main (the origin-first default), with one it follows the
// recorded fork point so only this worktree's own work is compared.
func TestResolveBaseCommitFlipsFromOriginFirstToCreationBase(t *testing.T) {
	f := newCreationBaseFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	if got := f.tracker.ResolveBaseCommit(ctx); got != f.originMain {
		t.Fatalf("origin-first anchor = %q, want the pushed baseline %q", got, f.originMain)
	}

	base := f.creationBase()
	f.tracker.SetCreationBase(&base)
	if got := f.tracker.ResolveBaseCommit(ctx); got != f.forkSHA {
		t.Fatalf("creation-base anchor = %q, want the fork point %q", got, f.forkSHA)
	}

	// The ahead/behind counts use the same anchor.
	ref, code := f.tracker.resolveAheadBehindCompareRef(ctx)
	if code != "" || ref != f.forkSHA {
		t.Fatalf("ahead/behind compare ref = (%q, %q), want the fork point", ref, code)
	}
}

// TestResolveBaseCommitPrefersExplicitComparisonTarget pins the priority:
// an explicit (even not-yet-ready) target outranks the creation base, which
// never overrides it.
func TestResolveBaseCommitPrefersExplicitComparisonTarget(t *testing.T) {
	f := newCreationBaseFixture(t)
	defer f.cleanup()
	ctx := context.Background()

	base := f.creationBase()
	f.tracker.SetCreationBase(&base)
	target := comparisonTargetProcessTestTarget()
	f.tracker.SetComparisonTarget(&target)

	if got := f.tracker.ResolveBaseCommit(ctx); got == f.forkSHA {
		t.Fatalf("explicit target did not outrank the creation base: anchor = %q", got)
	}
}

// TestResolveBaseAnchorReturnsMotherRefForCreationBase covers the multi-repo
// contract: the anchor comes with the recorded mother ref so callers can
// display it, and CreationBaseResolution marks the result as fail-closed (no
// stale re-anchoring).
func TestResolveBaseAnchorReturnsMotherRefForCreationBase(t *testing.T) {
	f := newCreationBaseFixture(t)
	defer f.cleanup()
	base := f.creationBase()
	f.tracker.SetCreationBase(&base)

	sha, ref := f.tracker.ResolveBaseAnchor(context.Background())
	if sha != f.forkSHA || ref != "main" {
		t.Fatalf("anchor = (%q, %q), want (%q, \"main\")", sha, ref, f.forkSHA)
	}
	if _, present := f.tracker.CreationBaseResolution(); !present {
		t.Fatal("creation base marker vanished after resolution")
	}
}

// TestCreationBaseUnavailableKeepsCountsExplicitAndStatusReady covers the
// fail-closed contract for ahead/behind: an unresolvable anchor yields the
// bounded error code, not a silent zero or an origin-first fallback.
func TestCreationBaseUnavailableKeepsCountsExplicitAndStatusReady(t *testing.T) {
	f := newCreationBaseFixture(t)
	defer f.cleanup()
	runGit(t, f.dir, "branch", "-D", "main")
	f.tracker.SetCreationBase(&models.CreationBase{Version: models.CreationBaseVersion, Ref: "main", ForkSHA: strings.Repeat("d", 40)})

	ref, code := f.tracker.resolveAheadBehindCompareRef(context.Background())
	if ref != "" || code != creationBaseErrorRefUnavailable {
		t.Fatalf("compare ref = (%q, %q), want the bounded unavailable code", ref, code)
	}
}
