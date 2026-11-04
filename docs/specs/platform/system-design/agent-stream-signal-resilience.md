---
status: draft
system: platform
created: 2026-11-04
updated: 2026-11-04
requirements:
  - REQ-PLATFORM-AGENT-STREAM-SIGNAL-001
  - REQ-PLATFORM-AGENT-STREAM-SIGNAL-002
  - REQ-PLATFORM-AGENT-STREAM-SIGNAL-003
  - REQ-PLATFORM-AGENT-STREAM-SIGNAL-004
  - REQ-PLATFORM-AGENT-STREAM-SIGNAL-005
---

# Agent stream signal resilience

## Purpose

This design covers how the backend-to-agentctl agent stream detects transport
failure, isolates MCP control requests from agent-event backpressure, captures
idempotent workflow signals durably, and makes an undelivered signal visible.
It extends [durable agent delivery](durable-agent-delivery.md) without changing
the step-completion accepted/stale semantics.

## Requirement mapping

| Requirement | Section |
| --- | --- |
| AC-PLATFORM-AGENT-STREAM-SIGNAL-001.1 through 001.4 | Signal capture and replay |
| AC-PLATFORM-AGENT-STREAM-SIGNAL-002.1 through 002.3 | Control-plane isolation |
| AC-PLATFORM-AGENT-STREAM-SIGNAL-003.1 through 003.3 | Transport health |
| AC-PLATFORM-AGENT-STREAM-SIGNAL-004.1 through 004.3 | Completion-pending visibility |
| AC-PLATFORM-AGENT-STREAM-SIGNAL-005.1 through 005.3 | Capture store boundaries |

## Transport health

Both ends of the WebSocket share one contract in
`internal/common/agentstream` so they detect a half-open connection with the
same timing.

| Constant | Value | Role |
| --- | --- | --- |
| `PingInterval` | 30s | Keeps NAT/proxy bindings warm and stays below the pong timeout |
| `PongTimeout` | 90s | Maximum age of the last pong (three missed pings) |
| `ReadDeadline` | 180s | Backstop behind the ping pump, refreshed by pongs |
| `WriteDeadline` | 10s | Bounds one frame so a stalled peer surfaces as `write_timeout` |
| `EventQueueMaxItems` | 4096 | Reader-side event retention when the handler stalls |
| `EventQueueMaxBytes` | 4 MiB | Byte bound for the same queue |

Every disconnect carries one reason from the closed set
`event_queue_full`, `write_timeout`, `ping_timeout`, `read_timeout`,
`owner_changed`, and `read_error`. A clean close records no failure. The ping
pump writes only websocket control frames, so it never races the data writer;
the data writer sets the write deadline on every frame, including journal
replay. The client sends a close frame carrying the typed reason so the peer
records the same cause instead of a generic read error.

## Control-plane isolation

The agent-stream writer used to serve agent events and MCP requests from one
blocking select loop. A slow or half-open peer could block a data write, and a
full event queue closed the stream before a control request could enter it.

The writer now drains at most one MCP request with a non-blocking select before
each event frame, in the live loop, in both replay callbacks, and per journal
page during durable replay. Because each frame write is bounded, a control
request waits at most one frame rather than a whole page or an unbounded write.
A failed write fails the pending request with a typed reason and increments the
bounded failure counter. The client-side event queue keeps its bounded size; a
full queue closes the stream with `event_queue_full`, and the durable journal
replays the missed events after reconnect.

## Signal capture and replay

`step_complete_kandev` is an idempotent workflow signal, so it is the first
action on the durable-capture allowlist. Other MCP tools keep bounded retries
and typed failures; their external side effects must not be replayed.

The capture store is a bounded `pending_signals` bucket in the agentctl journal.
Each record carries request ID, action, payload, session ID, creation time,
delivery time, and attempt count. The bucket is created lazily, so journals
written by older builds remain readable, and `bolt.Compact` copies it with
every other bucket.

| Bound | Value | Behavior at the boundary |
| --- | --- | --- |
| `MaxPendingSignals` | 256 | `Put` returns `ErrPendingSignalStoreFull`; no unexpired record is evicted |
| `PendingSignalTTL` | 24h | `Put` prunes expired records; `List` filters them; an undelivered record is never deleted before expiry |

Capture happens before publication to the stream. A retry with the same request
ID is a no-op, so a crashed attempt can re-put the same record. When the journal
is unavailable, full, or invalid, the signal call returns a typed failure and
the agent keeps its chat fallback; the system never falls back to an unbounded
in-memory queue.

On connection establishment or reconnect the writer replays undelivered records
with their original request IDs. A backend response, including the existing
`already_signaled` duplicate result, marks the record delivered. Replay is
therefore idempotent twice over: the capture record and the backend's
step-complete dedup both prevent a second transition.

## Completion-pending visibility

A captured signal that remains undelivered beyond the bounded delivery window
produces one durable `signal_delivery_failed` event. It carries request ID,
action, session, reason, attempts, and first-attempt time, and never the
payload. The event travels through the durable agent-event pipeline, so journal
replay delivers it after reconnect or an agentctl restart, and a persisted
notified marker prevents duplicates.

The backend projects the notice through the existing session recovery/error
surface. Resend, the chat fallback, and user retry remain available. A later
successful signal clears the notice and advances the workflow once.

## Observability

| Metric | Unit | Producer and labels |
| --- | --- | --- |
| `agent_stream_disconnect_total` | Streams | Both stream ends, reason (closed set above) |
| `agent_stream_event_queue_depth` | Events | Client event queue |
| `agent_stream_mcp_request_failures_total` | Requests | Stream writer and MCP producer, reason (closed set) |

Label sets are closed. Session, submission, request, and payload identities stay
in structured logs, never in metric labels. Signal capture metrics record
capture, replay, and delivery-failure counts without payload identity.

## Related records

- [Agent stream signal resilience requirements](../requirements/agent-stream-signal-resilience.md).
- [Durable agent delivery requirements](../requirements/durable-agent-delivery.md).
- [Agent stream signal resilience decision](../../decisions/2026-11-04-agent-stream-signal-resilience.md).
