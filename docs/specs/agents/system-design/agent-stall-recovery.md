---
status: draft
system: agents
requirements:
  - REQ-AGENTS-AGENT-STALL-RECOVERY-001
created: 2026-09-02
updated: 2026-11-04
owners:
  - Kandev
---

# Agent Stall Recovery System Design

## Purpose and boundaries

The agent system owns the prompt watchdog: the clock that decides a prompt is
inactive, the classification of that inactivity as advisory or never-started,
the exact delivery identity published with a never-started stall, and the
validation and retirement of that prompt's retained journal record.

It does not own the persisted session and task state machine, the notice
rendering, the SQL delivery settlement, or the task-scoped stop path. The
orchestrator consumes the stall event: it validates ownership, tears the
execution down, settles the durable submission through the lifecycle and task
repositories, and applies either the recovered `WAITING_FOR_INPUT` outcome or
the legacy launch-failure transition. Reaching an execution from a task ID
stays with
[task stop reachability](../../tasks/system-design/task-stop-reachability.md).

## Requirement mapping

| Requirement | Design sections |
| --- | --- |
| `REQ-AGENTS-AGENT-STALL-RECOVERY-001` | [Prompt progress clock](#prompt-progress-clock), [Watchdog classification](#watchdog-classification), [Terminal teardown](#terminal-teardown), [Failure and recovery](#failure-and-recovery) |

## Components and responsibilities

- **`AgentExecution` activity state** (`internal/agent/runtime/lifecycle`) holds
  the per-prompt progress timestamp, the never-started discriminator, and the
  activity epoch under one mutex.
- **The lifecycle event recorder** classifies each inbound agent frame as a turn
  event or a metadata frame and advances progress state only for turn events.
- **The prompt watchdog** in the session manager ticks once per minute, takes a
  consistent snapshot, and publishes at most one stall event per prompt
  generation.
- **The orchestrator stall handler** validates the snapshot's ownership,
  persists the notice, and, for a never-started prompt in settlement scope,
  delegates to the settlement flow that tears down, settles, and applies the
  final outcome; every other never-started prompt keeps the synchronous
  launch-failure transition.
- **The lifecycle retirement validator** inspects the retained journal evidence
  for a never-started prompt (exact submission, complete unresolved inventory,
  no terminal evidence, exact hash and stream, proven-terminated owner) and
  retires the exact record under the zero-event authorization.
- **The agent manager's stop entry point** performs teardown. It stops the
  runtime and deregisters the execution without writing session state.

## Data and contracts

The published stall payload carries the delivery identity alongside the
existing fields (task, session, and execution identity, prompt generation,
last-activity time, elapsed duration, activity epoch, optional active-tool
identity, and the `NeverStarted` discriminator):
`DeliverySubmissionID`, `DeliveryStreamID`, `DeliveryIncarnationID`,
`DeliveryHarnessGeneration`, and the `OriginalRuntime` process identity of the
agentctl client that owns the prompt. The orchestrator cannot infer the exact
submission from the session alone, because queued or older submissions can
coexist with the active one; the identity must come from the execution that
owns the prompt. If the agentctl client or its identity is unavailable, the
fields stay empty and the orchestrator takes the fail-closed legacy path.

The prompt progress timestamp is the single clock behind that payload. Exactly
four inputs advance it:

| Input | Meaning |
| --- | --- |
| Prompt dispatch | A new prompt is armed; the clock restarts and the discriminator is cleared |
| Turn event | Assistant text, reasoning, tool call, tool update, plan, or permission request |
| Terminal completion | The prompt's own completion or error frame |
| User steer | New user input delivered into the running turn |

A metadata frame advances nothing. It is still processed, published, and
recorded for its own purposes; it is not evidence of progress.

The discriminator remains separate from the clock. Prompt dispatch clears it;
a turn event or terminal completion sets it. A user steer restarts the clock
without setting it, because user input is not agent output.

## Control flow

1. Prompt dispatch arms the clock and clears the discriminator.
2. Each inbound frame is classified. A turn event advances the clock, sets the
   discriminator, and bumps the activity epoch. A metadata frame does neither.
3. Every minute the watchdog snapshots the clock, the discriminator, and the
   epoch together. Once the elapsed time reaches five minutes it publishes one
   stall event for the prompt generation and does not publish again.
4. The orchestrator rejects the event when the session is no longer running,
   when cancellation is in flight, or when the execution, prompt generation, or
   activity epoch is no longer current.
5. An accepted advisory event persists the neutral running notice and stops.
6. An accepted never-started event checks the settlement scope (interactive,
   non-passthrough, non-Office durable-delivery session with a complete
   payload identity). In scope, it stamps the stalled prompt generation on the
   captured turn, claims teardown ownership, and starts the detached
   settlement flow: a bounded force-stop, then the journal retirement, the SQL
   settlement, and one of two final outcomes (settled or blocked). Out of
   scope, it persists the terminal error notice, applies the launch-failure
   transition, and then requests execution teardown.

### Advisory notice resolution

The action-message renderer derives advisory visibility from the existing
session, active turn, and session-message store. A running-only status notice
is hidden once an agent-authored content, reasoning, tool, plan, or permission
row for the same session and turn has a timestamp later than the notice's
creation. Use `updated_at` as well as `created_at`, preserving RFC3339
nanosecond precision: a tool can finish by updating a row created before the
notice. System status rows, queued user input, and other turns do not resolve
it. Terminal error diagnostics retain their settled rendering.

The store selector returns a boolean and subscribes only for running-only
status notices. Live message reducers record monotonic
`metadata.running_notice_resolved` on a loaded notice before discarding an
update whose older tool row is outside the loaded history window. Reconciliation
preserves this evidence through stale snapshots and includes it in message
identity even when the notice timestamp is unchanged.

The task service projects the same metadata on full and paginated transcript
reads. The repository aggregates the latest eligible agent creation/update
timestamp across the entire scoped turn without reading tool payloads. This
read projection does not change stored metadata or require a migration, and
query failures propagate rather than supplying incorrect evidence. Both live
events and reload therefore resolve notices independently of the loaded page.
The advisory message remains persisted and the watchdog still publishes at
most once per prompt generation; resolution adds no automatic cancellation.

### Prompt progress clock

Because the clock advances only on the four inputs above, an adapter that emits
metadata frames while producing no turn output cannot postpone detection. The
never-started elapsed time is measured from prompt dispatch, which is the
behavior the requirement describes.

The same clock also feeds the ten-minute completion-signal watchdog. That
consumer inherits the corrected semantics: a signalled session that has gone
quiet apart from metadata frames becomes reclaimable on schedule instead of
never. No separate clock is introduced for it.

### Watchdog classification

Classification is a pure read of the snapshot. An advisory stall leaves task
state, session state, prompt admission, and process liveness untouched. A
never-started stall stops the execution; its outcome is recoverable when the
zero-event settlement succeeds and terminal only when the evidence blocks the
settlement.

### Terminal teardown

In settlement scope, teardown runs before the final state is chosen. The
handler claims teardown ownership while it still holds the per-session guard,
stamps the stalled prompt generation on the captured turn, and runs the bounded
force-stop detached; the stop halts the runtime and deregisters the execution
without touching session rows. When the stop returns, the settlement flow
re-decides ownership from durable rows — the session's execution binding, the
prompt generation stamped on the captured turn, that turn still being the
active, non-terminal turn, and no cancel in flight — because the stop removes
the live execution. `RUNNING`, `WAITING_FOR_INPUT`, and `IDLE` sessions stay
eligible, so a prompt-error revert does not suppress recovery; a successor
execution or turn, a terminal session, or a user cancellation ends ownership.

The final outcome follows the settlement evidence. Settled: the session moves
to `WAITING_FOR_INPUT` with the stall error, the captured turn is completed or
error-terminated, the managed input settles failed without observed effects,
the task is reconciled to review, and at most one auto-resume may run. Blocked:
the legacy launch-failure transition applies and the session and task settle
`FAILED`, with the uncertain submission and its open block untouched. The
session stays `RUNNING` during the bounded stop-and-settle window instead of
taking a transient `FAILED` that a later correction would have to overwrite.

Teardown is requested with force, because a prompt that produced no frame is
not expected to answer a graceful protocol stop. The legacy path (missing
identity or out of scope) keeps the original order — record the failure first,
then tear down — so a teardown failure cannot replace the recorded `FAILED`
state with a cancellation state.

## Failure and recovery

- Teardown failure is logged with the session and execution identity. On the
  legacy path the session and task remain `FAILED`; the execution stays
  registered so a later task-scoped stop or startup cleanup can retry it.
- A late turn event that lands between the snapshot and the handler moves the
  activity epoch and the event is rejected, so a prompt that started late is not
  failed.
- Notice persistence failure is logged and does not block the final transition
  or the teardown.
- An already-removed execution reports not-found on teardown. That is the
  success condition, not an error, because no process remains.
- Blocked settlement evidence (identity missing, owner alive, journal conflict
  or unavailable, SQL failure, settlement timeout) falls back to the
  launch-failure outcome; the uncertain submission and its open block stay
  authoritative.
- A journal retirement that commits before a failing SQL settlement leaves the
  retired record as idempotent evidence: the SQL row stays uncertain and a
  later settlement or native resume can still match the retired peer, so no
  data is lost.
- A user action that supersedes the stalled generation during settlement keeps
  the delivery settlement but skips the session, turn, notice, and task writes,
  and skips auto-resume.
- A late prompt-delivery error for a settled submission creates no new
  `unknown_prompt_outcome` block, does not revert the session to `RUNNING`,
  and does not re-complete the captured turn.
- Duplicate stall events for the same session, execution, and prompt generation
  are deduplicated in memory while one settlement runs; the deterministic
  notice identity, the settlement effect, and the guarded writes keep replay
  idempotent.

## Persistence

No schema change. The clock, discriminator, and epoch stay in memory for the
lifetime of the execution. The durable records are the existing session state
and error message, task state, the notice message (deterministic identity per
session, execution, and prompt generation), the `failed` delivery submission
with its `agent_delivery.never_started_settlement:<submissionID>` effect, and
the prompt generation stamped on the captured turn. The settlement writes no
terminal event, terminal sequence, or `fully_settled_at`.

## Security

The notice copy and the log fields keep their existing bounds: identifiers,
durations, and tool display names only. Teardown adds no new user input path
and no new externally reachable operation.

## Observability

The existing single stall log per prompt generation is retained and keeps its
`never_started` field. Terminal handling adds one log line for the teardown
result, carrying task, session, and execution identity plus the outcome, so an
orphan left behind by a failed teardown is greppable.

The never-started settlement records `provider_stall_settlement_total` with a
closed outcome set (`settled`, `already_settled`, `blocked`, `partial_output`)
plus a closed blocked-reason set (`identity_missing`, `owner_alive`,
`journal_conflict`, `journal_unavailable`, `sql_failed`,
`settlement_timeout`), and `provider_stall_auto_resume_total` with
`attempted`, `succeeded`, `failed`, `skipped`. No task, session, execution,
submission, or payload identifier is a label.

## Related decisions

- [ADR-2026-07-29-agent-stall-user-controlled-recovery](../../../decisions/2026-07-29-agent-stall-user-controlled-recovery.md)
- [ADR-2026-08-18-never-started-agent-stall-terminal](../../../decisions/2026-08-18-never-started-agent-stall-terminal.md) (superseded)
- [ADR-2026-09-02-terminal-stall-owns-process-teardown](../../../decisions/2026-09-02-terminal-stall-owns-process-teardown.md)
- [ADR-2026-11-04-never-started-stall-recovery](../../../decisions/2026-11-04-never-started-stall-recovery.md)
