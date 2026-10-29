---
status: current
system: ui
requirements:
  - REQ-UI-NEEDS-YOU-INBOX-003
  - REQ-UI-NEEDS-YOU-INBOX-004
  - REQ-UI-NEEDS-YOU-INBOX-005
---

# Needs-you Inbox Waiting Rows System Design

## Context and boundaries

This design extends the needs-you Inbox
([part 1](needs-you-inbox-01.md), [part 2](needs-you-inbox-02.md),
[part 3](needs-you-inbox-03.md)) from one row kind to three and unifies remote
notification with local. The observable contract is
[the requirements](../requirements/needs-you-inbox-waiting-rows.md); this
document owns the technical path for all runtime boundaries involved, backend
and frontend.

The package layers on the landed remote-questions change (Kandev task
`32e4f7e5`): that change lists remote clarification bundles in the local
Inbox through per-instance live reads over each instance's proxy port,
answers them in place by forwarding through the proxy, keeps sidecar state on
the remote, and renders degraded per-instance states. This package reuses its
composition mechanism for remote manual-step rows and does not modify its
question contracts.

Boundaries consumed unchanged:

- Manual-step semantics: `StepRunsOnEntry`
  (`apps/backend/internal/workflow/models/step_semantics.go:7`) plus the
  `CompleteTaskOnEnter` step field decide whether a step is manual. The Inbox
  and the notification pipeline consume that predicate; neither redefines it.
- Clarification bundle visibility, resolution, and the existing bundle read
  path (`ListUnresolvedClarificationBundles`,
  `apps/backend/internal/task/repository/sqlite/clarification_bundle_query.go:19`)
  are unchanged; question rows keep their meaning.
- Federation transport and dedup stay owned by
  `openspec/specs/cross-instance-notifications/spec.md`. This change extends
  the relayed occurrence set with one kind and replaces the relay's dedicated
  presentation path with the shared notification pipeline; both are recorded
  as revisions of that spec and of
  [ADR 2026-10-28-multi-instance-federation](../../../decisions/2026-10-28-multi-instance-federation.md).

The design preserves two invariants explicitly:

- The count and the list come from one read (REQ-005): one endpoint computes
  the merged local page, count, hidden count, and truncation flag in one
  response, and remote composition attaches per-instance pages under the same
  response shape with per-instance degradation.
- Remote data isolation: remote rows are read live through the proxy; the
  relay store holds only notification occurrences for deduplication and
  recovery, and no remote task or session row enters local task tables.

## Requirement mapping

| ACs | Design section |
| --- | --- |
| 003.1, 003.2, 003.3 | [Waiting-row read path](#waiting-row-read-path) |
| 003.4, 003.5, 003.7 | [Episode occurrence and notification](#episode-occurrence-and-notification) |
| 003.6 | [Sidecar generalization](#sidecar-generalization) |
| 003.8 | [Waiting-row read path](#waiting-row-read-path) (classifier untouched) |
| 004.1, 004.2, 004.5 | [Unified notification bridge](#unified-notification-bridge) |
| 004.3, 004.4 | [Unified notification bridge](#unified-notification-bridge), [Frontend surfaces](#frontend-surfaces) |
| 004.6 | [Flags and profiles](#flags-and-profiles) |
| 005.1 through 005.5, 005.7, 005.8 | [Aggregated read contract](#aggregated-read-contract) |
| 005.6 | [Remote waiting rows](#remote-waiting-rows) |
| 005.9 | [Frontend surfaces](#frontend-surfaces) |

## Episode occurrence and notification

A waiting episode begins wherever the runtime converges a task onto "this
step will not start an agent":

- With a session: every path that settles a session into `WAITING_FOR_INPUT`
  funnels through `publishSessionWaitingEvent`
  (`apps/backend/internal/orchestrator/event_handlers_workflow.go:7614`),
  whose eleven call sites cover the manual branch of `processOnEnter`, the
  non-starting on-enter dispatch branch, and the failure-settlement paths.
- Without a session: the manual branch of `autoStartTaskForLoadedStep`
  (`apps/backend/internal/orchestrator/event_handlers_workflow.go:2130`),
  reached from `handleTaskMovedNoSession`, queue promotion, and task
  creation.

Both sites call one shared emitter, `notifyManualStepWait`
(`apps/backend/internal/orchestrator/manual_step_wait.go`), gated on the step
being manual (`StepRunsOnEntry` false, `CompleteTaskOnEnter` false). The
emitter publishes the semantic event `task.manual_step_entered` on the
internal event bus, carrying task id, session id, step id, the episode
occurrence id, the move's actor kind and id, and the move timestamp. The
episode identity (`mw:<task_id>:<step_id>:<transitionID>`, from the immutable
`task_step_transitions` ledger row) is stable across a whole episode, so the
notification service's occurrence dedup delivers exactly one notification per
episode even though the emitter fires on every waiting convergence. The
move's actor travels on the event: the notification service excludes the
moving operator's user from the recipients (a human actor with no id maps to
the single-user fallback identity), so an episode begun by the operator's own
move is listed but never pushes, while agent and other-operator moves notify.
Entering a manual step from another manual step in an automated chain is a
new episode and notifies again; that is deliberate (the operator is now owed
a different action in a different step) and is the behavior the product
decision selected.

The notification pipeline grows symmetrically with clarifications:

- `internal/notifications/service/service.go` adds the event constant to
  `AvailableEvents` and a `HandleManualStepWait` entry point next to
  `HandleClarificationRequested` (`:227`).
- `internal/backendapp/gateway.go` subscribes the bus event next to the
  clarification subscription (`subscribeManualStepWaitNotifications`, `:67`,
  wired at `:530`), reusing the same occurrence-filter discipline: exactly one
  notification per episode.
- `ensureDefaultProviders` (`service.go:667`) subscribes the default Local and
  System providers to the new kind, fresh installs through the creation lists
  and existing installs through the one-time subscription migration, so push
  presentation, sound, and native notifications behave exactly as for a
  question, including the frontend's viewing suppression
  (`apps/web/lib/ws/handlers/notifications.ts:25`).
- Delivery is independent of `features.NeedsYouInbox`, exactly as
  clarification notifications are today; enabling the flag never backfills
  episodes that already began.

The same event is the semantic boundary the federation relay consumes, so a
remote instance running this version emits `task.manual_step_entered`
occurrences that notify locally through the unified bridge below, with the
deferred-dispatch discipline every relayed kind already inherits.

## Waiting-row read path

Waiting rows are derived on read, not stored. The query lists tasks of the
workspace whose current workflow step is manual, joining the active session,
and keeps a task only when no turn is executing or queued. Step predicates are
evaluated in Go through `StepRunsOnEntry` after the SQL narrows candidates by
workspace and non-terminal task state, so the predicate has exactly one
definition in the codebase. The row identity for sidecar and ordering is
`mw:<task_id>:<step_id>:<transitionID>`, where `<transitionID>` is the
immutable row id of the newest `task_step_transitions` row that moved the
task into the current step (the transactional ledger
`recordManualStepTransition` writes,
`apps/backend/internal/task/service/service_workflow.go`; built by
`ManualStepWaitRowID`,
`apps/backend/internal/task/models/needs_you_waiting.go`); a missing ledger
row falls back to `:0`, and a later episode produces a different identity, so
the prior sidecar state does not apply.

Because the row set is derived, resolution converges by re-reading: leaving
the step, starting a turn, completing, or archiving the task all change the
read result, and the inbox controller re-reads on the episode event
`task.manual_step_entered` alongside the session-state frames
(`apps/web/hooks/domains/needs-you-inbox/use-needs-you-inbox-controller.ts`),
so a sessionless wait is also signaled. The Threads needs-action classifier
(`apps/web/lib/coordinator/attention.ts`) is deliberately untouched; the
asserted difference of AC-003.8 is a test, not a classifier change.

## Unified notification bridge

Before this change, the relay re-published retained occurrences on its own
wire (`ws.ActionRemoteNotification`), consumed only by the sidebar
notifications module. The unification removes that dedicated path:

- The relay keeps its subscription, reconciliation, and dedup key (instance,
  event, occurrence), but instead of broadcasting its own action it feeds
  each recovered occurrence into the shared notification service as an event
  of the same kind the remote emitted, carrying an origin tag with the
  instance identity. Dedup guarantees one delivery per occurrence, so replay
  and reconciliation never re-notify.
- Provider routing, per-kind subscriptions, sound, and browser or desktop
  native notifications therefore behave identically for relayed and local
  events with no remote-specific presentation code: a kind quiet by default
  stays quiet when relayed, a subscribed kind notifies identically when
  relayed (AC-004.1).
- The frontend handles origin-tagged notifications in the existing handler
  (`apps/web/lib/ws/handlers/notifications.ts`): a notification shows its
  origin label but exposes no open action. Navigation to a relayed task
  happens by opening its Inbox row; informational kinds, which have no Inbox
  row, are reached by switching to the origin instance through the standard
  instance-switch path (`apps/web/lib/instances/switch-instance.ts`,
  `apps/web/lib/links.ts`). Viewing suppression compares the active instance
  and task, not only the local task id (AC-004.3, AC-004.4). The desktop
  native notification plugin stays at its current version, which does not
  deliver desktop click callbacks; adding an open action would require a
  plugin upgrade and is explicitly out of scope.
- The sidebar notifications module, the remote-notifications slice, and the
  module's REST read and dismiss endpoints are removed. The relay store
  (`remote_notification_occurrences`,
  `apps/backend/internal/instances/store/sqlite/store.go:72`) remains solely
  as the deduplication and recovery ledger for notification delivery; its
  read and dismissed columns stop being presentation state (AC-004.5).
- The relayed occurrence set becomes v2: `session.turn_finished`,
  `session.clarification_requested`, `office.inbox_item`, and
  `task.manual_step_entered` (`relay.go` allowed list). A needs-you relayed
  occurrence also refreshes the Inbox read, exactly as its locally emitted
  twin does (AC-004.2).

## Remote waiting rows

Remote manual-step rows reuse the per-instance composition the landed
remote-questions change established, adding one remote read: the aggregate
needs-you endpoint this package introduces locally
([below](#aggregated-read-contract)) is versioned API surface, so a remote
running this version exposes its own waiting rows through it. Composition,
ordering, and degraded states follow the landed change's mechanism:

- The local Inbox read attaches one page per enabled registered instance,
  read live through that instance's proxy port; remote waiting rows carry
  the same fields as local ones plus the origin tag.
- Resolution is live: a row disappears when the remote no longer reports the
  wait, with no local derivation (AC-005.6).
- An unreachable instance, an auth-on instance, or a remote version without
  the waiting read renders that instance's section in the degraded state the
  landed change defines, never an error for the whole read and never a dead
  row; local rows still list (AC-005.6, AC-005.7).
- Workspace mapping for remote reads follows whatever the landed change
  settled for questions; this package does not reopen it.

## Sidecar generalization

The per-user sidecar (`clarification_inbox_sidecar`,
`apps/backend/internal/task/repository/sqlite/clarification_inbox_sidecar.go:18`,
primary key `(user_id, pending_id)`) is reused for local waiting rows through
the `mw:` prefixed identity above. Dismiss, snooze (1h, 4h, 24h), restore,
idempotence, expiry semantics, and the hidden enumeration all keep their
existing repository methods; the inbox read joins the sidecar by row id
instead of by bare `pending_id`. Remote rows do not touch the local sidecar:
per the landed change, their sidecar state lives on the remote and travels
with the forwarded write.

## Aggregated read contract

One endpoint family serves the needs-you tab, registered under the existing
`needsYouInboxEnabled` gate next to the clarification-inbox routes
(`apps/backend/internal/clarification/handlers.go:218`, wired at
`apps/backend/internal/backendapp/helpers.go:1929`):

- `GET /api/v1/needs-you-inbox` merges question rows and waiting rows into
  one page: rows carry a `kind` discriminator, question rows keep their
  existing shape, waiting rows carry task, step, and episode reference time.
  With federation enabled, remote rows are read separately for each instance
  through `GET /api/v1/instances/:id/needs-you-inbox` (and `/hidden`) and
  composed in the SPA; this response never carries remote rows, so a failed
  instance degrades only its own section.
- `GET /api/v1/needs-you-inbox/hidden` generalizes the hidden enumeration
  across local kinds with the same workspace-wide totals discipline.

The local merge fetches the bounded page from each source with the same
limit, orders by (reference time, id), and pages the merged stream; the
response carries rows, count, hidden count, and `has_more` from that single
merge, so every producer of the count (list read, boot summary, badge) calls
the same merge helper with the same bound. `InboxBootSummary`
(`apps/backend/internal/clarification/inbox_handlers.go:660`, served from
`apps/backend/internal/backendapp/boot_state_routes.go:629`) is extended to
the merged count so the pre-open badge (AC-005.8) holds across kinds; the
landed change's remote count decision carries over unchanged for remote rows.
Workspace authorization mirrors the existing handlers
(`AuthorizeWorkspaceScope`); the workspace filter applies to local rows only,
remote rows being panel-level by contract. A local read failure renders the
existing error state; degraded remote instances do not fail the local read.

The existing `/api/v1/clarification-inbox` endpoints remain for the history
tab and the remote forwarding contract the landed change established; their
contract is unchanged.

## Frontend surfaces

- The page client (`apps/web/app/needs-you-inbox/needs-you-inbox-page-client.tsx`)
  renders three row variants in the needs-you tab. A waiting row names the
  task and step and offers open-task, dismiss, and snooze; it has no inline
  answer affordance. A remote row shows its origin label and behaves like
  the landed remote question rows: live, answerable or openable through the
  standard instance-switch path, degraded when its instance is unavailable.
- The needs-you-inbox slice and controller
  (`apps/web/lib/state/slices/needs-you-inbox/`,
  `apps/web/hooks/domains/needs-you-inbox/use-needs-you-inbox-controller.ts`)
  move to the new endpoint; refresh triggers add the local episode event and
  the needs-you relayed kinds through the unified handler; the boot seed
  consumes the extended boot summary.
- The sidebar notifications module is removed; the push surfaces handle
  origin-tagged events uniformly through the existing notification settings,
  which gain no remote-specific switches. No notification open action is
  added: the Inbox row is the only navigation surface for relayed needs-you
  kinds, and informational relayed kinds remain push-only.
- Copy is added through `t()` in every shipped locale
  (`needsYouInbox.json`, `instances.json`), using `pnpm run i18n:zh-hant` for
  the Traditional Chinese pair, with no Unicode em dash.

## Flags and profiles

No new runtime flag is introduced. `features.NeedsYouInbox` keeps gating the
destination and its reads; `features.multiInstance` keeps gating federation
and therefore the existence of relayed notifications and remote rows; the
episode notification rides the notification pipeline's provider
configuration exactly as clarification notifications do. Both flags' profile
defaults are unchanged.

## Failure and recovery behavior

| Symptom | Cause | Behavior |
| --- | --- | --- |
| Duplicate remote notification | Replay plus reconciliation re-observing one occurrence | Dedup key (instance, event, occurrence) admits exactly one delivery |
| Missed remote notification | Subscription gap | Recovered by the reconciliation poll within one interval, dedup-suppressed if seen |
| Remote waiting rows absent from one instance | Remote version predates the occurrence or the waiting read | Absence and degraded section, never an error; informational kinds from that instance notify per their own subscriptions |
| Degraded instance during an Inbox read | Instance unreachable or auth-on | That instance's section degrades; local rows and other instances still list |
| Two notifications for one chain | Automated advance through consecutive manual steps | Deliberate: each entry is a new episode owed a different action |
| Waiting row stranded | Source of truth is derived | Cannot persist: any episode exit changes the read result |
| Badge and list disagree | Divergent producers | Both call the one merge helper with the same bound |

## Observability

No new metric families. Notification delivery keeps the existing
`notifications` provider logging; the relay keeps its occurrence counters. An
origin-tagged frame is identifiable in existing structured logs by its event
kind and origin, which is the debugging surface the federation module already
documents.

## Related ADRs

- [ADR 2026-10-28-multi-instance-federation](../../../decisions/2026-10-28-multi-instance-federation.md),
  revised by the landed remote-questions change and by this change: the
  relayed set gains `task.manual_step_entered`; relayed occurrences notify
  through the shared pipeline with origin tags instead of a dedicated quiet
  module; the sidebar notifications module is removed; needs-you rows are
  read live through the proxy; the data-isolation invariant is unchanged.
