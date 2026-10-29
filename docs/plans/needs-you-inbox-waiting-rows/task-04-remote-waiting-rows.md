---
id: "04-remote-waiting-rows"
title: "Remote waiting rows through the landed composition"
status: done
wave: 4
depends_on: ["02-local-waiting-rows", "03-unified-notification-bridge"]
plan: "plan.md"
requirements:
  - REQ-UI-NEEDS-YOU-INBOX-004
  - REQ-UI-NEEDS-YOU-INBOX-005
acceptance_criteria:
  - AC-UI-NEEDS-YOU-INBOX-004.3
  - AC-UI-NEEDS-YOU-INBOX-005.6
system_design:
  - ../../specs/ui/system-design/needs-you-inbox-waiting-rows.md
---

# Task 04: Remote waiting rows through the landed composition

## Summary

Move the landed per-instance Inbox composition onto the merged remote read:
the aggregate `needs-you-inbox` endpoint that task 02 introduced locally
becomes remote API surface, so a remote running this version reports its own
manual-step waits. The remote controller's question-only read is replaced by
that merged read (one fan-out per instance page; the question-only client
functions became dead and were removed), rows render with the origin label,
resolve live, and degrade per instance when unreachable or too old.

## In scope

- Versioned exposure of the waiting-row read through the remote API consumed
  by the landed composition; workspace mapping follows the landed change's
  settled decision.
- Frontend remote waiting row variant: origin label, open by instance
  switch, degraded per-instance section per the landed pattern.
- Merged count including remote waiting rows through the landed count
  decision.
- Multi-instance e2e coverage for the row, its resolution, the degraded
  state, and the unified notification of a relayed manual-step occurrence.

## Out of scope

- Local waiting rows, the aggregate endpoint itself, and sidecar behavior
  (task 02); relayed notification treatment (task 03).
- Answering remote rows in place beyond what the landed question rows
  already do.

## Acceptance

- A remote manual-step wait appears as an origin-labeled row, disappears when
  the remote no longer reports it, and degrades per instance without failing
  the local read or listing a dead row.
- An instance without the waiting read renders its degraded section while
  local rows and other instances list normally.

## ASCII UI preview

UI-01 excerpt (full preview in [the plan](plan.md#ascii-ui-preview)): the
`Awaiting merge · gpu-box` row with the origin label is this task's view.

## Verification

```bash
cd apps/backend && go test ./internal/instances/...
cd apps/web && pnpm vitest run lib/state/slices/needs-you-inbox app/needs-you-inbox && pnpm run typecheck && pnpm run i18n:check && pnpm run i18n:ratchet
cd apps/web && pnpm e2e:run --grep "multi-instance"
```

## Files likely touched

- `apps/backend/internal/instances/remoteinbox/` (the landed composition
  package; extend with the waiting read)
- `apps/backend/internal/clarification/handlers.go` (route registration for
  the remote-facing read, if not already versioned)
- `apps/web/app/needs-you-inbox/needs-you-inbox-page-client.tsx`
- `apps/web/lib/state/slices/needs-you-inbox/`
- `apps/web/e2e/tests/multi-instance/multi-instance.spec.ts`

## Dependencies

Tasks 02 and 03, and the landed remote-questions change.

## Risks

- Depends on the landed composition's actual shape (backend fan-out versus
  SPA-side composition); reconcile this task's integration points against it
  before starting.

## Parallelism

`sequential`

## Inputs

- Requirements REQ-UI-NEEDS-YOU-INBOX-004 and -005; system design section
  [Remote waiting rows](../../specs/ui/system-design/needs-you-inbox-waiting-rows.md#remote-waiting-rows).

## Results

Done. `remoteinbox.Client.ReadNeedsYou`/`ReadNeedsYouHidden`
(`apps/backend/internal/instances/remoteinbox/client.go`) discover the
remote's credential-visible workspaces and read the remote's own aggregated
endpoint (`/api/v1/needs-you-inbox?workspace_id=<id>&limit=<n>`, plus
`/hidden`) once per workspace, merging by (reference time, row id), stamping
instance and origin-workspace identity, and mapping 404 to inbox-disabled
and 401/403 to unauthorized; the per-workspace fetch skeleton is shared with
the clarification path so transport behavior cannot drift. The local forward
routes `GET /api/v1/instances/:id/needs-you-inbox` (+`/hidden`,
`apps/backend/internal/backendapp/instance_inbox_routes.go`) wrap one
fan-out per request in `instanceNeedsYouPageEnvelope` with the same limit
validation and degraded-200 contract.

The SPA's remote controller reads the new forward routes; the remote section
renders kind-tagged question and waiting rows (waiting rows through
`NeedsYouWaitingRow` with an origin chip and open-task via `switchInstance`);
the remote hidden panel enumerates the merged hidden set; dismiss/snooze/
restore go through the existing sidecar forward routes so `mw:` ids address
the remote's own sidecar; no remote row is written to any local table. The
now-dead question-only client functions were removed. `remoteEmpty` copy was
updated in eight locales.

Verification: `go test ./internal/instances/... ./internal/backendapp/...`
passes except three `TestWorkspaceSourceMaterializer_*` failures verified
identical on pristine HEAD (macOS `/var` symlink); targeted vitest, typecheck,
`i18n:check`, `i18n:ratchet` pass. E2E added
"remote waiting rows appear, resolve, degrade, and notify without affecting
other sources" to `multi-instance.spec.ts`; the full `--grep "multi-instance"`
run exposed one stale badge assertion in the landed
`remote-inbox.spec.ts` (the fixture task's manual-step wait legitimately
counts as a second remote row), fixed by completing that fixture task right
after seeding, with both affected specs re-run green. `ReadNeedsYou` takes
`(ctx, limit)` rather than the planned `(ctx, workspaceID, limit)`: the
client performs its own workspace discovery, mirroring the landed
`ReadInbox` composition, and the forwarding route passes no workspace filter.
