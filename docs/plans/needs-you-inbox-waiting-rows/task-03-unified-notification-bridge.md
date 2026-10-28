---
id: "03-unified-notification-bridge"
title: "Unified notification bridge and module removal"
status: pending
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
  open-by-switch for remote origins, suppression comparing the active
  instance, needs-you relayed kinds refreshing the Inbox read.
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

Pending.
