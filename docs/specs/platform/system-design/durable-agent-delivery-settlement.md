---
status: draft
system: platform
created: 2026-11-04
updated: 2026-11-04
requirements:
  - REQ-PLATFORM-DURABLE-AGENT-DELIVERY-006
  - REQ-PLATFORM-DURABLE-AGENT-DELIVERY-007
---

# Pre-intake delivery settlement and admission health

## Purpose

This design covers durable submissions that definitively never reached the
agentctl journal, durable submissions that did reach it without terminal
evidence, and the admission health that prevents new divergences.
It extends [durable agent delivery](durable-agent-delivery.md) and
[missing record recovery](durable-agent-record-recovery.md) without changing
their reconstruction or terminal settlement semantics.

## Requirement mapping

| Requirement | Section |
| --- | --- |
| AC-PLATFORM-DURABLE-AGENT-DELIVERY-006.25 | Pre-intake settlement |
| AC-PLATFORM-DURABLE-AGENT-DELIVERY-006.26 | Pre-intake settlement |
| AC-PLATFORM-DURABLE-DELIVERY-ABANDONED-001.1, 001.2 | Retry succession |
| AC-PLATFORM-DURABLE-DELIVERY-ABANDONED-002.1, 002.2 | Abandoned journal settlement |
| AC-PLATFORM-DURABLE-DELIVERY-ABANDONED-003.1, 003.2 | Abandoned journal settlement |
| AC-PLATFORM-DURABLE-DELIVERY-ABANDONED-004.1 | Abandoned journal settlement |
| AC-PLATFORM-DURABLE-DELIVERY-ABANDONED-005.1 | Abandoned journal settlement |
| AC-PLATFORM-DURABLE-DELIVERY-ABANDONED-006.1 | Abandoned journal settlement |
| AC-PLATFORM-DURABLE-AGENT-DELIVERY-007.7 | Admission health |
| AC-PLATFORM-DURABLE-AGENT-DELIVERY-001.4 (oversized event) | Admission health |

## Pre-intake settlement

A dispatch that fails definitively before journal intake is a separate evidence class.
Agentctl distinguishes a pre-write rejection from a post-write transition failure.
The backend settles a pre-write rejection as `failed` without a recovery block.
Any post-write failure keeps the existing uncertain path.

For a historic `interrupted_unknown` submission, an explicit retry may prove that the exact ID never entered the authenticated retained journal.
Settlement requires a durable untruncated descriptor, no other unresolved candidate, an exact empty-stream block binding, and matching session, incarnation, generation, and owner evidence.
The settlement writes the failed submission, its pre-intake effect, and the bound block resolution in one transaction, advances the session recovery revision, and releases parked queue work.
It never resends the prompt.
Missing ownership, a lost or unreachable journal, a truncated descriptor, another unresolved candidate, or a non-empty block stream keeps recovery blocked.

The absence probe is an exact-ID request on the authenticated owner snapshot.
A record owned by another session or incarnation is an ownership error, not proof of absence.
Retained reads compare descriptors around the lookup and reject a changed snapshot.
The absence proof relies on retained submissions never being deleted: retirement
only marks a record, and compaction copies every bucket.

Settlement uses the `agent_delivery.pre_intake_settlement` effect type.
It keeps the terminal stream, sequence, turn, and `fully_settled_at` unset, so
pending terminal settlement never treats it as an incomplete terminal outcome.
Repeated or concurrent retries return `settled` and write the effect once.

## Retry succession

A recovery record names the execution that owned the interrupted dispatch. A
recovery-driven resume may create a successor execution for the same task,
session, incarnation, and generation before the user retries. The database
guard must not treat that legitimate successor as a foreign owner.

Retry validates the recovery record's session, incarnation, harness generation,
submission, stream, and prompt generation, but not execution-ID equality.
Execution ownership is decided by the runtime's authenticated successor
evidence: the same task/session/incarnation/generation, an initialized native
session, no pending prompt dispatch, and proof that the historical owning
process terminated. A live execution that fails any of those checks remains
blocked, and a retry with no execution row keeps using retained evidence.

## Abandoned journal settlement

A submission can reach the journal and still have no terminal evidence because
agentctl exited before writing it. Retry may settle this evidence class only
through the conservative rules below.

- **Eligibility.** The authenticated retained journal must be durable, use a
  supported protocol version, and return a complete descriptor. The target
  must be `interrupted_unknown` with `terminal_event_retained=false` and no
  terminal sequence. Every other unresolved inventory entry must also be
  `interrupted_unknown` without terminal evidence, and its SQL row must be
  missing or terminal. Any active SQL peer, any `prepared`/`accepted`/
  `dispatching` record, a truncated descriptor, or an unproven outcome keeps
  the whole settlement blocked. Session, incarnation, generation, stream,
  submission ID, and immutable payload hash must match.
- **Death proof.** The historical `OriginalRuntime` must validate and
  `processidentity.OwnedSessionTerminated` must return true. Unsupported
  platforms fail closed.
- **Journal retirement.** Runtime owns the mutation. A live instance retires
  through its per-instance client; a file-only retained runtime uses the
  authenticated control route `POST /api/v1/delivery/retained/retire`, which
  requires an unowned session, the owner marker, and the same death proof.
  Retirement marks records `retired` and keeps payload, hash, state, and the
  unknown label. It never deletes a record and never writes a terminal event.
- **SQL settlement.** One transaction marks the submission `failed` with outcome
  `failed`, keeps `terminal_sequence=0` and `fully_settled_at` unset, writes a
  completed `agent_delivery.abandoned_journal_settlement:<submission_id>`
  effect with an empty stream and sequence zero, resolves only the bound
  non-empty-stream block with `authorized_action=durable_delivery_abandoned`
  and `delivery_outcome=failed`, and advances the recovery revision.
- **Idempotency.** A target found already retired is valid evidence for
  resubmission. A submission already settled with the matching effect resolves
  its bound block without a second effect. A crash between retirement and SQL
  commit therefore converges on the next retry.
- **Resume.** Native resume skips retired peers that carry no terminal
  evidence, and those peers no longer count as unresolved. A retired peer that
  still carries retained terminal evidence keeps participating as proof, and
  non-retired peers keep the existing terminal-evidence requirement.
- **Boundaries.** Archived tasks, Office- or automation-owned sessions, and
  remote executor journals are rejected before any write. The historical
  prompt is never resent, and unrelated blocks and queue entries stay
  untouched.

## Admission health

The capability carries `AdmissionBlocked` while the journal remains readable.
A readable journal whose writer cannot admit new submissions reports the blocked
state without becoming legacy or non-durable, so recovery evidence stays readable.
The backend refuses the prompt before creating a durable SQL submission or a recovery block.
The refusal is transient and clears when capacity recovers with a live update pump.

Agentctl distinguishes two admission failures.
A pre-write rejection (pressure, blocked health, or a failed submission write) is
`durable_delivery_admission_rejected`.
A transition failure after the submission write is
`durable_delivery_admission_uncertain` and never takes the immediate-settlement path.
Both carry a bounded `reason` in the prompt error response.

A single event over the per-event limit is skipped with an ERROR log and a bounded
metric instead of blocking admission or stopping the update pump.
Genuine storage exhaustion still fails closed; Stop, status, replay, and ACK
remain available.

## Observability

| Metric | Unit | Producer and labels |
| --- | --- | --- |
| agent_delivery_admission_rejected_total | Refusals | agentctl admission, reason (`storage_pressure`, `storage_error`, `journal_full`, `event_too_large`, `journal_corrupt`, `owner_mismatch`, `unknown`) |
| agent_delivery_event_rejected_total | Events | agentctl update pump, reason (`event_too_large`, `queue_too_large`) |

Both label sets are closed and never carry submission, stream, task, or payload identity.

## Related records

- [Durable agent delivery](durable-agent-delivery.md).
- [Abandoned delivery settlement requirements](../requirements/durable-delivery-abandoned-settlement.md).
- [Recovery of missing durable delivery records](durable-agent-record-recovery.md).
- [Pre-intake delivery settlement decision](../../../decisions/2026-11-03-pre-intake-delivery-settlement.md).
- [Abandoned delivery settlement decision](../../../decisions/2026-11-04-abandoned-delivery-settlement.md).
