---
id: "05-spec-docs-alignment"
title: "Specification and user-doc alignment"
status: pending
wave: 5
depends_on: ["04-remote-waiting-rows"]
plan: "plan.md"
requirements:
  - REQ-UI-NEEDS-YOU-INBOX-003
  - REQ-UI-NEEDS-YOU-INBOX-004
  - REQ-UI-NEEDS-YOU-INBOX-005
acceptance_criteria:
  - AC-UI-NEEDS-YOU-INBOX-005.9
system_design:
  - ../../specs/ui/system-design/needs-you-inbox-waiting-rows.md
---

# Task 05: Specification and user-doc alignment

## Summary

Bring every durable document in line with the shipped behavior: the
cross-instance notifications OpenSpec specification (relayed set v2, unified
presentation, module removal, relay store role), the instances module
invariant notes, and the public multi-instance user guide. Promote the draft
requirement and system design per the spec-driven-development lifecycle.

## In scope

- Revise `openspec/specs/cross-instance-notifications/spec.md` through the
  repository's OpenSpec change flow: relayed set v2 with
  `task.manual_step_entered`, one notification logic with origin tags,
  removal of the sidebar module requirement, relay store as dedup ledger
  only.
- Update `apps/backend/internal/instances/AGENTS.md` invariants (relay no
  longer "publishes only into the sidebar notifications module").
- Update `docs/public/multi-instance.md` user guide for the unified
  notification behavior and the removed module.
- Promote `docs/specs/ui/requirements/needs-you-inbox-waiting-rows.md` to
  `active` and its system design to `current`; apply the supersession notes
  to REQ-UI-NEEDS-YOU-INBOX-001's amended criteria; update the ui system
  README boundary links if ownership can be confused with the federation
  specs.

## Out of scope

- Any production code or test change.
- Reopening settled product decisions.

## Acceptance

- Every revised document states the shipped behavior, no document still
  describes the removed sidebar module as the surface for needs-you relayed
  events, and the draft artifacts are promoted with the supersessions
  applied.

## Verification

```bash
uv run --no-project --python 3.12 python scripts/list-docs.py validate
uv run --no-project --python 3.12 python scripts/lint-spec-files.py --all
cd apps/web && pnpm run i18n:check
git diff --check -- docs openspec
```

## Files likely touched

- `openspec/specs/cross-instance-notifications/spec.md`
- `apps/backend/internal/instances/AGENTS.md`
- `docs/public/multi-instance.md`
- `docs/specs/ui/requirements/needs-you-inbox-waiting-rows.md`
- `docs/specs/ui/requirements/needs-you-inbox.md`
- `docs/specs/ui/system-design/needs-you-inbox-waiting-rows.md`
- `docs/specs/ui/README.md` (only if boundary links change)

## Dependencies

Task 04, so the documents describe shipped behavior rather than intent.

## Risks

- None material; documentation only.

## Parallelism

`sequential`

## Inputs

- The full shipped behavior of tasks 01 through 04 and the revised ADR.

## Results

Pending.
