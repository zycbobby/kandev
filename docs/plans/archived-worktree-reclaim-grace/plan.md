---
created: 2026-10-14
status: implemented
requirements:
  - REQ-TASKS-DIRTY-WORKTREE-ARCHIVE-005
system_design:
  - ../../specs/tasks/system-design/dirty-worktree-grace-reclaim.md
legacy_specs: []
---

# Implementation Plan: Discard Retained Archived Worktrees After a Grace Period

## Overview

Bound the archived-worktree recheck's patience. Today a checkout that never
becomes clean is revisited every 24 hours forever; two archived tasks on one
install held about 1.05 GB this way. This package makes the recheck discard a
still-dirty checkout once its task has been archived continuously beyond an
operator-configurable grace period (default 3 days), keeps every identity,
ownership, reference, and fail-closed guard except cleanliness, and updates
the archive confirmation copy so the consent surface states the discard.

The product decision and its rejected alternatives are recorded in
[ADR-2026-10-14-archived-worktree-grace-discard](../../decisions/2026-10-14-archived-worktree-grace-discard.md).

This install tracks upstream Kandev and merges remote changes regularly, so
the package deliberately adds only new files. The requirement and design live
in separate `dirty-worktree-grace-reclaim` documents that reference the
upstream `dirty-worktree-archive` pair without editing it; do not fold the
grace criteria into the upstream files, and do not edit other tracked files
beyond what the work orders name.

## Scope

### In scope

- An operator startup setting `tasks.archiveReclaim.dirtyGrace`
  (`KANDEV_TASK_ARCHIVE_RECLAIM_DIRTY_GRACE`, default `72h`, `0` disables
  forcing) following the typed `tasks.deliveryRetention.*` config contract.
- Grace evaluation inside the existing `archive_reclaim` job processing, keyed
  to the `ArchivedAt` value already recorded in the job snapshot, so
  unarchive/re-archive cycles grant a fresh window with no new clock state.
- Forced removal through the audited archived-cleanup path with the existing
  `DiscardWorktreeChanges` bypass, preserving the in-use veto and the
  fail-closed inspection-error path.
- A closed-set `force_discarded` outcome in recheck reporting plus an
  `archive_reclaim_total` expvar counter following the `orphan_reap_total`
  convention.
- Archive confirmation copy on desktop and phone stating the retention, the
  clean-removal rule, and the grace discard, with translations and public
  documentation.

### Out of scope

- An archive-time discard-consent flag or dirty-file list on the archive API.
- Any change to the task-delete dirty-checkout guard or the branch-handoff and
  orphan-reaper cleanup callers.
- Backups or stash-style safety nets before a forced discard (explicitly
  rejected in the ADR).
- Changes to storage maintenance scheduling or quarantine policy.

## Work order dependency graph

```text
Wave 1 (parallel-safe):
  task-01  backend grace evaluation + forced reclamation
  task-02  archive confirmation grace copy + translations + docs
```

The two work orders have independent verification boundaries (backend tests
versus web tests, i18n checks, and public docs) and share no files.

## Risks

- **Data loss surface.** Forced discard destroys the only copy of uncommitted
  work after the grace period. Mitigated by the explicit product decision in
  the ADR, the `0`-disables operator escape hatch, and copy that states the
  discard in the confirmation surface.
- **Unarchive race.** A force attempt must never win against an unarchive.
  Mitigated by the existing claim-time `cancelIfTaskUnarchived` fence and
  identity revalidation at the mutation boundary; both keep their current
  position ahead of any removal.
- **Shared checkouts.** A worktree borrowed by an active task must never be
  force-removed. Mitigated by keeping `archiveReclaimWorktreeInUse` as an
  unconditional veto ahead of the force path, independent of the grace window.

## Verification strategy

Backend behavior is covered by service-level tests around
`processArchiveReclaimJob` (grace expiry forces, in-use vetoes, inspection
failure stays fail-closed, zero grace disables, unarchive cancels) and config
catalog tests for the new key. Copy is covered by the existing
`task-cleanup-summary` tests, `i18n:check`, and the non-JSX copy scanner.
Specification changes validate with the catalog and spec linters.

## ASCII UI previews

UI-01: Archive confirmation, worktree summary region (desktop and phone share
one summary selector; only copy changes, layout and touch targets unchanged).
English copy below is illustrative; final strings go through the locale
catalogs.

```text
Before (current behavior):
+----------------------------------------------------------+
| Archive task?                                            |
|                                                          |
| Repositories with local changes are kept. A kept         |
| checkout is removed after it becomes clean.              |
|                                                          |
|                [ Cancel ]  [ Archive ]                    |
+----------------------------------------------------------+

After:
+----------------------------------------------------------+
| Archive task?                                            |
|                                                          |
| Repositories with local changes are kept. A kept         |
| checkout is removed once it becomes clean; local         |
| changes still present after 3 days are discarded.        |
|                                                          |
|                [ Cancel ]  [ Archive ]                    |
+----------------------------------------------------------+
```

Covers AC-TASKS-DIRTY-WORKTREE-ARCHIVE-005.6. The structural requirement is
the stated retention, clean-removal, and grace-discard facts in one summary;
exact wrapping and button placement are illustrative and follow the existing
compositions.
