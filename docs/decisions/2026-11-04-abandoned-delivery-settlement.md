# ADR-2026-11-04-abandoned-delivery-settlement: Settle journal-present submissions without terminal evidence

**Status:** accepted
**Date:** 2026-11-04
**Area:** protocol, backend

## Context

Durable delivery has two settled evidence classes. A retained journal with a
terminal event settles through the terminal path. An authenticated journal that
proves the exact submission was never written settles through
[pre-intake settlement](2026-11-03-pre-intake-delivery-settlement.md).

A 2026-11-04 service restart exposed a third class. Agentctl had accepted the
submission but exited before writing its terminal event, so the journal record
stayed `interrupted_unknown` with no `terminal_event_retained` and
`terminal_sequence=0`. Two stores then required each other to move first:

- Retry's retained path returned `terminal_outcome_missing` and had no
  retirement entry point.
- Native resume required terminal evidence from a journal-only unknown peer,
  while its retirement step required the SQL row to still be
  `interrupted_unknown`. After another path settled the SQL row, retirement was
  refused.
- The journal's `Unresolved` count also blocked new prompt admission.

Nine sessions and fifteen submissions reached this state. The only escape was
an offline edit of SQLite and the journal. "No terminal evidence" is not proof
that work is still alive, so the system needs a conservative, auditable exit
once the historical owner is proven dead.

## Decision

- **Evidence.** Abandoned settlement requires an authenticated retained journal
  owned by the exact session, a durable descriptor with a supported protocol
  version that is not truncated, and a complete unresolved inventory. Every
  inventory entry must be either the exact target or another
  `interrupted_unknown` record without terminal evidence whose SQL row is
  missing or already terminal. Any `prepared`, `accepted`, or `dispatching`
  record, any terminal state without terminal evidence, a truncated descriptor,
  or an active SQL peer blocks the whole settlement. Session, incarnation,
  generation, stream, submission ID, and immutable payload hash must match.
- **Death proof.** The historical `OriginalRuntime` identity must be validated
  and `processidentity.OwnedSessionTerminated` must return true. Missing,
  invalid, false, or errored proof keeps recovery blocked. Platforms where the
  proof is unavailable fail closed.
- **Journal retirement.** Runtime owns journal mutation through
  `RetireDeliveryRecordSubmissions`. A live instance uses the per-instance
  retirement route; a file-only retained runtime uses the authenticated control
  route `POST /api/v1/delivery/retained/retire`, which requires an unowned
  session, the owner marker, and the same death proof. Retirement marks records
  `retired` and preserves payload, hash, state, and the unknown label. It never
  deletes a record and does not fabricate a terminal event or sequence.
- **SQL settlement.** A sibling transaction marks the submission `failed` with
  outcome `failed`, keeps `terminal_sequence=0` and `fully_settled_at` unset,
  writes `agent_delivery.abandoned_journal_settlement:<submission_id>` as a
  completed effect with an empty stream and sequence zero, resolves only the
  bound non-empty-stream recovery block with
  `authorized_action=durable_delivery_abandoned` and
  `delivery_outcome=failed`, and advances the session recovery revision. The
  separate effect type keeps the audit distinction from a submission that never
  reached the journal and prevents the pending-terminal scan from treating it as
  an incomplete terminal.
- **Idempotency and crash convergence.** A retry that finds the target already
  retired treats that as valid evidence and resubmits the same settlement. A
  previously settled submission with the same effect resolves the bound block
  again without writing a second effect. A crash between retirement and SQL
  commit therefore converges on the next retry.
- **Native resume.** The native-resume peer loop skips `Retired` journal
  records, so a settled peer no longer reports disagreeing terminal evidence.
  Non-retired peers keep the existing blocked behavior.
- **Boundaries.** Archived tasks, Office- or automation-owned sessions, and
  remote executor journals are rejected before any write. The historical prompt
  is never resent, unrelated blocks and queue entries are untouched, and only
  the retry path releases parked queue work.

## Consequences

Affected sessions become recoverable through the existing Retry action: the
block disappears, the journal `Unresolved` count returns to zero, and new
prompts and native resume work again. `fully_settled_at` stays unset, so the
payload is not covered by terminal-retention cleanup; this matches the accepted
pre-intake trade-off.

A rollback to an older backend will treat a retired peer as unresolved again.
Operators must converge with the new build rather than deleting journal records.
Retiring an extra peer is justified only by the complete descriptor inventory
and the SQL cross-check; future changes that add another unresolved shape must
extend the eligibility rules before this path may run.

## Alternatives considered

- **Reuse pre-intake settlement:** It requires an empty block stream and zero
  descriptor submissions, which is exactly the opposite of this evidence class.
- **Write a synthetic terminal event:** The system cannot prove what happened;
  this violates the retained-evidence contract and the no-fabrication rule.
- **Delete the journal record:** Destroys the audit trail and breaks the
  invariant that retained records are never deleted.
- **Retire only the target:** The journal `Unresolved` count is session-wide, so
  another unproven peer would keep admission blocked.
- **Let the orchestrator write bbolt directly:** Bypasses authenticated
  ownership and the runtime's death proof.
- **Settle from native resume:** Resume must preserve uncertainty; settlement is
  an explicit retry action.

## Related records

- [Durable agent delivery requirements](../specs/platform/requirements/durable-agent-delivery.md).
- [Durable restart reconciliation requirements](../specs/platform/requirements/durable-restart-reconciliation.md).
- [Pre-intake delivery settlement and admission health](../specs/platform/system-design/durable-agent-delivery-settlement.md).
- [Pre-intake delivery settlement decision](2026-11-03-pre-intake-delivery-settlement.md).
