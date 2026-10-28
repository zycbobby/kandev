---
status: draft
system: ui
requirements:
  - REQ-UI-NEEDS-YOU-INBOX-001
---

# Cross-instance Inbox System Design 01: Data and Contracts

Implements REQ-UI-NEEDS-YOU-INBOX-002
(`docs/specs/ui/requirements/cross-instance-inbox.md`). The architecture
rationale is the multi-instance federation ADR (amended 2026-10-28): the
local backend is the federation client, and this surface is its one
sanctioned read of remote clarification state.

### Cross-instance instance pages (REQ-UI-NEEDS-YOU-INBOX-002)

`GET /api/v1/instances/:id/clarification-inbox` and `.../hidden` serve one
aggregated instance page per enabled registered remote while
`features.needsYouInbox` and `features.multiInstance` are both on. The local
backend fans out to the remote's own `GET /api/v1/workspaces` and then its
`GET /api/v1/clarification-inbox[?workspace_id=...]` per credential-visible
workspace (serial, one 10s total deadline per instance read), merges rows by
`created_at` ascending with `pending_id` as the tie-break, caps at the same
default limit as the local read, and returns one envelope:

```json
{
  "instance_id": "...",
  "instance_name": "...",
  "status": "ok",
  "degraded_reason": "unreachable | unauthorized | inbox_disabled | error",
  "bundles": [{ "...local bundle shape...": "", "instance_id": "...", "workspace_id": "..." }],
  "count": 1,
  "hidden_count": 0,
  "total": 0,
  "next_snooze_expiry": null,
  "has_more": false
}
```

A degraded read is HTTP `200` with `status: "degraded"`, empty `bundles`, and
one closed-set reason: `unreachable` (dial or deadline failure),
`unauthorized` (remote 401/403, or the local credential secret cannot be
revealed), `inbox_disabled` (the remote's inbox routes are absent, i.e. its
`features.needsYouInbox` is off), `error` (any other non-2xx). `hidden_count`,
`total`, and `next_snooze_expiry` aggregate across the remote's workspaces
(sum, sum, earliest). Writes forward verbatim: respond passes the remote's
200/409 through untouched, sidecar PUT/DELETE pass 204/400/404 through, and a
transport failure answers `502 {"error":"remote_unreachable","instance":name}`.
A pending identifier is always addressed through its owning instance, so
identical identifiers on different instances never collide. Remote rows never
enter local tables; the instance page is a computed view.
