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
agentctl journal, and the admission health that prevents new divergences.
It extends [durable agent delivery](durable-agent-delivery.md) and
[missing record recovery](durable-agent-record-recovery.md) without changing
their reconstruction or terminal settlement semantics.

## Requirement mapping

| Requirement | Section |
| --- | --- |
| AC-PLATFORM-DURABLE-AGENT-DELIVERY-006.25 | Pre-intake settlement |
| AC-PLATFORM-DURABLE-AGENT-DELIVERY-006.26 | Pre-intake settlement |
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
- [Recovery of missing durable delivery records](durable-agent-record-recovery.md).
- [Pre-intake delivery settlement decision](../../../decisions/2026-11-03-pre-intake-delivery-settlement.md).
