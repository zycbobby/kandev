---
status: current
system: platform
requirements:
  - REQ-PLATFORM-WORKTREE-COMPARISON-BASE-001
created: 2026-10-21
owners:
  - kandev
---

# Worktree Comparison Base

## Boundary and requirement mapping

Platform owns the comparison resolution, transport, and bounded failure contract for the worktree creation base. Tasks owns the task-repository projection and priority filtering. The worktree manager owns fork-point capture.

This design extends [workspace status](workspace-git-status.md) and specifies
[worktree comparison base](../requirements/worktree-comparison-base.md).

| Criteria | Design section |
| --- | --- |
| 001.1 | Carrier and capture |
| 001.2 | Resolution and comparison entry points |
| 001.3 | Fallback and bounded unavailable |
| 001.4 | Priority and live updates |
| 001.5 | Comparison-only boundary |
| 001.6 | Legacy behavior and display |

## Carrier and capture

A task-repository attachment materialized as a worktree persists one versioned `creation_base` in `task_repositories.metadata`: the mother branch ref exactly as resolved when the branch was cut and the fork-point commit that ref pointed at. `worktree.Manager.createNewBranchRef` already resolves the base ref to create the branch, so capture adds no Git command; the fork point travels on the in-memory `Worktree`/`RepoWorktreeResult` to the executor, which persists it best-effort after the launch's environment transaction. Reuse and recreate paths do not re-record; a replacement branch re-anchors the record. There is no backfill: rows without a record keep the origin-first default.

## Transport

The lifecycle seeds agentctl through the same transport family as base branches and comparison targets: instance config and create-request fields, launch metadata (`creation_bases`), `POST /workspace/creation-bases`, and `SetCreationBases`. Tracker keys follow the shared subpath projection. The first session of a fresh materialization is seeded from the preparer-observed fork points — the executor persists the record only after the launch returns — with the durable projection merged underneath and the fresher observation winning. Readiness rehydrates running workspaces and skips an empty hydration so it cannot wipe what the launch seeded. A manual base-branch selection pushes the projection without the selected row, which evicts that tracker's creation base. Submodule trackers stay pinned to their gitlink anchors.

## Resolution and comparison entry points

The anchor is `merge-base(HEAD, Ref)` with the recorded ref used verbatim; the three comparison entry points apply it identically after any explicit comparison target and before the branch-only origin-first resolution: tracker status base-commit and ahead/behind resolution, the commits panel, and the cumulative diff. Multi-repo fan-out resolves through the tracker's anchor.

## Fallback and bounded unavailable

When the ref no longer resolves, or the merge-base regresses strictly behind the fork point (the mother branch was rewritten), the anchor is the fork-point SHA. When that anchor cannot resolve, comparison-derived surfaces report the bounded code `creation_base_ref_unavailable` while the file-status surface stays ready. `CorrectStaleComparisonBase` never applies to a creation-base anchor: a recorded base must not silently expand to an integration default.

## Priority and live updates

Priority resolves before the creation base: an explicit repository-qualified comparison target, then a manual "Compare against" selection (the backend filters such rows out of the projection, so system writes are non-sticky), then the creation base, then the origin-first default.

## Comparison-only boundary

The creation base never rewrites `origin`, the checkout branch, upstream tracking, push routing, worktree branch generation, or terminal cleanup. It is only ever a comparison argument, so two worktrees may reference the same mother branch.

## Legacy behavior and display

Rows without a record resolve exactly as before; the manual picker remains the escape hatch. Status payloads carry the mother branch name and the bounded error code, and Changes names the effective base on desktop and phone through the branch details hover card and touch drawer.

This contract follows [ADR-2026-10-21-worktree-creation-base-comparison](../../../decisions/2026-10-21-worktree-creation-base-comparison.md).
