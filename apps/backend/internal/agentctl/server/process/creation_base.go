package process

import (
	"context"
	"strings"

	"github.com/kandev/kandev/internal/task/models"
)

// creationBaseErrorRefUnavailable is the bounded code published when a
// recorded creation base cannot resolve any anchor: the recorded fork point
// is no longer resolvable in this repository. Comparison-derived data must
// surface it explicitly rather than fall back to an integration default.
const creationBaseErrorRefUnavailable = "creation_base_ref_unavailable"

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

// SetCreationBaseIfNotSubmodule stamps the creation base like
// SetBaseBranchIfNotSubmodule: a tracker pinned to a submodule gitlink anchor
// never takes a task-repository default.
func (wt *WorkspaceTracker) SetCreationBaseIfNotSubmodule(base *models.CreationBase) bool {
	wt.mu.Lock()
	defer wt.mu.Unlock()
	if wt.comparisonAnchorSet {
		return false
	}
	if base == nil {
		wt.creationBase = nil
		return true
	}
	stored := *base
	wt.creationBase = &stored
	return true
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

// ResolveCreationBaseAnchor computes this tracker's comparison anchor from
// the recorded creation base:
//
//	anchor = merge-base(HEAD, Ref), resolved against the local ref verbatim
//	Ref unresolvable, or the merge-base strictly behind ForkSHA (the mother
//	branch was rewritten) → anchor = ForkSHA
//	the anchor itself unresolvable → creationBaseErrorRefUnavailable
//
// Unlike the branch-only path it never rewrites a local name to origin/ and
// never re-anchors to an integration default (no CorrectStaleComparisonBase):
// a recorded base fails closed. Both ref and SHA are re-validated inline so
// the taint barrier sits in the same function as the git invocations.
func (wt *WorkspaceTracker) ResolveCreationBaseAnchor(ctx context.Context) (sha, errorCode string) {
	base, present := wt.CreationBaseResolution()
	if !present {
		return "", ""
	}
	anchor := wt.creationBaseMergeBase(ctx, base)
	if anchor != "" && anchor != base.ForkSHA && wt.commitIsStrictAncestor(ctx, anchor, base.ForkSHA) {
		anchor = ""
	}
	if anchor == "" {
		anchor = base.ForkSHA
	}
	if !gitObjectIDPattern.MatchString(anchor) {
		return "", creationBaseErrorRefUnavailable
	}
	if err := wt.runGit(ctx, "rev-parse", "--verify", anchor+"^{commit}"); err != nil {
		return "", creationBaseErrorRefUnavailable
	}
	return anchor, ""
}

// creationBaseMergeBase resolves merge-base(HEAD, Ref) with the ref used
// verbatim. Invalid or unresolvable refs yield "" so the caller falls back
// to the recorded fork SHA.
func (wt *WorkspaceTracker) creationBaseMergeBase(ctx context.Context, base models.CreationBase) string {
	rest, hasOriginPrefix := strings.CutPrefix(base.Ref, "origin/")
	check := base.Ref
	if hasOriginPrefix {
		check = rest
	}
	if !safeBranchRefPattern.MatchString(check) || strings.Contains(check, "..") || strings.HasSuffix(check, ".lock") {
		return ""
	}
	out, err := wt.runGitOutput(ctx, "merge-base", "HEAD", base.Ref)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// commitIsStrictAncestor reports whether ancestor is an ancestor of
// descendant, using the merge-base identity check shared with
// correctStaleComparisonBase.
func (wt *WorkspaceTracker) commitIsStrictAncestor(ctx context.Context, ancestor, descendant string) bool {
	if !gitObjectIDPattern.MatchString(ancestor) || !gitObjectIDPattern.MatchString(descendant) {
		return false
	}
	out, err := wt.runGitOutput(ctx, "merge-base", ancestor, descendant)
	return err == nil && strings.TrimSpace(string(out)) == ancestor
}
