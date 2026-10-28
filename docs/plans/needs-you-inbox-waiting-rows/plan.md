---
created: 2026-10-28
status: draft
requirements:
  - REQ-UI-NEEDS-YOU-INBOX-003
  - REQ-UI-NEEDS-YOU-INBOX-004
  - REQ-UI-NEEDS-YOU-INBOX-005
system_design:
  - ../../specs/ui/system-design/needs-you-inbox-waiting-rows.md
---

# Implementation Plan: Needs-you Inbox Waiting Rows and Unified Notification

## Overview

The needs-you Inbox becomes the single waiting surface, and remote events use
one notification logic with local events. The package layers on the landed
remote-questions change (Kandev task `32e4f7e5`, remote clarification rows
read live through per-instance proxy ports, answered in place); this plan
starts from its landed base and does not modify its question contracts.

Delivery order: first the manual-step episode event and its local
notification (the new semantic occurrence everything else consumes), then the
local merged Inbox read with waiting rows, then the unified notification
bridge that removes the dedicated remote presentation path, then remote
waiting rows through the landed composition, and finally the specification
and user-doc alignment.

## Scope

### In scope

- Local manual-step waiting rows: derived read, one row per waiting episode,
  sidecar dismiss and snooze, merged count.
- `task.manual_step_entered`: semantic occurrence, local notification
  treatment identical to a clarification, self-move suppression.
- One notification logic for relayed events: origin-tagged occurrences
  through the shared pipeline, per-kind rules identical to local, relayed set
  v2, sidebar notifications module removal, relay store demoted to the dedup
  ledger.
- Aggregate local read `GET /api/v1/needs-you-inbox` (+ `/hidden`) merging
  question and waiting rows, extended boot count.
- Remote manual-step waiting rows via the landed per-instance composition,
  with degraded per-instance states.

### Out of scope

- Remote clarification rows: read, in-place answering, sidecar forwarding,
  workspace mapping, degraded states. Owned by the landed remote-questions
  change.
- Office inbox items entering the needs-you Inbox; the Threads needs-action
  classifier; history entries for waiting or remote rows.
- New runtime flags; profile default changes; notification settings UX.

## Technical approach

By boundary, with the owning requirement in parentheses:

- **Episode occurrence (002):** one shared emitter called from the
  orchestrator's two manual-branch sites
  (`apps/backend/internal/orchestrator/event_handlers_workflow.go`, manual
  branch of `processOnEnter` and `handleTaskMovedNoSession`), gated on
  `StepRunsOnEntry` false, `CompleteTaskOnEnter` false, and a non-self move
  actor. Notification service kind and provider subscription mirror
  `HandleClarificationRequested` (`apps/backend/internal/notifications/service/service.go`).
- **Local merged read (002, 004):** derived waiting-row query in the task
  SQLite repository; aggregate endpoint family under the existing
  `needsYouInboxEnabled` route registration
  (`apps/backend/internal/clarification/handlers.go`); sidecar reuse through
  `mw:` prefixed row identities; `InboxBootSummary` extension.
- **Unified bridge (003):** relay occurrence recovery feeds the notification
  service with origin-tagged events instead of its own
  `ws.ActionRemoteNotification` wire
  (`apps/backend/internal/instances/relay/relay.go`); frontend origin
  handling in `apps/web/lib/ws/handlers/notifications.ts`; module, slice, and
  module REST removal; relayed set v2 adds `task.manual_step_entered`.
- **Remote waiting rows (004):** extend the landed per-instance composition
  with the aggregate endpoint as remote API surface; degraded states per the
  landed pattern.

## ASCII UI preview

### UI-01: Needs-you tab, mixed row kinds (desktop)

Entry: sidebar Inbox, needs-you tab, three rows waiting. Structural: kind
glyph, task and step or question summary, origin label for remote rows, row
menu; copy illustrative and localized.

```text
┌ Inbox ──────────────────────────────────────────────────────────────┐
│ [Needs you 3] [Failed] [History]          ← badge = merged count     │
├─────────────────────────────────────────────────────────────────────┤
│ ▸ ?  Fix login redirect        task KAN-12, 2 questions        [⋯]   │
│ ▸ ⏸  Awaiting your review      task KAN-14, step Review       [⋯]   │
│ ▸ ⏸  Awaiting merge            gpu-box, step Merge      ◫ gpu [⋯]   │
│                                                                      │
│ 1 row hidden by your own dismiss or snooze.  Show hidden             │
└─────────────────────────────────────────────────────────────────────┘
```

Question rows keep their expand-to-answer behavior. Waiting rows have no
inline answer affordance; their menu is UI-02. Remote rows carry the origin
label and behave like the landed remote question rows. The empty state names
what it is not counting (AC-005.9 copy update).

### UI-02: Waiting row menu

```text
 ⏸  Awaiting your review   task KAN-14, step Review            [⋯]
                                          ├ Open task
                                          ├ Snooze ▸ 1 hour · 4 hours · 24 hours
                                          └ Dismiss
```

### UI-03: Sidebar after unification

The dedicated remote notifications module is removed; remote events arrive
through the same surfaces as local ones per notification settings.

```text
 sidebar before                          sidebar after
 ├ …                                     ├ …
 ├ ▾ 远端通知 (2)         →              ├ (section removed)
 │   ├ gpu-box · turn finished           │
 │   └ gpu-box · turn finished           │
 └ …                                     └ …
```

### UI-04: Phone composition

Same list and order as UI-01, full-width rows; the origin label wraps to a
second line; row menu opens the standard mobile action sheet; tapping a
remote row uses the same instance-switch path. The removed sidebar module
has no phone surface left to migrate (its AppNavSheet entry disappears with
it).

## Tests

| ACs | Evidence |
| --- | --- |
| 002.1, 002.2, 002.3 | `apps/backend/internal/task/repository/sqlite/needs_you_waiting_query_test.go` (new), `apps/backend/internal/clarification/needs_you_inbox_handlers_test.go` (new) |
| 002.4, 002.5 | `apps/backend/internal/orchestrator/event_handlers_workflow_manual_wait_test.go` (new), `apps/backend/internal/notifications/service/service_test.go` (extended) |
| 002.6 | `apps/backend/internal/task/repository/sqlite/clarification_inbox_sidecar_test.go` (extended) |
| 002.7 | notification service flag-independence test in the same files |
| 002.8 | `apps/web/lib/coordinator/attention.test.ts` (difference asserted) |
| 003.1, 003.2, 003.5 | `apps/backend/internal/instances/relay/relay_test.go` (extended), `apps/backend/internal/notifications/service/service_test.go` (origin-tagged delivery) |
| 003.3, 003.4 | `apps/web/lib/ws/handlers/notifications.test.ts` (extended: origin navigation, suppression by active instance) |
| 003.6 | flag-off behavior asserted in the relay and handler tests |
| 004.1 through 004.5, 004.7, 004.8 | `needs_you_inbox_handlers_test.go` (merge, order, page, count, hidden, boot, error) |
| 004.6 | remote composition tests extending the landed change's suite in `apps/backend/internal/instances/` |
| 004.9 | `pnpm run i18n:check`, `pnpm run i18n:ratchet` |

## E2E tests

- `apps/web/e2e/tests/chat/needs-you-inbox-waiting-rows.spec.ts` (new):
  move a task into a manual step from the board, observe the row, its menu,
  and its removal when the task moves on (AC-003.1 through .3, .6);
  notification and self-move suppression asserted through the mock agent
  fixtures (AC-003.4, .5).
- `apps/web/e2e/tests/multi-instance/multi-instance.spec.ts` (extended):
  remote waiting rows, degraded instance section, unified notification for a
  relayed kind, sidebar module absence (AC-004.1 through .6, AC-005.6).
- Mobile parity sibling of
  `apps/web/e2e/tests/chat/mobile-needs-you-inbox-answer.spec.ts` for the
  waiting row interactions (UI-04).

## Work orders

- [ ] [Task 01: Manual-step episode notification](task-01-manual-step-episode-notification.md)
- [ ] [Task 02: Local waiting rows and the merged Inbox read](task-02-local-waiting-rows.md)
- [ ] [Task 03: Unified notification bridge and module removal](task-03-unified-notification-bridge.md)
- [ ] [Task 04: Remote waiting rows through the landed composition](task-04-remote-waiting-rows.md)
- [ ] [Task 05: Specification and user-doc alignment](task-05-spec-docs-alignment.md)

## Verification results

Pending.

## Risks

- The package depends on the landed shape of the remote-questions change
  (composition mechanism, degraded pattern, count decision). If that change
  lands differently than its direction states, tasks 03 and 04 need a
  reconciliation pass before implementation.
- Removing the sidebar notifications module retires user-visible surface and
  its REST contract while federation is still default-off; the removal must
  ride the same feature gate so a flag-off panel is unchanged.
- One notification logic means a subscribed `session.turn_finished` now
  notifies for remote occurrences too; notification settings copy must not
  promise local-only delivery.
- The derived waiting-row query joins tasks, workflow steps, and sessions;
  it needs the same bounded-page discipline as the bundle query to avoid an
  unbounded scan on large boards.

## Open questions

None. Workspace mapping and the remote-rows count decision are owned by the
landed remote-questions change and are consumed, not reopened.
