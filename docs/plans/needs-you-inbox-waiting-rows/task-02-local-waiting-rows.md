---
id: "02-local-waiting-rows"
title: "Local waiting rows and the merged Inbox read"
status: done
wave: 2
depends_on: ["01-manual-step-episode-notification"]
plan: "plan.md"
requirements:
  - REQ-UI-NEEDS-YOU-INBOX-003
  - REQ-UI-NEEDS-YOU-INBOX-005
acceptance_criteria:
  - AC-UI-NEEDS-YOU-INBOX-003.1
  - AC-UI-NEEDS-YOU-INBOX-003.2
  - AC-UI-NEEDS-YOU-INBOX-003.3
  - AC-UI-NEEDS-YOU-INBOX-003.6
  - AC-UI-NEEDS-YOU-INBOX-003.8
  - AC-UI-NEEDS-YOU-INBOX-005.1
  - AC-UI-NEEDS-YOU-INBOX-005.2
  - AC-UI-NEEDS-YOU-INBOX-005.3
  - AC-UI-NEEDS-YOU-INBOX-005.4
  - AC-UI-NEEDS-YOU-INBOX-005.5
  - AC-UI-NEEDS-YOU-INBOX-005.7
  - AC-UI-NEEDS-YOU-INBOX-005.8
  - AC-UI-NEEDS-YOU-INBOX-005.9
system_design:
  - ../../specs/ui/system-design/needs-you-inbox-waiting-rows.md
---

# Task 02: Local waiting rows and the merged Inbox read

## Summary

Add the derived waiting-row query, the aggregate endpoint family
`GET /api/v1/needs-you-inbox` (+ `/hidden`) merging question and waiting
rows under one response, sidecar reuse through `mw:` identities, the extended
boot count, and the frontend waiting row with its menu and copy.

## In scope

- Repository query: tasks of the workspace in a manual step with no executing
  or queued turn, `StepRunsOnEntry` evaluated in Go, bounded page, episode
  identity `mw:<task_id>:<step_id>:<transitionID>` from the immutable
  `task_step_transitions` row id for the move into the current step.
- Aggregate endpoint and hidden enumeration under `needsYouInboxEnabled`,
  merged ordering by (reference time, id), count, hidden count, `has_more`
  from the one merge; `InboxBootSummary` extension.
- Sidecar join by row id; dismiss, snooze, restore, expiry, and hidden
  enumeration for waiting rows.
- Frontend: page client row variant and menu (UI-02), slice and controller
  move to the new endpoint, empty-state copy update, seven-locale copy with
  zh-hant conversion.
- The Threads classifier difference test (AC-003.8).

## Out of scope

- Remote rows of any kind (tasks 03 and 04) and relayed refresh triggers.
- Notification behavior (task 01, landed).
- History tab changes.

## Acceptance

- The merged read lists question and waiting rows under one bounded page with
  count, hidden count, and truncation from the same response; workspace
  switch replaces local rows.
- A waiting row resolves by re-read when its episode ends; a new episode
  yields a fresh row without inherited hiding.
- The Inbox renders correct pre-open badge state from the boot summary.

## ASCII UI preview

UI-01 excerpt (full preview in
[the plan](plan.md#ascii-ui-preview)); the waiting row and its menu are this
task's UI-02 view.

## Verification

```bash
cd apps/backend && go test ./internal/task/... ./internal/clarification/...
cd apps/web && pnpm vitest run lib/state/slices/needs-you-inbox lib/api/domains app/needs-you-inbox lib/coordinator && pnpm run typecheck && pnpm run i18n:check && pnpm run i18n:ratchet
cd apps/web && pnpm e2e:run --grep "waiting rows"
```

## Files likely touched

- `apps/backend/internal/task/repository/sqlite/needs_you_waiting_query.go`
- `apps/backend/internal/task/repository/sqlite/needs_you_waiting_query_test.go`
- `apps/backend/internal/task/repository/sqlite/clarification_inbox_sidecar.go`
- `apps/backend/internal/clarification/handlers.go`
- `apps/backend/internal/clarification/needs_you_inbox_handlers.go`
- `apps/backend/internal/clarification/needs_you_inbox_handlers_test.go`
- `apps/backend/internal/backendapp/boot_state_routes.go`
- `apps/web/app/needs-you-inbox/needs-you-inbox-page-client.tsx`
- `apps/web/lib/state/slices/needs-you-inbox/`
- `apps/web/lib/api/domains/needs-you-inbox-api.ts`
- `apps/web/hooks/domains/needs-you-inbox/use-needs-you-inbox-controller.ts`
- `apps/web/src/locales/*/needsYouInbox.json` (seven locales plus pseudo)
- `apps/web/e2e/tests/chat/needs-you-inbox-waiting-rows.spec.ts`

## Dependencies

Task 01 (episode semantics and event name are settled inputs).

## Risks

- The waiting query's join breadth on large boards; keep the SQL candidate
  narrowing and the bounded page discipline of the bundle query.

## Parallelism

`sequential`

## Inputs

- Requirements REQ-UI-NEEDS-YOU-INBOX-003 and -005; system design sections
  [Waiting-row read path](../../specs/ui/system-design/needs-you-inbox-waiting-rows.md#waiting-row-read-path),
  [Sidecar generalization](../../specs/ui/system-design/needs-you-inbox-waiting-rows.md#sidecar-generalization),
  [Aggregated read contract](../../specs/ui/system-design/needs-you-inbox-waiting-rows.md#aggregated-read-contract).

## Results

Done. `ListManualStepWaits`
(`apps/backend/internal/task/repository/sqlite/needs_you_waiting_query.go`)
narrows candidates by workspace, non-terminal state, `complete_task_on_enter
= 0`, no STARTING/RUNNING session, and no queued message, then filters
through `StepRunsOnEntry` in Go over a bounded overscan (`limit*4+1`, capped);
`queued_messages` is probed for existence because it is queue-owned. The
episode identity comes from the newest `task_step_transitions` row for the
step (correlated LEFT JOIN; no row falls back to `:0`) and is built by the
single source `taskmodels.ManualStepWaitRowID`, which the task-01 notifier
also delegates to, so the notification occurrence id, the row id, and the
sidecar key are one identity. The sidecar join reuses
`clarification_inbox_sidecar` keyed by the `mw:` id with the existing hidden
predicates.

The merged read lives in
`apps/backend/internal/clarification/needs_you_inbox_handlers.go`:
`GET /api/v1/needs-you-inbox` and `/hidden` return
`{rows, count, hidden_count, has_more, next_snooze_expiry}` from one merge of
the bundle page and the waiting page at the same limit (order
`(reference_time, id)`; either source failing returns 500 with no partial
list); `InboxBootSummary` calls the same merge helper for the pre-open badge.
`/api/v1/clarification-inbox` and its sidecar routes are unchanged, with
`mw:` ids authorized through the task-scope rule. The frontend moved to
`listNeedsYouInbox`/`listHiddenNeedsYouInbox` with the `rows` union slice, the
waiting-row component and shared actions menu, the merged hidden panel, the
`task.manual_step_entered` refresh trigger, kind-neutral copy in eight
catalogs, the classifier difference tests, and the
`needs-you-inbox-waiting-rows.spec.ts` E2E (3 tests).

Verification: the targeted backend and frontend suites pass, `pnpm run
typecheck`, `i18n:check`, `i18n:ratchet`, and the waiting-rows E2E all pass;
package-level failures in `internal/task/...` and `internal/backendapp` were
verified name-identical on a pristine-HEAD baseline (93 = 93, environmental).
The two variadic compatibility parameters this task originally introduced
were removed after acceptance: `RegisterRoutes` and `InboxBootSummary` take
the waiting store as a required parameter.
