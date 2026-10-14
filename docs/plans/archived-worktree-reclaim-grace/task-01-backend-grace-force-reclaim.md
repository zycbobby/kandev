---
id: "01-backend-grace-force-reclaim"
title: "Force-reclaim dirty archived worktrees after the grace period"
status: done
wave: 1
depends_on: []
plan: "plan.md"
requirements:
  - REQ-TASKS-DIRTY-WORKTREE-ARCHIVE-003
acceptance_criteria:
  - AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.1
  - AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.2
  - AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.3
  - AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.4
  - AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.5
system_design:
  - ../../specs/tasks/system-design/dirty-worktree-grace-reclaim.md
---

# Task 01: Force-reclaim dirty archived worktrees after the grace period

## Summary

When an `archive_reclaim` job's due attempt finds its checkout still dirty at
or after the configured grace period measured from the snapshot's
`ArchivedAt`, remove the checkout through the audited archived-cleanup path
with the existing discard switch instead of deferring for another 24 hours.

## In scope

- `TasksConfig.ArchiveReclaim.DirtyGrace` (`time.Duration`, mapstructure
  `dirtyGrace`) in `internal/common/config/config.go`, catalog entry
  `tasks.archiveReclaim.dirtyGrace` with env
  `KANDEV_TASK_ARCHIVE_RECLAIM_DIRTY_GRACE` and default `72h` in
  `internal/common/config/catalog.go`, wired into the task service
  construction.
- Grace evaluation in `Service.processArchiveReclaimJob`
  (`internal/task/service/resource_cleanup_jobs.go`): dirty outcome plus an
  expired positive window proceeds to removal; zero or negative grace keeps
  the current `waitForCleanArchiveReclaim` deferral.
- A discard-capable variant of `Manager.CleanupArchivedWorktree`
  (`internal/worktree/manager_cleanup.go`) passing
  `WorktreeCleanupOptions.DiscardWorktreeChanges: true` while keeping
  `validateArchivedCleanupIdentity`, path ownership, repository locks, and
  branch compaction unchanged.
- The `archiveReclaimWorktreeInUse` veto stays ahead of any force attempt and
  ignores the grace window; inspection errors keep the fail-closed retry path.
- Closed-set outcome `force_discarded` in the reclaim outcome reporting plus
  an `expvar.NewMap("archive_reclaim_total")` counter keyed by the closed
  outcome set, following `orphan_reap_total`.
- Service tests: force on expiry, fresh window after re-archive, zero grace
  disables, in-use veto, inspection-failure fail-closed, unarchive cancel
  still wins the race; config catalog completeness for the new key.

## Out of scope

- Archive API changes, confirmation copy, translations, and public docs
  (task 02).
- Any change to the delete-path guard, branch-handoff cleanup, or the orphan
  reaper.
- Backup, stash, or ref creation before discard.

## Acceptance

- A dirty checkout whose task has been archived for at least the grace period
  is removed with its local changes discarded on the next due attempt, and
  the job completes with a distinct forced-discard outcome.
- A checkout borrowed by an active task, and a checkout whose inspection
  fails, are never force-removed regardless of the window.
- With the grace set to `0`, behavior is byte-for-byte the current
  retain-until-clean contract.

## Verification

```bash
(cd apps/backend && go test ./internal/task/service ./internal/worktree ./internal/common/config)
(cd apps/backend && make lint)
```

## Files likely touched

- `apps/backend/internal/common/config/config.go`
- `apps/backend/internal/common/config/catalog.go`
- `apps/backend/internal/task/service/resource_cleanup_jobs.go`
- `apps/backend/internal/task/service/resource_cleanup_jobs_test.go`
- `apps/backend/internal/worktree/manager_cleanup.go`
- `apps/backend/internal/worktree/manager_cleanup_dirty.go` (test support only, if needed)

## Dependencies and risks

Depends on no other work order. The main risk is the unarchive race; the
existing claim-time fence (`cancelIfTaskUnarchived`) and mutation-boundary
identity revalidation must keep their current position ahead of any removal.

## Results

Implemented as specified. `TestArchiveReclaimForcesDirtyCheckoutAfterGracePeriod`,
`TestArchiveReclaimZeroGraceKeepsRetainUntilClean`,
`TestArchiveReclaimInUseVetoOutranksGracePeriod`, and
`TestArchiveReclaimInspectionFailureStaysFailClosedAfterGrace` pass; the
config tests (`TestArchiveReclaimDirtyGraceConfig`, catalog completeness and
audited-inventory tests) pass, and the worktree owner-mismatch guard
`TestCleanupArchivedWorktreeDiscardingChangesStillValidatesOwner` passes.

Environment note (this darwin host only): the service-fixture tests that
drive a real worktree need `TMPDIR` pointed at a symlink-free directory
(e.g. `/Users/<you>/.tmp-kandev-tests`); under the default `/var/folders`
the upstream no-follow path-pinning walk fails with "unsafe worktree path".
With that TMPDIR the three-package verification is otherwise green: the
`internal/worktree` package has 35 failures whose set is byte-identical on
stashed baseline, and `internal/task/service` has exactly one pre-existing
failure (`TestWorkspaceRecoveryProjectsRelocationErrorFromEveryEntryPoint`,
also failing on baseline). `make lint` reports 0 issues. The happy-path
discard coverage intentionally lives in the service fixture because the
minimal worktree-package store does not persist repository paths, which
makes the manager-level identity check unsatisfiable there.
