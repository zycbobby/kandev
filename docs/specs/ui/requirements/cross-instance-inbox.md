---
status: active
system: ui
created: 2026-09-12
owners:
  - nova28
---

# Cross-instance Inbox Requirements

## Overview

A remote instance's blocked clarification questions were answerable only in
that instance's own panel: the local sidebar carried a relayed notification
that is not answerable, and the local Needs-you Inbox listed only the active
workspace's bundles. An operator not watching the remote panel would miss a
blocked remote agent for exactly as long as the local failure the Inbox
exists to remove. This requirement makes the local Inbox compose one
answerable section per enabled registered remote instance, answered in place
through the local backend's forward routes, with no remote row entering any
local table. It builds on REQ-UI-NEEDS-YOU-INBOX-001 (the local Inbox
contract) and the multi-instance federation ADR (local backend as federation
client); the companion system design is
`docs/specs/ui/system-design/cross-instance-inbox-01.md` plus the trigger and
copy sections amending the needs-you-inbox-02/03 designs.

## Requirements

### REQ-UI-NEEDS-YOU-INBOX-002: Remote instance rows

**Intent:** While both `features.needsYouInbox` and `features.multiInstance`
are on and the panel views the local instance, the Inbox shall compose, for
every enabled registered remote instance, one answerable section holding that
remote's blocked clarification bundles, answerable in place, without writing
any remote row into a local task table, board cache, or clarification sidecar
table.

**User story:** As an operator registered with remote Kandev instances, I want
their blocked questions listed and answerable in my local Inbox, so that a
remote agent is never blocked just because I am not watching that panel.

#### Acceptance criteria

- **AC-UI-NEEDS-YOU-INBOX-002.1:** The local backend shall read each enabled
  registered remote's own clarification-inbox surface over its registered
  credential, aggregating the credential-visible remote workspaces into one
  instance page ordered by bundle creation time ascending and bounded by the
  same default page size as the local read. Rows shall carry instance and
  origin-workspace identity.
- **AC-UI-NEEDS-YOU-INBOX-002.2:** Answering or rejecting a remote row shall
  be forwarded through the local backend to the remote's own respond endpoint,
  preserving the existing resolution semantics (first-write-wins, idempotent
  replay, task resume, and the 200 win/loss and 409 not-active outcomes)
  unchanged. Row removal and count change shall land only via that instance
  page's re-read, without navigation.
- **AC-UI-NEEDS-YOU-INBOX-002.3:** Dismiss, snooze, and restore on a remote
  row shall be forwarded to the remote's own sidecar endpoints, so the sidecar
  state stays on the remote under the credential identity and a pending
  identifier is always addressed through its owning instance.
- **AC-UI-NEEDS-YOU-INBOX-002.4:** An unreachable, credential-required, or
  inbox-disabled remote shall render that instance's degraded state naming the
  instance and offering a retry, shall not render an answer form, and shall
  not affect the local page, other instances' sections, or their counts.
- **AC-UI-NEEDS-YOU-INBOX-002.5:** Every remote row shall offer a control
  that switches the panel to the row's instance via the standard
  instance-switch path and opens the referenced task's route.
- **AC-UI-NEEDS-YOU-INBOX-002.6:** With either feature flag off, no remote
  read shall be issued and the Inbox, badge, and notification behavior shall
  be identical to before this requirement.
