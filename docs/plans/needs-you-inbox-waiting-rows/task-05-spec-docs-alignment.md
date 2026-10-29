---
id: "05-spec-docs-alignment"
title: "Specification and user-doc alignment"
status: done
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

Done. The draft artifacts are promoted
(`docs/specs/ui/requirements/needs-you-inbox-waiting-rows.md` to
`status: active`, its system design to `status: current`), and the
requirements doc's supersession section is stated in effect rather than as a
future activation. AC-004.1 and AC-004.3 were confirmed to match decision D8
(no open action, Inbox-row navigation, manual instance switch for
informational kinds) as already revised during planning. The system design's
stale implementation premises were aligned without touching its conclusions:
the episode emitter converges on `publishSessionWaitingEvent` (11 waiting
convergence sites) plus the sessionless `autoStartTaskForLoadedStep` entry
(not two manual-branch sites), the episode identity is
`mw:<task_id>:<step_id>:<transitionID>` from the immutable
`task_step_transitions` row (not `<moved_at>`), the event payload is the
implemented field set, and the waiting-row re-read trigger is
`task.manual_step_entered`.

The supersession is applied to REQ-UI-NEEDS-YOU-INBOX-001's amended criteria
in `docs/specs/ui/requirements/needs-you-inbox.md`: AC-001.5 and .6 are
scoped to question rows within the wider row set, AC-001.20's not-counted
list and hidden-row disclosure are kind-neutral, and AC-001.27's difference
list gains the manual-step wait. That file sits 8 bytes under its 20 KiB
lint ceiling, so the conventional pointer line (as used for the
cross-instance-inbox exemptions) could not be added; the one-way reference
lives in the waiting-rows requirements' Supersession section, and a back-link
should be added when that file is next split or trimmed. The ui README needed
no boundary-link change (it links no inbox documents).

`apps/backend/internal/instances/AGENTS.md` now states the unified relay
(relayed set v2, notifier into `HandleRelayedOccurrence` with the instance
owner as explicit recipient, relay store as the dedup/recovery ledger only,
remoteinbox reading both the clarification and aggregated needs-you
endpoints). `docs/public/multi-instance.md` section 3 describes remote events
notifying per the notification settings with an origin label, no dedicated
sidebar module, and no open action, and its Inbox section covers remote
waiting rows and per-instance degradation; the "remote events never trigger
toasts, browser notifications, or sound" wording is removed, as is the
module-entry verify step in Rollout. The ADR revision paragraph names
REQ-UI-NEEDS-YOU-INBOX-003 through -005, the removed module, the
dedup-and-recovery relay store role, the v2 relayed set, and the
notification contract without an open action, consistent with the change's
`cross-instance-notifications` spec delta.

Validation: `openspec validate --all --strict --no-interactive` (13 passed),
`uv run --no-project --python 3.12 python scripts/list-docs.py validate`
(395 decisions, 1681 specifications), `uv run --no-project --python 3.12
python scripts/lint-spec-files.py --all` (all files pass), and
`git diff --check -- docs openspec` all pass. No production code was touched.
