# ADR-2026-10-28-multi-instance-federation: Local backend as the multi-instance federation client

**Status:** accepted (amended 2026-10-28)
**Date:** 2026-10-28
**Area:** backend

## Context

Users run several Kandev instances (for example one on the local machine, one on a GPU workstation) and until now had to keep one browser panel per instance, switching manually; remote session notifications were only visible in the remote instance's own panel. A browser cannot connect to a second instance directly: the WebSocket same-origin check (`internal/gateway/websocket/origin.go`, anti cross-site WS hijacking) and cross-site cookie policy (port-scoped cookies, SameSite) both block it, and weakening those trust policies is not acceptable. Change `add-multi-instance-management` (archived `openspec/changes/archive/2026-10-28-add-multi-instance-management/`) introduced the capability and needed three durable architectural commitments to keep it safe.

## Decision

1. **The local backend is the federation client.** All cross-instance traffic flows through the local backend: it owns the per-owner instance registry, dials remote instances itself, and re-publishes remote events to local browser clients. The browser never talks to a remote instance directly; it talks to a local per-instance proxy port that the backend owns.
2. **Proxy form is one dedicated local port per enabled instance** (same hostname, dynamically allocated, persisted for rebinding, falling back to `:0` on conflict). This passes the existing `httpmw.AllowedOrigin` policy unchanged — it compares hostnames only, so the same host on a different port is already allowed — and reuses the SPA's existing port-separation mechanism (`__KANDEV_API_PORT`, `resolveUrl(path, baseUrl)`, WS URL derived from the API base), so no API call site or origin/CORS policy changes. The proxy port deliberately skips the global auth middleware and self-checks the local session via the short-lived HMAC bridge capability cookie when auth is on.
3. **Remote data isolation invariant:** remote task/session rows never enter local task tables or board caches, and every cross-instance ID, key, and dedup tuple carries instance identity — the relay dedup key is (instance, event, occurrence), following the stats-sync precedent. Remote semantic notifications are re-published only into the dedicated sidebar module (no toast, browser-native notification, or sound; local notification behavior unchanged).

  **Amendment (2026-10-28, cross-instance inbox):** Decision 3's "only into the
  dedicated sidebar module" wording is amended to also allow the local
  Needs-you Inbox to compose answerable clarification state read from a
  registered remote instance: the local backend fans out to the remote's own
  clarification-inbox endpoints (the same direct-dial, PAT-injected trust face
  as the relay), aggregates one instance page per enabled remote, and the Inbox
  renders one answerable section per instance whose submissions forward back to
  the remote. The relay frame is additionally consumed as a re-read bump for
  that instance's page and nothing else. The isolation invariant is unchanged
  and still binding: remote rows never enter local task tables, board caches,
  or clarification sidecar tables; the composed remote page is a computed view
  (change `surface-remote-inbox-bundles`, capability `cross-instance-inbox`).

  **Revision (2026-10-28, unified notifications + live-read rows):**
  the "re-published only into the dedicated sidebar module (no toast, browser
  native notification, or sound)" clause no longer describes the terminal
  state. The decided end state is one notification logic for local and relayed
  events: relay-recovered occurrences carry an instance origin tag into the
  same shared notification pipeline as local events (`internal/notifications`),
  with the same per-kind subscriptions, quiet-by-default behavior, view
  suppression, and provider set as a local event of that kind, and an origin
  label but no open action on the notification (navigation to a relayed task
  goes through its Inbox row; informational relayed kinds are reached by
  manually switching to the origin instance); the dedicated sidebar
  notifications module is removed, and the relay store retains only the
  (instance, event, occurrence) deduplication and recovery ledger. That end
  state is realized by the needs-you-inbox-waiting-rows change
  (REQ-UI-NEEDS-YOU-INBOX-003 through -005), whose relayed set is v2
  (`session.turn_finished`, `session.clarification_requested`,
  `office.inbox_item`, `task.manual_step_entered`); the quiet
  sidebar-module behavior is retired with it. The
  data-isolation invariant is unchanged: remote rows are read live and never
  enter local task tables.

## Consequences

- The trust boundary stays where it was: no CORS or WS origin policy is weakened; the proxy is first-party code that authenticates browser clients against the local session and injects the registered PAT toward the remote.
- Adding the capability required no API-client rewrite; the SPA switches instances by full-page reload with the API/WS base pointed at the instance's proxy port (single active instance model).
- Costs: one listening port per enabled instance (conflict handling and degraded health state required), and host-bound surfaces (session port previews, desktop-privileged open-in-editor/folder actions) cannot cross instances and degrade with an explicit unavailable-for-remote affordance.
- The capability ships default-off behind `features.multiInstance` in every shipped profile and is graduated per the runtime-feature-flags procedure. Health probes carry no credentials, so an auth-on remote reports unreachable until probes learn to send the PAT (recorded in the multi-instance-management spec's Known Failure Modes).
- The durable behavioral contract lives in `openspec/specs/multi-instance-management/spec.md` and `openspec/specs/cross-instance-notifications/spec.md`; no separate `docs/specs/` product system applies, because local notification, board, and auth behaviors are unchanged and the new capabilities are fully owned by the two OpenSpec specs. Revision (2026-10-28): the cross-instance-inbox and needs-you-inbox-waiting-rows extensions each change one local surface, the Inbox row set and the notification presentation path, so their product contracts live in `docs/specs/ui/` (`cross-instance-inbox.md` now; the waiting-rows requirements REQ-UI-NEEDS-YOU-INBOX-003 through -005 live in `needs-you-inbox-waiting-rows.md`); the two OpenSpec specs remain authoritative for federation transport, dedup, and occurrence recovery.

## Alternatives Considered

- **Browser connects to remote instances directly.** Rejected: blocked by design by the WS origin check and cross-site cookie policy; making it work would require weakening anti-hijacking protections.
- **Path-prefix proxying through the main port** (`/i/<instance>/api/...`). Rejected: requires rewriting every SPA API path construction and per-endpoint rewriting of absolute URLs inside payloads, a strictly larger frontend change than pointing the existing single `resolveUrl` base at a different port.
- **Read-only remote views with jump-out links.** Rejected by explicit user decision: v1 operates remote tasks directly through the full proxy.
- **Merging remote rows into local task tables for a unified board.** Rejected: violates the data-isolation discipline (stats-sync precedent); the single-active-instance full-reload model keeps local state clean.
