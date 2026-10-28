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
  (instance, event, occurrence); re-publishes via `BroadcastToUser` only.
- `store/sqlite/` — registry and relayed-occurrence tables (requiredstores +
  storeconformance registered).

## Invariants

- Only `internal/backendapp/` assembles this package (registry routes,
  ProxyManager, RelayManager); the flag-off path must stay behavior-identical.
- The proxy port deliberately skips the global auth middleware and self-checks the
  local session (HMAC bridge capability cookie). It delegates origin checks to
  `httpmw.AllowedOrigin` and must never weaken the WS origin policy.
- Remote rows never enter local task tables or board caches; instance identity
  rides every cross-instance ID, key, and dedup tuple (stats-sync precedent).
- Relay publishes only into the sidebar notifications module: no toast, browser
  native notification, or sound; local notification behavior stays untouched.
- Long-lived goroutines follow Start/Stop + WaitGroup + package goleak
  `TestMain`; backoff uses `time.NewTimer` + `select`, never `time.Sleep`.
