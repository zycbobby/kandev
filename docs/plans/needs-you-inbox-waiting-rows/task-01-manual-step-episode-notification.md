---
id: "01-manual-step-episode-notification"
title: "Manual-step episode notification"
status: done
wave: 1
depends_on: []
plan: "plan.md"
requirements:
  - REQ-UI-NEEDS-YOU-INBOX-003
acceptance_criteria:
  - AC-UI-NEEDS-YOU-INBOX-003.4
  - AC-UI-NEEDS-YOU-INBOX-003.5
  - AC-UI-NEEDS-YOU-INBOX-003.7
system_design:
  - ../../specs/ui/system-design/needs-you-inbox-waiting-rows.md
---

# Task 01: Manual-step episode notification

## Summary

Emit `task.manual_step_entered` once per waiting episode from the
orchestrator's waiting convergence (one call inside
`publishSessionWaitingEvent`, which every site that settles a session into
waiting funnels through, plus the sessionless manual-step entry), and give it
notification treatment identical to a clarification: provider subscriptions,
sound, native notification, viewing suppression, and self-move suppression.
No Inbox row yet; that is task 02.

## In scope

- Shared emitter gated on `StepRunsOnEntry` false and
  `CompleteTaskOnEnter` false, called inside `publishSessionWaitingEvent`
  (all waiting convergence sites) and from the sessionless manual-step entry
  of `autoStartTaskForLoadedStep`; the move actor travels on the event and
  the notification service excludes the moving operator's user from
  recipients.
- Notification service event kind, `AvailableEvents` entry,
  `HandleManualStepWait`, default Local and System provider subscription,
  and the gateway bus subscription.
- Frontend event constant and handler entry in
  `apps/web/lib/ws/handlers/notifications.ts` and
  `apps/web/lib/notifications/events.ts`, honoring existing sound, native
  notification, and viewing suppression.

## Out of scope

- Any Inbox row, read, count, or copy (task 02).
- The relayed occurrence kind (task 03 wires the relay side).
- Office inbox items and any other notification kind.

## Acceptance

- Entering a manual step by an automated or agent move fires exactly one
  notification per entry with the same push presentation a clarification
  receives; a self-move fires none.
- Delivery is independent of `features.NeedsYouInbox`.
- No notification is emitted for steps that auto-start an agent or complete
  the task on entry.

## Verification

```bash
cd apps/backend && go test ./internal/orchestrator/... -run ManualStep && go test ./internal/notifications/...
cd apps/web && pnpm vitest run lib/ws/handlers/notifications.test.ts lib/notifications
```

## Files likely touched

- `apps/backend/internal/orchestrator/event_handlers_workflow.go`
- `apps/backend/internal/orchestrator/event_handlers_workflow_manual_wait_test.go`
- `apps/backend/internal/notifications/service/service.go`
- `apps/backend/internal/notifications/service/service_test.go`
- `apps/backend/internal/backendapp/gateway.go`
- `apps/web/lib/ws/handlers/notifications.ts`
- `apps/web/lib/ws/handlers/notifications.test.ts`
- `apps/web/lib/notifications/events.ts`

## Dependencies

None. This task must land after, or rebase onto, the landed remote-questions
change (`32e4f7e5`) only to the extent that change touches the same gateway
subscription sites.

## Risks

- The move-actor identity may not be uniformly available on both emission
  sites; if one site cannot know the actor, that site suppresses on the
  frontend viewing rule only and the gap is recorded here.

## Parallelism

`sequential`

## Inputs

- Requirement REQ-UI-NEEDS-YOU-INBOX-003, system design section
  [Episode occurrence and notification](../../specs/ui/system-design/needs-you-inbox-waiting-rows.md#episode-occurrence-and-notification).
- Existing clarification subscription in `gateway.go` and its occurrence
  filter as the pattern.

## Results

Done. The emitter is `notifyManualStepWait` in the new
`apps/backend/internal/orchestrator/manual_step_wait.go`, called once inside
`publishSessionWaitingEvent` (covering all 11 waiting convergence sites,
including the `launchAfterOnEnterDispatch` branch and failure settlement) and
once from `autoStartTaskForLoadedStep`'s manual branch for sessionless tasks
(reached via task.moved without a session, queue promotion, and task
creation, all behind the queue gates). It is gated on
`!StepRunsOnEntry && !CompleteTaskOnEnter`, resolves the episode identity
`mw:<task>:<step>:<transitionID>` from the newest `task_step_transitions` row
(`GetLatestStepTransitionForStep`, added alongside the `ActorKind`/`ActorID`
read-model fields; no row falls back to `:0` with a WARN), and publishes
`events.TaskManualStepEntered` (canonical constant in
`apps/backend/internal/events/types.go`) with `task_id`, `session_id`,
`workflow_step_id`, `occurrence_id`, `actor_kind`, `actor_id`, `occurred_at`.

The notification service gained `EventTaskManualStepEntered`,
`HandleManualStepWait`, the `AvailableEvents` entry, per-kind copy, and
actor-based self-move exclusion (`handleSemanticOccurrenceWithActor`; a human
actor with no id maps to the single-user fallback identity, so a self-move
delivers and records nothing). Fresh default providers subscribe through the
creation lists; existing local/system providers get a one-shot marker
migration (`add_task_manual_step_entered_to_local_and_system_v1`) that never
backfills a user's later opt-out. The gateway subscribes via
`subscribeManualStepWaitNotifications` and drops payloads without a task or
occurrence id. The frontend added the event constant, a handler entry with
the waiting sound and native notification and no open action, and settings
labels in all eight catalogs (zh-hk/zh-tw via `pnpm run i18n:zh-hant`).

Verification: 14 new backend tests (6 orchestrator, 3 service, 1 store
migration, 1 step-transition read model, 3 gateway wiring) and the targeted
frontend vitest files pass; `pnpm run i18n:check` passes. Remaining package
failures in `internal/orchestrator`, `internal/orchestrator/executor`,
`internal/notifications/service`, and `internal/task/repository/sqlite` were
verified name-identical on a pristine-HEAD baseline worktree (environmental
fixtures), so the change adds zero new failures. Known deviations from the
planned file list are recorded in the task report: the bus constant lives in
`internal/events/types.go` (canonical home), `lib/types/session-events.ts`
needed the handler's payload map entry, and `providers/provider.go` carries
the two origin fields that group 3 consumes.
