---
id: "03-unified-notification-bridge"
title: "Unified notification bridge and module removal"
status: done
wave: 3
depends_on: ["02-local-waiting-rows"]
plan: "plan.md"
requirements:
  - REQ-UI-NEEDS-YOU-INBOX-004
acceptance_criteria:
  - AC-UI-NEEDS-YOU-INBOX-004.1
  - AC-UI-NEEDS-YOU-INBOX-004.2
  - AC-UI-NEEDS-YOU-INBOX-004.3
  - AC-UI-NEEDS-YOU-INBOX-004.4
  - AC-UI-NEEDS-YOU-INBOX-004.5
  - AC-UI-NEEDS-YOU-INBOX-004.6
system_design:
  - ../../specs/ui/system-design/needs-you-inbox-waiting-rows.md
---

# Task 03: Unified notification bridge and module removal

## Summary

Feed recovered relay occurrences into the shared notification pipeline with
an origin tag instead of the relay's dedicated `ws.ActionRemoteNotification`
wire, extend the relayed set with `task.manual_step_entered`, handle
origin-tagged events uniformly on the frontend (navigation by instance
switch, suppression by active instance and task), and remove the sidebar
notifications module with its slice and REST endpoints. The relay store
keeps only its deduplication and recovery role.

## In scope

- Relay rework in `apps/backend/internal/instances/relay/relay.go`: allowed
  set v2, origin-tagged republication through the notification service, dedup
  guaranteeing one delivery per occurrence.
- Frontend origin handling in `apps/web/lib/ws/handlers/notifications.ts`:
  origin label on the notification, no open action anywhere (D8),
  suppression comparing the active instance and task, needs-you relayed kinds
  refreshing the corresponding instance's Inbox page.
- Removal of `apps/web/components/app-sidebar/sections/notifications-section.tsx`,
  the remote-notifications slice, hook, API client, and the module's REST
  read and dismiss endpoints; relay store read and dismissed columns stop
  being presentation state.
- Notification settings copy audit so nothing promises local-only delivery.

## Out of scope

- Remote Inbox rows of any kind (task 04) and the landed remote question
  rows.
- New notification settings switches; provider behavior changes beyond the
  origin tag.
- Relay transport, reconciliation cadence, or dedup key changes.

## Acceptance

- A relayed occurrence notifies exactly as a locally emitted event of the
  same kind under the same subscriptions, with the origin visible; replay
  and reconciliation never re-notify.
- No dedicated remote presentation path remains: with federation off,
  behavior is identical to before this plan.
- A needs-you relayed occurrence refreshes the Inbox; an informational one
  does not touch it.

## ASCII UI preview

UI-03 in [the plan](plan.md#ascii-ui-preview): the sidebar module is removed;
remote events arrive through the shared notification surfaces.

## Verification

```bash
cd apps/backend && go test ./internal/instances/... ./internal/notifications/...
cd apps/web && pnpm vitest run lib/ws/handlers/notifications.test.ts components/app-sidebar && pnpm run typecheck && pnpm run i18n:check && pnpm run i18n:ratchet
cd apps/web && pnpm e2e:run --grep "multi-instance"
```

## Files likely touched

- `apps/backend/internal/instances/relay/relay.go`
- `apps/backend/internal/instances/relay/relay_test.go`
- `apps/backend/internal/instances/AGENTS.md`
- `apps/backend/internal/backendapp/remote_notification_routes.go`
- `apps/backend/internal/notifications/service/service.go`
- `apps/web/components/app-sidebar/sections/notifications-section.tsx` (removed)
- `apps/web/lib/state/slices/remote-notifications/` (removed)
- `apps/web/hooks/domains/remote-notifications/` (removed)
- `apps/web/lib/api/domains/remote-notifications-api.ts` (removed)
- `apps/web/lib/ws/handlers/notifications.ts`
- `apps/web/lib/ws/handlers/notifications.test.ts`
- locale keys under `instances.json` retired or relocated

## Dependencies

Task 02 (the Inbox refresh trigger wiring lands on the new controller), and
the landed remote-questions change for the proxy and composition context.

## Risks

- Removing the module retires user-visible surface; the removal must leave a
  flag-off panel untouched, so the deleted code paths must all sit behind
  `features.multiInstance` today. Verify that before deleting.

## Parallelism

`sequential`

## Inputs

- Requirement REQ-UI-NEEDS-YOU-INBOX-004; system design section
  [Unified notification bridge](../../specs/ui/system-design/needs-you-inbox-waiting-rows.md#unified-notification-bridge).

## Results

Done. The relay (`apps/backend/internal/instances/relay/relay.go`) keeps its
subscription, 60s reconciliation, and (instance, event, occurrence) dedup key
and, after a successful dedup insert, hands the occurrence to
`notifications.Service.HandleRelayedOccurrence` with the instance owner as the
explicit recipient and the origin identity on the message; it no longer holds
a broadcaster, and `ws.ActionRemoteNotification` is deleted. The relayed set
is v2: `session.turn_finished`, `session.clarification_requested`,
`office.inbox_item`, `task.manual_step_entered`. `HandleRelayedOccurrence`
never writes the local occurrence history, honors per-kind subscriptions, and
falls back to local per-kind copy when the remote sends no title/body; the
System provider folds `[<instance name>]` into the OS-notification title; the
Local provider frame carries `origin_instance_id`/`origin_instance_name` only
when set.

The frontend handler labels origin-tagged notifications, suppresses them only
while the active instance equals the origin and the active task matches, and
adds no open action anywhere (D8); `use-remote-inbox-controller` bumps the
named origin instance's refresh tick on relayed clarification and
manual-step frames, while the local controller ignores origin-carrying
manual-step frames. The sidebar notifications module, the
remote-notifications slice/hook/API client, the `remote_notification` WS
action, and the `/api/v1/remote-notifications` routes are deleted; the relay
store keeps only Record/LatestOccurrenceTime/SweepOccurrences, with the
display read/dismiss methods removed and the table retained for dedup and
bounded retention. The module's two orphaned locale keys were removed from
all eight catalogs in a scoped follow-up.

Verification: backend groups pass except failures verified name-identical on
pristine HEAD; `pnpm vitest run lib/ws/handlers/notifications.test.ts
components/app-sidebar lib/state/slices` (135 files, 1440 tests),
`typecheck`, and `i18n:check` pass; `pnpm e2e:run --grep "multi-instance"`
passed 5/5 at this task's close (flow rewritten around the removed module:
relayed notification with origin label, module absence, endpoint 404, kill-B
degradation, flag-off regression).
