---
status: active
system: tasks
created: 2026-10-14
owners:
  - cfl
---
# Dirty Worktree Grace Reclaim Requirements

## Overview

This document extends
[dirty worktree task archive](dirty-worktree-archive.md), which owns the
archive-side contract for checkouts holding uncommitted work: archive
preserves such a checkout and a durable recheck revisits it until it becomes
clean. That recheck never terminates for a checkout whose local changes are
never cleaned, and retained checkouts are pure disk cost once their task is
archived.

This document bounds that preservation with an operator-configurable grace
period. It is kept as a separate document from the capability it extends so
the upstream contract files remain untouched; the requirement IDs continue
the `REQ-TASKS-DIRTY-WORKTREE-ARCHIVE` sequence. The product decision and the
rejected alternatives are recorded in
[ADR-2026-10-14-archived-worktree-grace-discard](../../../decisions/2026-10-14-archived-worktree-grace-discard.md).

## Requirements

### REQ-TASKS-DIRTY-WORKTREE-ARCHIVE-003: Grace-Period Forced Reclamation

**Intent:** A retained checkout shall be discarded once its task has been
archived continuously beyond a bounded grace period, so archive reclamation
terminates for checkouts whose local changes are never cleaned. The
preservation guarantees of AC-TASKS-DIRTY-WORKTREE-ARCHIVE-001.1 and
AC-TASKS-DIRTY-WORKTREE-ARCHIVE-002.3 apply within this grace period; this
requirement owns what happens when it elapses.

#### Acceptance criteria

- **AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.1:** When a recheck finds an archived
  task's retained worktree still dirty at or after the archive grace period
  measured from the task's archive time, the system shall discard the local
  tracked and untracked changes and remove that worktree under the existing
  archive identity, ownership, and branch policy.
- **AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.2:** The grace period shall be
  anchored to the task's archive time, so an unarchive and re-archive cycle
  grants a fresh window; a grace period of zero shall restore
  retain-until-clean behavior.
- **AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.3:** When a retained worktree is
  still referenced by an active task at or after the grace period, the system
  shall not force-remove it and shall keep rechecking.
- **AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.4:** When the change inspection fails
  at or after the grace period, the system shall keep the worktree and retry;
  an unknown checkout state shall never be discarded.
- **AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.5:** Each forced reclamation shall be
  observable as a distinct forced-discard outcome in recheck reporting,
  without recording change contents.
- **AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.6:** When archive confirmation is
  shown on desktop or phone, its worktree summary shall state, in addition to
  the retention and clean-removal facts required by
  AC-TASKS-DIRTY-WORKTREE-ARCHIVE-002.6, that local changes still present
  when the archive grace period elapses are discarded. The user's choice to
  bypass archive confirmation and programmatic archive behavior shall remain
  unchanged.

## Exclusions

- No change to the archive API: no refusal, no discard-consent flag, no
  dirty-file list.
- No change to the task-delete dirty-checkout guard, branch-handoff cleanup,
  or the orphan worktree reaper.
- No backup, stash, or ref creation before a forced discard.
- No change to storage maintenance scheduling or quarantine policy.

## System design

[Dirty worktree grace reclaim](../system-design/dirty-worktree-grace-reclaim.md).

## Implementation plans

- [Discard retained archived worktrees after a grace period](../../../plans/archived-worktree-reclaim-grace/plan.md)
