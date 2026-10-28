---
status: draft
system: ui
created: 2026-10-28
owners:
  - nova28
---

# Needs-you Inbox Waiting Rows Requirements

## Overview

The Inbox answers one question: what is waiting on me. It currently answers it
for exactly one kind of wait, a clarification bundle, and only for the local
instance's active workspace. Two waits stay invisible, and both were measured
rather than hypothesized:

- A task that enters a manual workflow step (a step that does not auto-start an
  agent on entry) emits no notification and no inbox row. The operator must
  watch the board to learn that a task now owes them a decision, a review, or a
  nudge. The only push signal the product sends for waiting input is the
  clarification notification.
- A remote federated instance's needs-you events surface only in the sidebar
  notifications module, through a presentation path that exists only for
  remote events. The product decisions of 2026-10-28 revise this: the needs-you
  Inbox becomes the single waiting surface, and remote and local events use one
  notification logic.

This capability makes the Inbox the single waiting surface and unifies remote
notification with local. It adds local manual-step waiting rows, remote
manual-step waiting rows, and a unified read that keeps the guarantee the
Inbox was built on: the count and the list come from one read.

UI owns the destination, the row kinds, the count, and the per-user hiding
sidecar, extending the same contract as
[REQ-UI-NEEDS-YOU-INBOX-001](needs-you-inbox.md). Workflow step semantics stay
owned by the workflow system and are consumed unchanged. Remote clarification
rows, their live read through the instance proxy, in-place answering, sidecar
forwarding, and degraded-instance presentation are owned by the in-flight
change that delivers them (Kandev task `32e4f7e5`, amending
[ADR 2026-10-28-multi-instance-federation](../../../decisions/2026-10-28-multi-instance-federation.md));
this capability builds on that landed base and links to it without copying it.
Relay transport, dedup, and occurrence recovery stay owned by the federation
specifications in `openspec/specs/cross-instance-notifications/`.

## Terminology

- **Manual step:** A workflow step that does not auto-start an agent on entry
  and does not complete its task on entry. The shared step-semantics predicate
  of the workflow system decides this; the Inbox does not redefine it.
- **Waiting episode:** The interval during which a task of the active workspace
  sits in a manual step with no executing or queued turn. An episode ends when
  the task leaves the step, a turn starts, or the task completes or archives.
- **Needs-you occurrence:** An event that asks the operator to act: a
  clarification request, a manual-step wait beginning, or an Office inbox
  item. A turn finishing is informational and is not one.
- **Origin tag:** The instance identity a relayed event carries into the local
  notification pipeline, used for labeling and navigation, never for choosing
  a different presentation rule.

## Requirements

### REQ-UI-NEEDS-YOU-INBOX-003: Manual-step waiting rows

**Intent:** A task that owes the operator a human action because it entered a
manual step shall be as visible as a task blocked on a question.

**User story:** As an operator, I want every task waiting on me in a manual
step to appear in the Inbox and to alert me when it starts waiting, so that a
task never sits unnoticed until I happen to look at its board column.

#### Acceptance criteria

- **AC-UI-NEEDS-YOU-INBOX-003.1:** When a task of the active workspace is in a
  manual step and no turn of that task is executing or queued, the Inbox shall
  list exactly one waiting row naming the task and the step. Given a fixture of
  tasks in auto-start steps, and a fixture of tasks in complete-on-enter
  steps, those tasks shall produce no waiting row.
- **AC-UI-NEEDS-YOU-INBOX-003.2:** A waiting row shall be removed without a
  manual reload when its episode ends: the task leaves the step, a turn
  starts, or the task completes or archives. Row removal shall converge by
  re-reading the wait state; no episode exit shall be able to strand a row by
  emitting no event.
- **AC-UI-NEEDS-YOU-INBOX-003.3:** A task shall hold at most one waiting row
  per episode. When an episode ends and a later episode begins, the later
  episode shall produce a fresh row that does not inherit the previous
  episode's dismiss or snooze state.
- **AC-UI-NEEDS-YOU-INBOX-003.4:** When a waiting episode begins and its cause
  is not the operator's own move of that task, the system shall deliver the
  same notification treatment a clarification bundle receives: subscribed
  notification providers fire, and the push presentation honors the existing
  sound, browser-native notification, and viewing-suppression behavior.
  Enabling the Inbox flag shall not emit notifications for episodes that
  already began.
- **AC-UI-NEEDS-YOU-INBOX-003.5:** An episode begun by the local operator's
  own move of that task shall not fire the push notification, and shall still
  be listed.
- **AC-UI-NEEDS-YOU-INBOX-003.6:** Dismiss, snooze, restore, snooze duration
  choices, per-user scoping, idempotence, expiry reappearance, and hidden-row
  enumeration shall apply to waiting rows exactly as
  AC-UI-NEEDS-YOU-INBOX-001.22 through .25, .32, .33, and .37 apply to
  bundles.
- **AC-UI-NEEDS-YOU-INBOX-003.7:** The episode-begin notification shall be
  delivered by the notification pipeline whether or not the Inbox destination
  flag is enabled. The waiting row itself shall require the flag, and where
  the flag is disabled the notification shall remain actionable through the
  task surfaces that exist today.
- **AC-UI-NEEDS-YOU-INBOX-003.8:** Tasks waiting in a manual step shall appear
  in the Inbox and shall not appear in the Threads needs-action preset, whose
  classifier is unchanged by this capability; a test shall assert that
  difference.

### REQ-UI-NEEDS-YOU-INBOX-004: One notification logic for remote events

**Intent:** A relayed event shall notify the operator through exactly the
logic a locally emitted event of the same kind uses; origin shall never select
a different presentation path.

**User story:** As an operator of several Kandev instances, I want remote
events to alert me the way local ones do, so that one mental model and one
set of notification settings covers every instance I run.

#### Acceptance criteria

- **AC-UI-NEEDS-YOU-INBOX-004.1:** A relayed occurrence shall be delivered
  through the same notification pipeline as a locally emitted event of the
  same kind: the same event-kind subscriptions, the same provider set, the
  same sound, browser-native notification, and toast behavior, and the same
  default-subscription state. A kind that is quiet by default locally shall be
  quiet by default when relayed, and a kind the operator subscribed to shall
  notify identically when relayed.
- **AC-UI-NEEDS-YOU-INBOX-004.2:** A relayed needs-you occurrence shall
  additionally refresh the Inbox read, exactly as the locally emitted event of
  the same kind does. A relayed informational occurrence shall not create or
  alter an Inbox row.
- **AC-UI-NEEDS-YOU-INBOX-004.3:** A relayed notification that the operator
  opens shall navigate to its task by the standard instance-switch path when
  its origin is a remote instance, and shall navigate locally otherwise. The
  origin shall be visible as a label on the notification and, for Inbox rows,
  on the row.
- **AC-UI-NEEDS-YOU-INBOX-004.4:** Viewing suppression shall apply across
  origins by the same rule: a notification for a task shall be suppressed when
  the panel is currently viewing that task, on the local instance or through
  the active remote instance.
- **AC-UI-NEEDS-YOU-INBOX-004.5:** The dedicated remote sidebar notifications
  module shall be removed. No relayed event shall be surfaced by a
  presentation path that exists only for remote events; the relay store
  remains solely the deduplication and recovery mechanism for notification
  delivery, and re-observing an occurrence by replay or reconciliation shall
  not re-notify.
- **AC-UI-NEEDS-YOU-INBOX-004.6:** With federation disabled, no relayed
  notification exists and local notification behavior shall be identical to
  before this capability.

### REQ-UI-NEEDS-YOU-INBOX-005: Unified waiting read and count

**Intent:** Widening the row set shall not loosen the guarantee that the count
and the list come from one read.

**User story:** As an operator, I want the Inbox count to equal what the Inbox
lists across every kind of wait, so that the number I glance at is never a
different truth from the list I would open.

#### Acceptance criteria

- **AC-UI-NEEDS-YOU-INBOX-005.1:** The needs-you list, its count, the hidden
  count, and the truncation indicator for all row kinds shall come from one
  aggregated response of one read path. The rendered count shall equal the
  number of listed rows across kinds, including hidden rows' exclusion and
  truncation bounds, extending AC-UI-NEEDS-YOU-INBOX-001.12, .23, and .40 to
  the merged set.
- **AC-UI-NEEDS-YOU-INBOX-005.2:** Rows shall be ordered oldest wait first by
  each row's reference time ascending, the bundle creation time for question
  rows, the episode start for waiting rows, and the remote row's own reference
  time for remote rows, with ties broken by a stable row identity ascending.
  The order shall be total and reproducible for any fixture.
- **AC-UI-NEEDS-YOU-INBOX-005.3:** A single read shall return at most the
  bounded page across kinds. A row hidden by the operator's own dismiss or
  snooze shall not consume a page slot regardless of kind. When more listable
  rows exist than the page returns, the Inbox shall indicate truncation,
  extending AC-UI-NEEDS-YOU-INBOX-001.11 to the merged set.
- **AC-UI-NEEDS-YOU-INBOX-005.4:** When no row of any kind is listed, no count
  shall be rendered, and the count shall reach absent only when nothing
  listable remains across kinds, extending AC-UI-NEEDS-YOU-INBOX-001.13; a
  truncated page shall render the capped indicator of
  AC-UI-NEEDS-YOU-INBOX-001.14.
- **AC-UI-NEEDS-YOU-INBOX-005.5:** Switching the active workspace shall
  replace the local rows, question and waiting, with the new workspace's rows
  and shall retain remote rows, so a remote wait is never hidden by workspace
  choice.
- **AC-UI-NEEDS-YOU-INBOX-005.6:** A remote manual-step waiting row shall be
  read live from its instance by the same per-instance composition the landed
  remote-questions change established, shall resolve when the remote no longer
  reports the wait, and shall render a degraded per-instance state, not an
  error and not a dead row, when its instance is unreachable or runs a version
  without the waiting read. An instance that cannot be read shall not prevent
  local rows from listing.
- **AC-UI-NEEDS-YOU-INBOX-005.7:** If the local aggregated read fails, the
  Inbox shall render the error state of AC-UI-NEEDS-YOU-INBOX-001.21, shall
  render no count, and shall not present a partial list of one kind as a
  complete list. Degraded remote instances are not local read failures.
- **AC-UI-NEEDS-YOU-INBOX-005.8:** The sidebar count shall be present and
  correct across kinds before the Inbox has been opened in the current browser
  session, extending AC-UI-NEEDS-YOU-INBOX-001.34 to the merged set.
- **AC-UI-NEEDS-YOU-INBOX-005.9:** All Inbox copy added by this capability
  shall be localized in every shipped locale and shall contain no Unicode em
  dash.

## Supersession upon activation

While this document is a draft, REQ-UI-NEEDS-YOU-INBOX-001 remains
authoritative as written. When this document activates, it supersedes, in
part:

- AC-UI-NEEDS-YOU-INBOX-001.5 and .6, whose "and no others" row-set
  exclusivity and waiting-session fixture become statements about question
  rows within a wider row set.
- AC-UI-NEEDS-YOU-INBOX-001.20, whose not-counted list gains the entries of
  the empty state this capability defines.
- AC-UI-NEEDS-YOU-INBOX-001.27, whose parity fixture gains the asserted
  difference of AC-UI-NEEDS-YOU-INBOX-003.8.

No other acceptance criterion of REQ-UI-NEEDS-YOU-INBOX-001 changes meaning.
The no-disturb rule for relayed events in
`openspec/specs/cross-instance-notifications/spec.md` is revised by this
capability's implementation rather than by this document.

## Out of scope

- **Remote clarification rows.** Their live read, in-place answering, sidecar
  forwarding, workspace mapping, and degraded states are owned by the
  in-flight remote-questions change; this capability consumes that landed
  base unchanged.
- **Office inbox items in the needs-you Inbox.** They notify through the
  unified logic like any kind; they surface in the Office inbox, matching
  local behavior.
- **History entries for waiting or remote rows.** The history tab remains
  resolved local clarification bundles.
- **A relay store for presentation.** The store keeps only its deduplication
  and recovery role; no row or badge is derived from it.
- **Backfill of notifications on first enable.** Enabling federation or the
  Inbox shall not emit notifications for waits that already began.
- **Remotes that do not emit the manual-step occurrence or expose the waiting
  read.** Older remote versions simply produce no remote waiting rows and no
  manual-step notifications.
- **Re-asking, re-notifying, or retention changes for bundles**, which remain
  exactly as REQ-UI-NEEDS-YOU-INBOX-001 and its exclusions state them.
