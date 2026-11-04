---
status: draft
system: platform
created: 2026-11-04
updated: 2026-11-04
owners:
  - kandev
---

# Durable delivery abandoned settlement requirements

## Overview

This contract extends [durable agent delivery](durable-agent-delivery.md) with
two recovery rules for dispatch state that the pre-intake absence proof cannot
cover:

- a recovery record whose execution was replaced by a successor execution
  created by the same recovery flow;
- a journal-present submission that has no terminal evidence and whose
  historical owner process is proven dead.

It also defines how a retired journal record behaves during native resume.
Pre-intake absence settlement and admission health are unchanged.

## Terminology

- Successor execution: An execution created by a recovery flow for the same
  task, session, incarnation, and harness generation as the historical
  execution.
- Abandoned submission: A journal record in `interrupted_unknown` with no
  terminal event, terminal sequence, or `terminal_event_retained`.
- Retirement: Marking a journal record `retired` while keeping its payload,
  hash, state, and unknown label. Retirement never deletes a record.
- Extra peer: Another unresolved journal record in the same authenticated
  descriptor that is not the retry target.

## Requirements

### REQ-PLATFORM-DURABLE-DELIVERY-ABANDONED-001: Retry defers execution succession to runtime evidence

**Intent:** A successor execution created by recovery must not make retry fail
before the runtime can prove ownership.

**User story:** As a user, I want retry to work after a recovery-created
successor started, so that the session is not permanently blocked.

#### Acceptance criteria

- **AC-PLATFORM-DURABLE-DELIVERY-ABANDONED-001.1:** Retry shall validate the
  recovery record's session, incarnation, harness generation, submission,
  stream, and prompt generation, and shall not reject the retry solely because
  `executors_running.agent_execution_id` differs from the recorded execution.
  Execution ownership shall be decided by the runtime's authenticated successor
  evidence.
- **AC-PLATFORM-DURABLE-DELIVERY-ABANDONED-001.2:** The runtime successor path
  shall require a matching task/session/incarnation/generation, an initialized
  native session, no pending prompt dispatch, and proof that the historical
  owning process terminated. A live non-successor execution shall remain
  blocked, and a retry without an execution row shall keep using retained
  evidence.

### REQ-PLATFORM-DURABLE-DELIVERY-ABANDONED-002: Abandoned settlement evidence must be authenticated and conservative

**Intent:** Settle only evidence that proves the historical prompt is abandoned
and no other work depends on it.

**User story:** As an operator, I want a conservative exit from a journal
deadlock, so that no live work is settled as failed.

#### Acceptance criteria

- **AC-PLATFORM-DURABLE-DELIVERY-ABANDONED-002.1:** Retry shall settle a
  journal-present `interrupted_unknown` submission without terminal evidence
  only when the authenticated retained journal is durable and untruncated, its
  complete unresolved inventory contains only the exact target or other
  `interrupted_unknown` peers whose SQL rows are missing or terminal, all
  session, incarnation, generation, stream, submission ID, and payload hash
  bindings match, the historical owner process is proven terminated, and the
  persisted recovery record is in the `uncertain` phase without a recorded
  history gap. Other recovery phases keep their existing reconciliation path.
- **AC-PLATFORM-DURABLE-DELIVERY-ABANDONED-002.2:** A truncated descriptor, a
  `prepared`/`accepted`/`dispatching` record, a terminal state without terminal
  evidence, an active SQL peer, a hash or stream mismatch, or missing death
  proof shall keep recovery blocked without any journal or SQL write. An
  unsupported platform for the death proof fails closed.

### REQ-PLATFORM-DURABLE-DELIVERY-ABANDONED-003: Settlement retires evidence without fabricating a terminal outcome

**Intent:** Recovery must acknowledge uncertainty without claiming a result the
system cannot prove.

**User story:** As an auditor, I want the original evidence preserved, so that I
can distinguish an abandoned prompt from a completed or failed one.

#### Acceptance criteria

- **AC-PLATFORM-DURABLE-DELIVERY-ABANDONED-003.1:** Abandoned settlement shall
  mark the exact journal records retired while preserving their payload, hash,
  state, and unknown label. It shall not delete records, fabricate a terminal
  event or sequence, write `completed`/`cancelled`, or set `fully_settled_at`.
- **AC-PLATFORM-DURABLE-DELIVERY-ABANDONED-003.2:** The SQL transaction shall
  mark the submission `failed` with outcome `failed`, keep
  `terminal_sequence=0` and `fully_settled_at` unset, write a dedicated
  `agent_delivery.abandoned_journal_settlement` effect rather than a terminal or
  pre-intake effect, resolve only the exact bound non-empty-stream recovery
  block with a dedicated authorized action, and advance the session recovery
  revision.

### REQ-PLATFORM-DURABLE-DELIVERY-ABANDONED-004: Settlement must be idempotent and crash-convergent

**Intent:** A repeated retry or a crash between the two stores must converge
without duplicate effects.

**User story:** As an operator, I want retrying to be safe, so that a failed SQL
commit does not require offline repair.

#### Acceptance criteria

- **AC-PLATFORM-DURABLE-DELIVERY-ABANDONED-004.1:** Repeated abandoned
  settlement and crashes between journal retirement and SQL commit shall
  converge without duplicate effects or duplicate block resolutions. A retry
  that finds the target already retired, or the submission already settled with
  the matching effect, shall return `settled` and resolve the bound block
  idempotently.

### REQ-PLATFORM-DURABLE-DELIVERY-ABANDONED-005: Settlement must preserve authorization, queue and owner boundaries

**Intent:** Settlement changes only the abandoned prompt it proved.

**User story:** As a user, I want unrelated sessions and queued work untouched.

#### Acceptance criteria

- **AC-PLATFORM-DURABLE-DELIVERY-ABANDONED-005.1:** Abandoned settlement shall
  not resend the historical prompt, replay tool calls, resolve unrelated
  blocks, or release unrelated queue entries. Archived tasks, Office- or
  automation-owned sessions, and journals outside the reachable local retained
  runtime shall be rejected before any journal or SQL write.

### REQ-PLATFORM-DURABLE-DELIVERY-ABANDONED-006: Native resume ignores settled retired peers

**Intent:** A retired record that carries no terminal evidence must not block
native resume.

**User story:** As a user, I want resume to work after settlement, so that the
saved conversation becomes usable again.

#### Acceptance criteria

- **AC-PLATFORM-DURABLE-DELIVERY-ABANDONED-006.1:** Native resume shall skip
  retired journal peers that carry no terminal evidence, and those records shall
  not count toward the session's unresolved delivery count. A retired peer that
  still carries retained terminal evidence keeps participating as proof, and
  non-retired peers keep the existing terminal-evidence requirement.

## Implementation records

- [Abandoned delivery settlement design](../system-design/durable-agent-delivery-settlement.md).
- [Abandoned delivery settlement decision](../../decisions/2026-11-04-abandoned-delivery-settlement.md).
- [Durable agent delivery requirements](durable-agent-delivery.md).

## Out of scope

- Automatic or startup sweeps; settlement is a user-triggered retry action.
- Remote executor journal surgery.
- `prepared`/`accepted`/`dispatching` abandoned forms.
- Deleting journal records or writing synthetic terminal evidence.
