# instances — Multi-instance federation

Scoped guidance for `internal/instances/`, the default-off (`features.multiInstance`)
subsystem that lets one panel operate other Kandev installs. Requirements and failure
modes live in `openspec/specs/multi-instance-management/spec.md` and
`openspec/specs/cross-instance-notifications/spec.md`; the architecture rationale is
`docs/decisions/2026-10-28-multi-instance-federation.md`; the user guide is
`docs/public/multi-instance.md`.

## Layout

- `service.go` / `models.go` — per-owner instance registry (`notification providers`
  ownership pattern; `*NotFound` sentinels; credentials via `internal/secrets`).
- `health.go` — 90s health poller (healthpoll shape) with `MinRemoteVersion` floor;
  health is process-memory only. Probes send no credentials, so an auth-on remote
  reports unreachable.
- `proxy/` — per-enabled-instance reverse proxy on its own local port: HTTP
  `/api/v1/**` forwarding with PAT injection, bidirectional WS bridge, structured
  remote-unreachable errors, HMAC bridge capability cookie when auth is on.
- `relay/` — persistent per-instance WS subscription plus 60s reconciliation poll
  for remote semantic notification occurrences; dedup key
  (instance, event, occurrence); relayed set v2 (`session.turn_finished`,
  `session.clarification_requested`, `office.inbox_item`,
  `task.manual_step_entered`); each deduped occurrence goes to the
  notification service (`Notifier` ->
  `notifications.Service.HandleRelayedOccurrence`) addressed to the instance
  owner with the origin identity on the message, never to a remote-only
  presentation path.
- `remoteinbox/` — request-scoped fan-out client that reads one remote's own
  clarification-inbox and aggregated needs-you-inbox endpoints across its
  credential-visible workspaces into single aggregated instance pages (10s
  total deadline, per-workspace serial) and forwards respond/sidecar writes
  back verbatim. No goroutines, no lifecycle; degraded reasons are the closed
  set unreachable|unauthorized|inbox_disabled|error.
- `store/sqlite/` — registry and relayed-occurrence tables (requiredstores +
  storeconformance registered); the occurrence table is the dedup and
  recovery ledger only, with no presentation read or dismiss methods.

## Invariants

- Only `internal/backendapp/` assembles this package (registry routes,
  ProxyManager, RelayManager); the flag-off path must stay behavior-identical.
- The proxy port deliberately skips the global auth middleware and self-checks the
  local session (HMAC bridge capability cookie). It delegates origin checks to
  `httpmw.AllowedOrigin` and must never weaken the WS origin policy.
- Remote rows never enter local task tables, board caches, or clarification
  sidecar tables; instance identity rides every cross-instance ID, key, and
  dedup tuple (stats-sync precedent). The inbox fan-out is the one sanctioned
  read of remote clarification and needs-you state: its aggregated instance
  pages are computed views only, and every remote respond/sidecar write is
  forwarded to the owning instance rather than resolved locally (ADR amendment
  2026-10-28).
- Relayed occurrences ride the shared notification pipeline with an origin
  tag: the same per-kind subscriptions, providers, sound, and browser or
  desktop native notifications as a local event of that kind, addressed to the
  instance owner, never recorded into the local occurrence history, and never
  surfaced by a remote-only presentation path (the former sidebar
  notifications module is removed). The relay store is the deduplication and
  recovery mechanism only.
- Long-lived goroutines follow Start/Stop + WaitGroup + package goleak
  `TestMain`; backoff uses `time.NewTimer` + `select`, never `time.Sleep`.
