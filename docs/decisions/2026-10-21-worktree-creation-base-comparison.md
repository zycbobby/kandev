# ADR-2026-10-21-worktree-creation-base-comparison: Worktree task workspaces compare against the recorded creation base

**Status:** accepted
**Date:** 2026-10-21
**Area:** backend

## Context

When a task workspace is materialized as a git worktree, every
comparison-derived surface — commit list, cumulative diff, comparison-derived
ahead/behind, native Review, and task summaries — defaulted its comparison
base to `merge-base(HEAD, origin/<base_branch>)`. That origin-first
resolution was a deliberate design position: `computeMergeBase` and
`resolveStoredRef` documented origin as the upstream source of truth, with
`CorrectStaleComparisonBase` re-anchoring stale bases to an integration
branch.

The position broke down for the common local-first workflow. When the mother
branch (the local ref the worktree branch was cut from) runs ahead of its
remote counterpart, the origin-first merge-base sits at the remote tip, so the
Changes panel attributes the mother branch's own unpushed commits to the
worktree. Users observed local `main` 39 commits ahead of `origin/main`
("pushed 39") with the panel mixing in those 39 commits — the panel stopped
answering "what did this worktree do".

The fork point (the commit the branch was cut from) is the only authoritative
anchor for "this worktree's own work", and it is unobservable after the fact:
the mother branch can advance, be rebased, or be deleted. Anchoring the
default therefore requires recording the fork point at materialization time.

## Decision

For task repositories materialized as worktrees, the comparison default base
is the **creation base**: the mother branch ref (recorded exactly as resolved
at materialization — a local base as its plain name, a remote-only base as
`origin/<name>`) plus the fork-point commit SHA that ref pointed at when the
worktree branch was cut. Resolution is local and fail-closed:

- The anchor is `merge-base(HEAD, Ref)` against the recorded ref verbatim; no
  origin-first rewriting of a local name.
- When the ref no longer resolves, or the live merge-base regresses strictly
  behind the recorded fork point (the mother branch was rewritten), the anchor
  is the recorded fork-point SHA.
- When the anchor itself cannot resolve, comparison-derived data reports the
  bounded code `creation_base_ref_unavailable` instead of falling back to an
  integration default. `CorrectStaleComparisonBase` is never applied to a
  creation-base anchor.

Priority is unchanged otherwise and resolves before the creation base:
explicit repository-qualified comparison targets (fork PR flow), then manual
"Compare against" picker selections (sticky; the backend excludes such rows
from the creation-base projection so a system refresh cannot replace them),
then the creation base, then the origin-first default for tasks without a
record (including all pre-existing tasks — no backfill, no migration).

The record is comparison-only. It changes no `origin`, checkout branch,
upstream tracking, push routing, worktree branch generation, or terminal
cleanup semantics; two worktrees may reference the same mother branch because
it is only ever a comparison argument.

Persistence is `task_repositories.metadata["creation_base"]`
(`models.CreationBase{Version, Ref, ForkSHA}`), mirroring the
`comparison_target` metadata pattern; no SQL migration. The fork point is
captured in `worktree.Manager.createNewBranchRef` (which already rev-parses
the base ref), travels on the in-memory `Worktree`/`RepoWorktreeResult` to the
executor, and is persisted best-effort after the launch's environment
transaction — the record is an enhancement, not a precondition of
materialization.

This reverses the origin-first position only for the worktree-materialized
task default. Manual picker selections and non-worktree scenarios keep their
existing resolution semantics.

## Consequences

- A local-first workflow's Changes panel shows only the worktree's own work,
  including after the mother branch advances; a rebase onto a newer mother tip
  shrinks the range to the post-rebase work.
- The first session of a fresh materialization is seeded from the
  preparer-observed fork points (the executor persists the record only after
  the launch returns), so the launch path carries both the observed values and
  the durable projection, with the fresher observation winning.
- Worktree recovery and re-materialization keep the record or re-anchor it to
  the same mother branch; live-merge-base resolution keeps the day-to-day
  anchor correct even when a stored ForkSHA goes stale.
- Existing tasks are unaffected: rows without a creation base resolve exactly
  as before, and the manual picker remains the escape hatch for users who
  want a different comparison.
- agentctl gains a creation-base transport layer
  (`POST /workspace/creation-bases`, `UpdateCreationBases`,
  `SetCreationBases`) mirroring the base-branch and comparison-target
  channels, with tracker keys shared by all three projections.

## Alternatives Considered

- **Session-start SHA (`base_commit_sha`) as the default anchor.** Rejected:
  after a rebase the whole rebase shows as the change, because the session
  start point is not on the rebased history. The fork point follows
  merge-base semantics like every other comparison base in the system.
- **Session field stamped at launch.** Rejected as the carrier: the status,
  ahead/behind, and multi-repo commits/diff paths read tracker state, not
  session fields, so the panel surfaces would drift from each other.
- **Keep origin-first and fix it in the picker.** Rejected: the picker cannot
  express a local branch name (its resolution rewrites to `origin/<name>`),
  and the default the user needs is knowable only at materialization time.
- **Re-anchor stale creation bases with `CorrectStaleComparisonBase`.**
  Rejected: it would silently expand the comparison range back to an
  integration default — exactly the failure this decision exists to prevent.

## Files touched

- `apps/backend/internal/task/models/creation_base.go` *(new)*
- `apps/backend/internal/task/repository/sqlite/task_repository_creation_base.go` *(new)*
- `apps/backend/internal/task/service/service_creation_base_projection.go` *(new)*
- `apps/backend/internal/worktree/manager_lifecycle.go`
- `apps/backend/internal/worktree/worktree.go`
- `apps/backend/internal/agent/runtime/lifecycle/manager_creation_base.go` *(new)*
- `apps/backend/internal/agentctl/server/process/creation_base.go` *(new)*
- `apps/backend/internal/agentctl/server/process/manager_creation_base.go` *(new)*
- `apps/backend/internal/agentctl/server/api/workspace_creation_bases.go` *(new)*
- `apps/backend/internal/agentctl/server/api/git.go`
- `apps/backend/internal/agentctl/server/process/workspace_git_status.go`
- `apps/backend/internal/orchestrator/executor/executor_execute.go`
- `apps/web/components/task/changes-panel-creation-base.tsx` *(new)*

Related: [0013-multi-branch-tasks.md](0013-multi-branch-tasks.md) (`base_branch`
semantics), `docs/specs/platform/requirements/workspace-git-status.md`,
`docs/specs/platform/system-design/workspace-git-status.md`.
