# ADR-2026-10-14-archived-worktree-grace-discard: Discard retained archived worktrees after a grace period

**Status:** accepted
**Date:** 2026-10-14
**Area:** backend, frontend

## Context

Archive preserves a Git worktree that holds tracked or untracked changes, and
the task-lifecycle recheck (ADR-2026-09-24-archived-worktree-reclamation)
revisits it daily until it becomes clean. A worktree whose local changes are
never committed is therefore revisited forever. On one install, two archived
tasks held about 1.05 GB of checkouts in `waiting_for_clean` because agents
had left untracked files behind. The user observed that after archiving, no
process can be working in those workspaces, so "retain until clean" behaves
as "retain forever" in practice.

Uncommitted changes in these worktrees live only in the local checkout of the
user's repository; origin has no copy. Removing them is irreversible data
destruction, which is why the 2026-09-24 decision confined forced discard to
the explicit delete-with-discard consent path.

## Decision

A retained archived worktree is preserved only for a bounded grace period.
When a recheck attempt finds a checkout still dirty at least 3 days after its
task was archived, the recheck discards the local changes and removes the
worktree through the existing audited archive cleanup path. The grace period
is an operator startup setting (`tasks.archiveReclaim.dirtyGrace`, default
`72h`; `0` restores retain-until-clean). Archiving remains non-destructive at
archive time; unarchive restarts the clock by cancelling and later recreating
the reclaim intent with a fresh archive timestamp.

Force reclamation keeps every existing safety guard except cleanliness: the
task must still be archived, the worktree identity and ownership must match
the recorded snapshot, and a checkout still referenced by an active task is
never force-removed. An inspection failure still fails closed and retries;
an unknown checkout state is never discarded. Forced reclamation is reported
as a distinct outcome in logs and counters.

No backup is taken before the discard. The product owner explicitly chose
direct discard over a stash-style safety net for this install-class default.

## Consequences

- Archived tasks with never-cleaned local changes stop occupying disk after
  the grace period instead of indefinitely.
- A user who archives by mistake and returns after the grace period loses
  uncommitted changes that archive previously would have preserved. The
  branch ref and committed work remain recoverable under the existing archive
  branch policy.
- The archive confirmation copy must state the grace window so the consent
  surface does not understate the discard.
- Operators can restore the previous contract per install by setting the
  grace period to `0`.

## Alternatives Considered

- **Retain until clean (status quo).** Rejected: the recheck provably never
  terminates for agent-generated untracked files, and disk use is unbounded.
- **Backup before discard (stash commit or hidden ref).** Rejected by the
  product owner: these worktrees attach to the user's own repositories, so a
  stash pollutes personal repository state; a dangling commit survives only
  until Git GC and requires recorded SHAs to be recoverable. Direct discard
  was chosen to keep the contract simple.
- **Archive-time discard consent flag.** Rejected: archive is a lightweight,
  cascading board action; the 2026-09-24 decision already documented that a
  reflexively passed consent flag recreates the defect it guards against.
