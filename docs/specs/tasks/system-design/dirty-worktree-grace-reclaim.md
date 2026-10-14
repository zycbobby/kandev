---
status: current
system: tasks
requirements:
  - REQ-TASKS-DIRTY-WORKTREE-ARCHIVE-003
created: 2026-10-14
owners:
  - cfl
---

# Dirty Worktree Grace Reclaim System Design

## Purpose and boundaries

This design bounds the archived-worktree recheck defined in
[dirty worktree task archive](dirty-worktree-archive.md) with a grace period:
a checkout still dirty past the window is discarded instead of being
revisited forever. It is a separate document so the sibling upstream design
file remains untouched; it modifies only the behavior paths named below and
changes no other boundary of that design.

It does not change task deletion, session deletion, branch-handoff cleanup,
the orphan worktree reaper, workspace reset, storage maintenance scheduling,
or the archive API. No backup is taken before a forced discard; see
[ADR-2026-10-14-archived-worktree-grace-discard](../../../decisions/2026-10-14-archived-worktree-grace-discard.md).

## Requirement mapping

| Requirement | Design source |
| --- | --- |
| AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.1-.2 | Configuration and evaluation point below |
| AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.3 | Force-time guards below |
| AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.4 | Force-time guards below |
| AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.5 | Observability below |
| AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.6 | Archive confirmation below |

## Design

### Configuration

Add `ArchiveReclaim ArchiveReclaimConfig` to `TasksConfig` in
`internal/common/config/config.go` with one field, `DirtyGrace time.Duration`
(`mapstructure:"dirtyGrace"`), and a catalog entry
`tasks.archiveReclaim.dirtyGrace`
(`KANDEV_TASK_ARCHIVE_RECLAIM_DIRTY_GRACE`, default `72h`, owner "task
archive reclaim") in `internal/common/config/catalog.go`, following the
`tasks.deliveryRetention.*` entries. `0` disables forcing and restores
retain-until-clean. The task service receives the resolved duration at
construction alongside its other settings; the cleanup worker reads it from
the service, not from global config, so tests construct it explicitly.

### Evaluation point

`processArchiveReclaimJob` in `internal/task/service/resource_cleanup_jobs.go`
already branches on the outcome prepared by `prepareArchiveReclaimWorktree`.
When that outcome is `taskArchiveReclaimOutcomeDirty` and the resolved grace
period is positive and `time.Now()` is at or after
`snapshot.ArchivedAt.Add(grace)`, the job proceeds to removal instead of
`waitForCleanArchiveReclaim`. The grace clock is the `ArchivedAt` value
already recorded in the job snapshot, so unarchive (which cancels the job)
followed by re-archive (which creates a fresh job) grants a new window with
no clock bookkeeping of its own. Every due attempt re-evaluates the window,
so a backend restart cannot skip or double-apply it.

### Force-time guards

Forced removal reuses the audited archived-cleanup path with the existing
`WorktreeCleanupOptions.DiscardWorktreeChanges` switch — the same bypass the
delete-with-discard path uses — while keeping
`validateArchivedCleanupIdentity`, path ownership, repository locks, and the
branch-preserving archive policy unchanged. The manager
(`internal/worktree/manager_cleanup.go`) gains a discard-capable variant of
`CleanupArchivedWorktree` (an options parameter or sibling method) that
passes `DiscardWorktreeChanges: true`; the existing final cleanliness guard
already yields to that switch. Two guards still veto force: the
active-reference check (`archiveReclaimWorktreeInUse`) runs before the
attempt and an in-use checkout keeps waiting regardless of the grace window,
and an inspection error keeps the fail-closed retry path so an unknown
checkout state is never discarded. A `stale` identity outcome completes as
today. The claim-time `cancelIfTaskUnarchived` fence keeps its current
position ahead of any removal, so unarchive still wins the race.

### Observability

Extend the closed reclaim outcome set with `force_discarded`, reported
through the existing zap outcome fields, and add an
`expvar.NewMap("archive_reclaim_total")` counter keyed by the same closed
outcome set (`reclaimed`, `force_discarded`, `dirty`, `in_use`, `stale`,
`failed`), following the `orphan_reap_total` convention in
`internal/task/service/`. No change contents, file paths beyond the existing
structured fields, or new identifiers are recorded.

### Archive confirmation

`task-cleanup-summary.ts` also supplies task-delete confirmation. The archive
summary used by desktop confirmation, compact inline confirmation, and the
phone confirmation sheet must additionally state that local changes still
present when the archive grace period (3 days by default) elapses are
discarded, on top of the retention and clean-removal facts it already states.
It must not claim that every worktree branch is deleted: unpublished branches
remain recoverable. The delete warning, discard-consent copy,
`confirm_task_archive` bypass, and archive API stay unchanged. Add
translations in the supported locale catalogs and update public
task/storage documentation.

## Out of scope

- Refusing archive, or adding a discard-consent flag and a dirty-file list to
  the archive API.
- The sibling `removeBranch=false` callers in branch-handoff cleanup and the
  orphan worktree reaper, which have different triggers and admission rules.
- Any change to the delete-path guard.
- Backups, stash commits, or hidden refs before a forced discard.
- Automatically deleting ignored files or unrelated non-Git task files.
- Making scheduled storage maintenance run by default.
