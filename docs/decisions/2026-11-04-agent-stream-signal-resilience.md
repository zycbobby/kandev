# ADR-2026-11-04-agent-stream-signal-resilience: Keep MCP control signals deliverable across stream loss

**Status:** accepted
**Date:** 2026-11-04
**Area:** protocol, backend

## Context

The backend-to-agentctl `/api/v1/agent/stream` WebSocket carries both agent
output events and MCP tool requests. `step_complete_kandev` is an MCP tool the
agent calls at the end of a workflow step; its request travels from the agentctl
MCP server through `ChannelBackendClient`, over the stream, to the backend's
step-completion handler. The workflow advances only when that signal arrives.

On 2026-11-04 the stream disconnected and reconnected twice under load
(12:15:18 and 12:17:30). The signal did not enter the stream within the 5-second
send window, so the backend never saw it. The workflow stayed in
completion-pending until the user started a new turn, and three MCP tool calls
from parallel tasks failed in the same window. SQLite writer contention
(`writer_wait_count=2281`, `writer_ping deadline_exceeded`) amplified handler
blocking and event backlog.

Three transport properties combined to lose the signal:

- `ChannelBackendClient` handed requests to an unbuffered channel with a fixed
  5-second send timeout. When the writer was busy or disconnected, the request
  failed with a generic timeout and left no durable trace.
- The agent-stream writer shared one select loop with agent events and had no
  ping/pong or write deadline. A slow or half-open peer could block it, and a
  disconnected peer could only be detected by TCP timeout.
- The client's bounded event queue closed the stream when handlers fell behind
  (`errAgentEventQueueFull`), so backpressure turned into reconnect churn.

The existing stuck-signal watchdog only sees signals that reached backend
persistence. A signal that never arrived is invisible to it.

## Decision

- **Transport health.** Both agent-stream endpoints add ping/pong and explicit
  read/write deadlines. Every disconnect carries a bounded typed reason from the
  closed set `event_queue_full`, `write_timeout`, `ping_timeout`,
  `read_timeout`, `owner_changed`, and `read_error`. Reconnect starts from the
  same retained state instead of waiting for a TCP timeout.
- **Control and event isolation.** MCP control requests no longer share the
  agent-event backlog path. A full event queue or a slow event consumer produces
  a typed retryable failure for non-idempotent tools rather than a silent
  five-second timeout. The writer prioritizes control traffic and bounds its own
  write time.
- **Durable capture for idempotent signals.** `step_complete_kandev` (and only
  explicitly idempotent signal actions) is written to a bounded `pending_signals`
  bucket in the agentctl journal before publication. The record carries request
  ID, action, payload, session, creation time, delivery time, and attempts. A
  new or reconnected stream replays undelivered records by request ID. The
  backend's existing duplicate-step-complete handling makes replay
  idempotent.
- **Bounded store, fail closed.** The capture store has a record cap and a TTL.
  When it is full, corrupt, or unavailable, `step_complete_kandev` returns a
  typed failure and the agent keeps its chat fallback and resend path. The
  system never falls back to an unbounded in-memory queue.
- **Visible completion-pending.** When a captured signal remains undelivered
  beyond the bounded delivery window, agentctl appends a durable
  `signal_delivery_failed` event containing request ID, action, session, reason,
  attempts, and first-attempt time, but not the payload. The event travels with
  journal replay and the backend surfaces completion-pending through the
  existing session recovery/error UI. Resend and the chat fallback remain
  available, and the notice clears after recovery.
- **Observability without identity labels.** Transport and signal counters use
  only closed reason labels plus queue depth and latency. Session, submission,
  and payload identities stay in logs, never in metric labels.
- **No backend-writer dependency.** Signal capture writes agentctl-local bbolt,
  not backend SQLite, so SQLite writer contention delays delivery but cannot
  lose the signal. Rebuilding the SQLite writer or changing NTFS storage is out
  of scope.

## Consequences

A transient disconnect no longer requires a new user turn to advance the
workflow. A signal that is captured but undeliverable becomes visible and
recoverable instead of silently disappearing. Payloads for pending signals are
retained until delivered or expired, which adds bounded local storage.

Replaying only idempotent actions is deliberate. Extending durable replay to
other MCP tools would risk repeating side effects; those tools keep bounded
retries and typed errors. A rollback to an agentctl without the capture bucket
simply stops replaying pending signals; the records are ignored, and the agent
or user recovery path still works.

## Alternatives considered

- **Increase the 5-second send timeout:** Treats the symptom. A blocked writer
  or disconnected stream still loses the request.
- **Unbounded buffers:** Removes the visible failure but risks memory growth and
  still loses everything on process exit.
- **Persist signals in backend SQLite:** Couples capture to the contended
  writer this change is trying to avoid.
- **Durable replay for every MCP tool:** Non-idempotent tools can repeat
  external side effects; only explicitly idempotent signals are captured.
- **Rely on the stuck-signal watchdog:** It can only inspect signals that
  reached the backend and cannot see a never-delivered attempt.
- **Add a dedicated completion-pending UI state:** Requires new copy in every
  locale for little benefit; the existing recovery/error surface already
  expresses the state.

## Related records

- [Agent stream signal resilience requirements](../specs/platform/requirements/agent-stream-signal-resilience.md).
- [Durable agent delivery requirements](../specs/platform/requirements/durable-agent-delivery.md).
- [Agent stream signal resilience system design](../specs/platform/system-design/agent-stream-signal-resilience.md).
