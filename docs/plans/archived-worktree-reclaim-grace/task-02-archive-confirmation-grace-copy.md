---
id: "02-archive-confirmation-grace-copy"
title: "State the grace discard in archive confirmation and docs"
status: done
wave: 1
depends_on: []
plan: "plan.md"
requirements:
  - REQ-TASKS-DIRTY-WORKTREE-ARCHIVE-003
acceptance_criteria:
  - AC-TASKS-DIRTY-WORKTREE-ARCHIVE-003.6
system_design:
  - ../../specs/tasks/system-design/dirty-worktree-grace-reclaim.md
---

# Task 02: State the grace discard in archive confirmation and docs

## Summary

Update the shared archive confirmation summary so it states that local
changes are retained, that a retained checkout is removed once clean, and
that local changes still present when the archive grace period (3 days by
default) elapses are discarded. Update translations and public documentation
to match.

## In scope

- The archive summary selection in
  `apps/web/components/task/task-cleanup-summary.ts` and its desktop, compact
  inline, and phone callers; the delete warning and discard-consent copy stay
  unchanged.
- Translation catalogs for all seven complete locales (run the Traditional
  Chinese pair through `pnpm run i18n:zh-hant`), including verbatim
  declarations where the English term is kept.
- Public documentation that describes archive retention and workspace
  reclamation (locate current statements via `rg -n "becomes clean|retained"
  docs/public`), including the config key `tasks.archiveReclaim.dirtyGrace`
  wherever startup settings are documented.
- Unit-test updates for the summary selection.

## Out of scope

- Archive API behavior, confirmation gating, or the `confirm_task_archive`
  bypass preference.
- Storage-page classification copy beyond what already exists for retained
  roots.

## Acceptance

- Desktop, compact inline, and phone archive confirmations state the grace
  discard; the delete confirmation copy is unchanged.
- No locale catalog misses the new keys, drops a placeholder, or leaves a
  value identical to English without a verbatim declaration.

## UI preview

See UI-01 in [plan.md](plan.md): the archive confirmation's worktree summary
gains the grace-discard sentence; layout, buttons, and the delete confirmation
are unchanged.

## Verification

```bash
(cd apps/web && pnpm exec vitest run components/task/task-cleanup-summary.test.ts)
(cd apps/web && pnpm run i18n:check)
(cd apps && pnpm --filter @kandev/web lint)
git diff --check -- docs/public
```

## Files likely touched

- `apps/web/components/task/task-cleanup-summary.ts`
- `apps/web/components/task/task-cleanup-summary.test.ts`
- `apps/web/src/locales/*/` (seven locale catalogs)
- `docs/public/**` task lifecycle and configuration pages

## Dependencies and risks

Depends on no other work order and shares no files with task 01. The main
risk is copy drift between the three confirmation compositions; they share
one summary selector, so the change is one string family, not three.

## Results

The archive summary (single, bulk, and their shared note) states the
grace discard in all seven locales; `task-cleanup-summary.test.ts` passes
29/29 including the new grace assertions, `pnpm run i18n:check` exits 0,
and `pnpm --filter @kandev/web lint` is clean. Public documentation updated:
`docs/public/operations.md` describes the grace discard, the active-task
veto, and the `0` escape hatch, and `docs/public/configuration.md` gains the
`tasks.archiveReclaim.dirtyGrace` row.

Two pre-existing catalog gaps surfaced by `i18n:check` were filled in the
same change because the check gates this work order: `editors:previewSvg`
translations for ja and ko (missing since the last upstream merge).
