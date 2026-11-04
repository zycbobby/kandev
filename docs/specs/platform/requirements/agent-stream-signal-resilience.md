---
status: draft
system: platform
created: 2026-11-04
updated: 2026-11-04
owners:
  - kandev
---

# Agent stream signal resilience requirements

## Overview

The agent stream is the WebSocket between the backend and agentctl. It carries
agent output events and MCP control requests, including the
`step_complete_kandev` workflow signal. This contract defines how control
signals survive transient stream loss, how backpressure is isolated, and how an
undelivered signal becomes visible.

This draft defines proposed behavior. It does not claim that the current
implementation provides these guarantees.

## Terminology

- Agent stream: The authenticated WebSocket between the backend and agentctl.
- Control request: An MCP request or response that must reach the backend, as
  opposed to agent output events.
- Capture store: The bounded agentctl-local durable store for idempotent
  control signals awaiting delivery.
- Delivery window: The bounded period after capture during which a signal may
  be delivered before completion-pending is surfaced.
- Completion-pending: The existing session recovery surface that tells the user
  a workflow step ended but its completion signal did not arrive.

## Requirements

### REQ-PLATFORM-AGENT-STREAM-SIGNAL-001: Step-complete signals survive transient disconnects

**Intent:** A transport disconnect must not require a new user turn to advance
the workflow.

**User story:** As a user, I want a completed step to advance even if the agent
stream briefly drops, so that I do not have to send another message.

#### Acceptance criteria

- **AC-PLATFORM-AGENT-STREAM-SIGNAL-001.1:** Before `step_complete_kandev` is
  published to the agent stream, agentctl shall write it to the capture store.
  The record shall carry request ID, action, payload, session, creation time,
  delivery time, and attempt count.
- **AC-PLATFORM-AGENT-STREAM-SIGNAL-001.2:** On connection establishment or
  reconnect, undelivered records shall be replayed by request ID before or
  alongside live control traffic. The backend shall apply the step transition
  once and return `already_signaled` or equivalent for duplicates.
- **AC-PLATFORM-AGENT-STREAM-SIGNAL-001.3:** A captured record shall remain
  replayable after an agentctl restart until it is confirmed delivered or
  expires.
- **AC-PLATFORM-AGENT-STREAM-SIGNAL-001.4:** Only actions proven idempotent may
  use durable capture and replay. Other MCP tools shall use bounded retries and
  typed failures.

### REQ-PLATFORM-AGENT-STREAM-SIGNAL-002: Backpressure must not starve control requests

**Intent:** Agent output volume must not make the control plane unreachable.

**User story:** As an agent, I want my step-complete signal to reach the backend
even when output events are backlogged.

#### Acceptance criteria

- **AC-PLATFORM-AGENT-STREAM-SIGNAL-002.1:** While agent-event queues are full
  or their consumer is slow, `step_complete_kandev` shall still be captured and
  delivered after the connection recovers.
- **AC-PLATFORM-AGENT-STREAM-SIGNAL-002.2:** Non-idempotent MCP tools shall
  receive a typed retryable failure instead of a silent fixed-duration send
  timeout.
- **AC-PLATFORM-AGENT-STREAM-SIGNAL-002.3:** A write that exceeds the write
  deadline shall close the connection with a typed backpressure reason and
  preserve captured records for replay.

### REQ-PLATFORM-AGENT-STREAM-SIGNAL-003: Connections detect half-open and slow peers

**Intent:** Transport failure must be detected by health signals, not by waiting
for TCP timeout.

**User story:** As an operator, I want a half-open stream to reconnect quickly,
so that delivery pauses are short.

#### Acceptance criteria

- **AC-PLATFORM-AGENT-STREAM-SIGNAL-003.1:** Agent stream connections shall use
  ping/pong and explicit read and write deadlines.
- **AC-PLATFORM-AGENT-STREAM-SIGNAL-003.2:** Every disconnect shall record one
  bounded typed reason from a closed set covering event-queue overflow, write
  timeout, ping timeout, read timeout, owner change, and read error.
- **AC-PLATFORM-AGENT-STREAM-SIGNAL-003.3:** Disconnect, reconnect, request
  failure, and queue saturation shall be observable through bounded metrics and
  logs. Metric labels shall not contain session, submission, or payload
  identity.

### REQ-PLATFORM-AGENT-STREAM-SIGNAL-004: Undelivered signals surface completion-pending

**Intent:** An attempted but undelivered signal must be visible and recoverable,
not silently pending.

**User story:** As a user, I want to see that a step completion did not reach the
system, so that I can recover it.

#### Acceptance criteria

- **AC-PLATFORM-AGENT-STREAM-SIGNAL-004.1:** When a captured signal remains
  undelivered beyond the delivery window, agentctl shall emit a durable
  `signal_delivery_failed` event with request ID, action, session, reason,
  attempts, and first-attempt time. The event shall not contain the payload.
- **AC-PLATFORM-AGENT-STREAM-SIGNAL-004.2:** The backend shall surface
  completion-pending through the existing session recovery or error UI and
  shall keep resend, chat fallback, and user retry available.
- **AC-PLATFORM-AGENT-STREAM-SIGNAL-004.3:** After a resend or chat fallback,
  the workflow shall advance exactly once and the notice shall clear.

### REQ-PLATFORM-AGENT-STREAM-SIGNAL-005: Capture must not depend on the backend SQLite writer

**Intent:** Backend database contention must delay delivery, not lose the
signal.

**User story:** As an operator, I want control signals to survive SQLite writer
pressure, so that a slow database does not strand a workflow.

#### Acceptance criteria

- **AC-PLATFORM-AGENT-STREAM-SIGNAL-005.1:** Capture and replay shall use
  agentctl-local durable storage, not backend SQLite.
- **AC-PLATFORM-AGENT-STREAM-SIGNAL-005.2:** When the capture store is full,
  corrupt, or unwritable, `step_complete_kandev` shall return a typed failure
  and the agent chat fallback shall remain available. The system shall not
  discard the signal silently or fall back to unbounded memory.
- **AC-PLATFORM-AGENT-STREAM-SIGNAL-005.3:** Capture storage shall be bounded
  by a record cap and a time-to-live. Undelivered records shall not be deleted
  before expiry.

## Implementation records

- [Agent stream signal resilience system design](../system-design/agent-stream-signal-resilience.md).
- [Agent stream signal resilience decision](../../decisions/2026-11-04-agent-stream-signal-resilience.md).

## Out of scope

- Delivery when both the backend and the agentctl capture store are unavailable.
- Durable replay for non-idempotent MCP tools.
- Rebuilding the backend SQLite writer or changing NTFS storage behavior.
- New frontend completion-pending concepts beyond the existing recovery surface.
