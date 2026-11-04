# ADR-2026-11-04-never-started-stall-recovery: Settle never-started stalls recoverable instead of failing them

**Status:** accepted
**Date:** 2026-11-04
**Area:** backend, protocol

## Context

A provider can hang while dispatching a prompt: the agent process stays alive
but produces zero turn events for the 300-second watchdog window. The
lifecycle watchdog classifies the prompt as never-started and publishes one
`agent.stalled` event. The orchestrator then treated this as a launch failure:
an error notice, the session and task to `FAILED`, a force-stop, and — because
the force-stop makes the prompt RPC return an uncertain-delivery error — the
SQL submission left `interrupted_unknown` / `prompt_dispatch_failed` with an
open `unknown_prompt_outcome` block.

ADR-2026-08-18 chose the terminal outcome so a failed launch would not stay
`RUNNING` forever. It also made the aftermath opaque: the user saw a generic
no-output error with no statement that the message was never delivered, and
recovery required a manual resume followed by manually re-sending the prompt.
The 2026-11-04 incident (session `6b9dd01c-352f-4b6a-97dc-2be0d4ad5642`) hit
this twice: one prompt waited 20 minutes for a manual resume, the other was
only delivered after the user re-typed it.

The durable state after the force-stop is strong evidence, not uncertainty:
zero turn events for the prompt generation, a retained journal record with no
terminal event and no terminal sequence, and an execution that the watchdog
itself stopped. Nothing observed the prompt, so nothing downstream can have
acted on it. Fail-closed does not require terminal: "provably no output and no
effects" should buy a recoverable session, an auditable settlement of the
submission, and one bounded automatic resume.

This is the fourth durable-delivery evidence class, next to terminal
settlement, [pre-intake settlement](2026-11-03-pre-intake-delivery-settlement.md),
and [abandoned settlement](2026-11-04-abandoned-delivery-settlement.md). A
native resume only retires the journal record and deliberately keeps the SQL
submission uncertain, and it leaves no retry entry point afterwards, so the
submission can never settle once resumed.

## Decision

- **Recoverable outcome.** A never-started stall in scope (interactive,
  non-passthrough, non-Office durable-delivery session) is a provider turn
  failure, not a launch failure. After a bounded force-stop, the settlement
  flow applies: session `WAITING_FOR_INPUT` with a stall error naming the
  provider stall and the watchdog stop, the captured turn completed or
  error-terminated, managed input settled failed with
  `execution_failed_without_observed_effects`, the task reconciled to review,
  and one persisted notice stating the message was not delivered. Blocked
  evidence keeps the existing fail-closed path: session and task `FAILED`, the
  uncertain submission, and its open block.
- **Evidence thresholds.** Settlement runs only when all of the following
  hold, and performs zero journal or SQL writes otherwise: the watchdog
  attests zero turn events for the current prompt generation and activity
  epoch; the stall payload carries the exact delivery identity (submission,
  stream, incarnation, harness generation) plus the original runtime identity,
  captured from the execution; the authenticated retained journal's complete
  unresolved inventory is exactly the target with no terminal event and no
  terminal sequence; session, incarnation, generation, stream, submission, and
  payload hash match the SQL row; and the historical owner process is proven
  terminated. A live agentctl instance that still owns the session blocks
  retirement.
- **Journal retirement.** Retirement keeps the default rule that a dispatch
  label alone is not evidence an external call did not occur. A new
  target-bound zero-event authorization (`NeverStartedSubmissionID`) allows
  exactly one named record — including a `dispatching` one — to retire;
  without it, non-`interrupted_unknown` targets stay non-retireable.
  Retirement preserves payload and hash, normalizes non-terminal states to
  `interrupted_unknown`, never deletes the record, and never writes terminal
  evidence.
- **SQL settlement.** A sibling transaction marks the submission
  `failed`/`failed` with effect `agent_delivery.never_started_settlement:<submissionID>`,
  keeps `terminal_sequence=0` and `fully_settled_at` NULL, and resolves only
  the blocks bound to that exact submission. It accepts
  `dispatching`/`dispatching` or `interrupted_unknown`/`prompt_dispatch_failed`
  observed immediately before the call. A replay that finds the effect returns
  already-settled and resolves only still-open bound blocks; a `failed` row
  without the effect is a conflict, not a silent success.
- **Journal and SQL converge in one flow.** Settling only the SQL side blocks
  a later native resume on disagreeing stores; retiring only the journal leaves
  a permanently uncertain row. If the journal retirement commits and the SQL
  transaction fails, the retired record remains idempotent evidence and the
  SQL row stays resumable.
- **Ownership from durable rows.** The final session and turn writes are
  guarded by durable state — the session's execution binding, the prompt
  generation stamped on the captured turn at stall time, that turn still being
  the active non-terminal turn, and no cancel in flight — because the
  force-stop removes the live execution from the runtime store. `RUNNING`,
  `WAITING_FOR_INPUT`, and `IDLE` are eligible, so a prompt-error revert does
  not suppress recovery.
- **Late signals are terminal-aware.** The force-stopped prompt's own error
  handler checks the submission before reverting state or creating a block, so
  a late uncertain-delivery signal for a settled submission creates no new
  block, does not revert the session to `RUNNING`, and does not re-complete
  the turn.
- **Bounded auto-resume.** After a recovered outcome, at most one
  `tryEnsureExecution` launch per stall episode (60-second budget), without
  dispatching the historical prompt, honoring the archived, deferred-launch,
  auto-start-disabled, Office/automation, and passthrough guards, with no
  retry on failure. The watchdog publishes one stall event per prompt
  generation, so "once per episode" is naturally bounded.
- **Partial output stays advisory.** Any turn event since dispatch keeps the
  existing advisory notice and Cancel turn: partial output may have produced
  effects, so it must stay uncertain.
- **Observability.** `provider_stall_settlement_total{outcome,reason}` and
  `provider_stall_auto_resume_total{outcome}` with closed label sets; no task,
  session, execution, submission, or payload identifier is a label.

This decision supersedes
[ADR-2026-08-18-never-started-agent-stall-terminal](2026-08-18-never-started-agent-stall-terminal.md)
in full: the never-started classification itself (the discriminator, the
activity-epoch guard, and process teardown) is unchanged, but the `FAILED`
session outcome is replaced by the settled/blocked split above.

## Consequences

A settled never-started stall becomes a normal recoverable provider failure:
the message states it was not delivered, the composer accepts a new prompt,
and one automatic resume removes the manual-resume step. The session stays
`RUNNING` for the bounded stop-and-settle window (~50 seconds) instead of
flipping through a transient `FAILED`, which would fire the frontend failure
toast for a recoverable outcome. The user sees one definitive notice after the
settlement rather than an immediate generic error.

A live agentctl instance that still owns the session blocks settlement; the
normal force-stop deregisters it, and a stuck teardown stays fail-closed. A
resumed agent can boot while the provider is still dead, and the next user
prompt may stall again — that is a new episode with its own bounded attempt,
not a retry loop. The two incident rows are not backfilled automatically; they
can be settled later through the same contract. A rollback treats settled
submissions and retired records as terminal evidence and does not reactivate
them.

## Alternatives considered

- **Keep the terminal `FAILED` outcome.** Rejected: it hides the one fact the
  user needs (the message was not delivered) and provides no recovery path
  even though the evidence proves no effects occurred.
- **Mark `FAILED` first, correct to `WAITING_FOR_INPUT` after settlement.**
  Rejected: the transient `FAILED` fires the failure toast for a recoverable
  outcome and invites state races.
- **Settle only the SQL submission.** Rejected: an unresolved journal record
  blocks a later native resume, so the stores would disagree.
- **Retire only the journal record.** Rejected: the SQL row stays permanently
  uncertain, exactly the incident state.
- **Reuse the pre-intake or abandoned effect type.** Rejected: pre-intake means
  the submission never reached the journal; abandoned means agentctl exited
  before writing terminal evidence and the record was already
  `interrupted_unknown`. This case has a live dispatch that the watchdog
  stopped, so the audit trail needs its own effect.
- **Relax journal retirement generically for `dispatching` records.** Rejected:
  it would turn a dispatch label into evidence of no external call. The
  explicit target-bound authorization keeps the default fail-closed.
- **Auto-replay the historical prompt.** Rejected: replay risks duplicate
  effects and turns the settlement into a resend entry; resume only restarts
  the agent.
- **An agentctl first-event dispatch timeout.** Deferred: the 300-second
  watchdog already bounds the wait; this change owns the aftermath.

## Related records

- [Agent stall recovery requirements](../specs/agents/requirements/agent-stall-recovery.md).
- [Agent stall recovery system design](../specs/agents/system-design/agent-stall-recovery.md).
- [Durable agent delivery requirements](../specs/platform/requirements/durable-agent-delivery.md).
- [Pre-intake delivery settlement and admission health](../specs/platform/system-design/durable-agent-delivery-settlement.md).
- [Superseded ADR-2026-08-18-never-started-agent-stall-terminal](2026-08-18-never-started-agent-stall-terminal.md).
