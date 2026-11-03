# ADR-2026-11-03-pre-intake-delivery-settlement: Settle prompts that never reached the journal

**Status:** accepted
**Date:** 2026-11-03
**Area:** protocol, backend

## Context

Durable delivery treated every dispatch error as "the submission may already be
in the agentctl journal". That assumption is wrong when agentctl refuses a
prompt before writing its submission: transport not sent, a runtime storage
failure that set `deliveryBlocked`, or a failed admission precondition. The
backend still recorded `interrupted_unknown` with `prompt_dispatch_failed` and
created an `unknown_prompt_outcome` recovery block, while the journal never
contained the submission.

Three fail-closed rules then deadlocked each other. Retry reconstruction
required exactly one unresolved journal candidate. Native resume required every
active SQL submission to exist in the journal. New prompts were refused while
the recovery block was open. Two live sessions (062a1281 and the 5a9f6ea5
family) reached this state, and the only escape was an offline SQLite edit.

A second defect amplified the first. After one durable event commit failed,
agentctl kept `deliveryBlocked` sticky for the life of the process, did not
advertise that state in its capability, and stopped forwarding agent output.
The backend therefore kept creating SQL submissions for a peer that could no
longer accept them.

## Decision

Make "definitively not delivered" a first-class evidence class.

- **Evidence.** Extend the authenticated retained-evidence request with an exact
  `AbsentSubmissionID`. A durable, untruncated descriptor plus an explicit
  submission-not-found answer proves absence; the before/after descriptor
  comparison must stay stable. A record owned by another session or incarnation
  is an ownership error, not proof of absence.
- **Settlement.** Add a repository transaction that marks the submission
  `failed`, writes an `agent_delivery.pre_intake_settlement:<id>` effect with an
  empty stream and sequence zero, resolves only the bound empty-stream recovery
  block with `durable_delivery_not_delivered`, and advances the session recovery
  revision. It never fabricates `fully_settled_at` or a terminal identity, and
  the existing pending-terminal settlement never picks this effect type up.
- **Retry.** `session.recover` settles a historic `interrupted_unknown` /
  `prompt_dispatch_failed` submission only when the block is an
  `unknown_prompt_outcome` bound to that exact submission with an empty stream,
  the descriptor has zero unresolved candidates and is not truncated, and the
  session, incarnation, and generation match. Settlement releases parked queue
  work but never resends the historical prompt. Repeated or concurrent retries
  return `settled` and write the effect once.
- **Immediate path.** Agentctl wraps pre-write failures in a typed rejected
  sentinel and post-write transition failures in an uncertain sentinel, and
  returns the matching `kind`/`reason` on the prompt error. The backend settles
  a rejected dispatching submission immediately with no recovery block. A
  failed settlement falls back to the existing uncertain block, so a database
  error cannot turn a possible delivery into a false `failed`.
- **Admission health.** The capability carries `AdmissionBlocked` while the
  journal stays readable. The backend refuses the prompt before creating a SQL
  submission. A single event over the per-event limit is skipped with a bounded
  metric instead of blocking admission or stopping the update pump. Genuine
  storage exhaustion still fails closed.

No table or column is added. The effect type is data in the existing text
column.

## Consequences

The affected historic sessions become recoverable through the existing Retry
connection action, and new pre-write rejection no longer creates a row that can
diverge from the journal. Uncertain outcomes and missing evidence keep the
existing blocked behavior; only the exact, authenticated absence case settles.

The absence proof relies on retained submission records never being deleted.
Retirement only marks a record and compaction copies every bucket, so the
invariant holds today. Any future submission deletion must update this proof
first.

Pre-intake settlement leaves `fully_settled_at` unset, so the prompt payload is
not covered by terminal-retention cleanup. The payload is bounded by the
existing 1 MiB ceiling; retention for pre-intake effects remains future work.

Skipping an oversized event loses that segment of agent output. The ERROR log
and `agent_delivery_event_rejected_total` make the loss observable, which is
preferable to wedging the whole session. Archived tasks and `dispatching`
journal retirement remain out of scope.

## Alternatives considered

- **Reuse the terminal effect with sequence zero:** `ListPendingAgentDeliverySettlements` would treat it as a pending terminal and report a sequence conflict. A separate effect type is required by the existing invariants.
- **Treat journal absence alone as proof:** It ignores another unresolved candidate, a truncated descriptor, or a block bound to a non-empty stream, and would settle work that may still exist elsewhere.
- **Downgrade to legacy delivery:** Violates the contract that a configured durable journal failure never silently becomes legacy.
- **Auto-resend the historical prompt:** Repeats tool execution and contradicts the no-resend rule; settlement changes state only.
- **Settle every admission error:** A response lost after the journal write would be misclassified as not delivered.
- **Advertise `Durable=false` while blocked:** Recovery evidence must stay readable; blocked admission is a transient health state, not missing durable storage.

## Related records

- [Durable agent delivery requirements](../specs/platform/requirements/durable-agent-delivery.md).
- [Durable agent delivery system design](../specs/platform/system-design/durable-agent-delivery.md).
- [Recovery of missing durable delivery records](../specs/platform/system-design/durable-agent-record-recovery.md).
- [Durable agent sessions plan](../plans/durable-agent-sessions/plan.md).
