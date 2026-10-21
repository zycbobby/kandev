---
status: active
system: platform
created: 2026-10-21
owners:
  - kandev
---

# Worktree Comparison Base Requirements

## Overview

Task workspaces materialized as git worktrees must attribute only the worktree's own work to it. When the mother branch the worktree was cut from runs ahead of its remote counterpart, comparison-derived surfaces must not mix the mother branch's unpushed commits into the worktree's Changes, Review, and summary data.

## Requirements

### REQ-PLATFORM-WORKTREE-COMPARISON-BASE-001: Worktree creation-base comparison default

**Intent:** Record the fork point at materialization and use it as the comparison default, failing closed instead of expanding to an integration default.

#### Acceptance criteria

- **AC-PLATFORM-WORKTREE-COMPARISON-BASE-001.1:** When Kandev materializes a task workspace worktree for a repository by cutting a new branch, it shall record the creation base: the mother branch ref exactly as resolved at materialization (a local base as its plain name, a remote-only base as `origin/<name>`) and the fork-point commit that ref pointed at. Reuse and recreate paths shall not re-record a fork point, re-materialization through a new branch shall re-anchor the record to the same mother branch, and the record shall survive worktree recovery.
- **AC-PLATFORM-WORKTREE-COMPARISON-BASE-001.2:** For a task repository with a recorded creation base, comparison-derived surfaces — commit list, cumulative diff, comparison-derived ahead/behind totals, native Review, and task summaries — shall default to merge-base(HEAD, mother branch), resolved against the local ref verbatim without origin-first rewriting. A mother branch ahead of its remote counterpart shall show only the worktree's own work; an advancing mother branch shall keep the anchor stable; a rebase onto a newer mother tip shall shrink the range to the post-rebase work.
- **AC-PLATFORM-WORKTREE-COMPARISON-BASE-001.3:** When the recorded mother branch no longer resolves, or the live merge-base regresses strictly behind the recorded fork point (history rewrite or mother-branch rebase), the comparison shall use the recorded fork-point SHA. When the anchor itself can no longer resolve, comparison-derived data shall report an explicit unavailable state with the bounded code `creation_base_ref_unavailable` instead of falling back to an integration default (`origin/main`, `origin/master`, local `main`, local `master`). The file-status surface shall remain usable.
- **AC-PLATFORM-WORKTREE-COMPARISON-BASE-001.4:** An explicit repository-qualified comparison target shall remain authoritative over the creation-base default. A manual "Compare against" selection shall override the creation-base default and mark the row as user-selected; a later system-side creation-base refresh shall not replace the user's selection, and once a row is user-selected its creation base shall stop taking effect.
- **AC-PLATFORM-WORKTREE-COMPARISON-BASE-001.5:** The creation base shall change no `origin`, checkout branch, upstream tracking, push routing, worktree branch generation, or terminal cleanup semantics. Two worktrees may reference the same mother branch because it is only ever a comparison argument.
- **AC-PLATFORM-WORKTREE-COMPARISON-BASE-001.6:** Task repositories without a recorded creation base shall keep the existing comparison behavior, with no migration or backfill. Desktop and phone Changes surfaces shall name the effective mother branch when the creation-base default is active and identify the unavailable state, in all supported catalogs.

## Out of scope

- Changing the manual picker's own origin-first resolution semantics.
- Main-workspace and non-worktree task scenarios.
- Replacing the existing Git-status routes or comparison-target transport.

## System design

See the [worktree comparison-base design](../system-design/worktree-comparison-base.md) and
[ADR-2026-10-21-worktree-creation-base-comparison](../../../decisions/2026-10-21-worktree-creation-base-comparison.md).
