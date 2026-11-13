---
status: current
system: agents
requirements:
  - REQ-AGENTS-AGENT-STALL-RECOVERY-001
created: 2026-09-02
updated: 2026-11-13
owners:
  - Kandev
---

# Agent stall recovery system design

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

Platform owns durable cancellation uncertainty and
[interrupted-prompt recovery](../../platform/system-design/interrupted-prompt-recovery.md).
The [silent prompt work package](../../../plans/silent-prompt-recovery/plan.md) records implementation
and verification of the advisory path.

## Requirement mapping

| Requirement | Design sections |
| --- | --- |
| REQ-AGENTS-AGENT-STALL-RECOVERY-001, .1-.9 | Progress clock, advisory notice resolution |
| REQ-AGENTS-AGENT-STALL-RECOVERY-001, .10-.15 | Never-started settlement, watchdog and cancellation |
| REQ-AGENTS-AGENT-STALL-RECOVERY-001, .16-.19 | Advisory notice, acceptance evidence, cancellation, diagnostic snapshot |

Never-started stalls settle recoverably under the
[ADR-2026-11-04 decision](../../../decisions/2026-11-04-never-started-stall-recovery.md);
the historical terminal rule is superseded.

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
- **`SessionManager.markPromptDispatched`** and the execution store record the
  current accepted generation after `triggerPrompt` succeeds. Acceptance is
  distinct from output and effect evidence.
- **`recordActivity`** (`manager_events.go`) classifies content and metadata
  frames for the progress clock.
- **Existing cancellation services** own protocol cancellation, bounded
  escalation, state projection, and durable recovery blocks. The watchdog does
  not bypass their owner checks or settle their submission records.

## Acceptance evidence

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

Use the current `dispatchedPromptGeneration` boundary for ordinary dispatch.
Expose acceptance in a snapshot paired with execution ID and prompt generation.
Take lifecycle ownership before activity state, following the existing lock
order; revalidate the same generation at the consumer. Do not read mutable
fields without their owner or infer acceptance from a request being sent.

Restore may establish acceptance only from the authenticated active durable
submission with matching session, execution, incarnation, stream and generation.
Native session identity, `session_info`, usage, and a prior turn's acceptance
are insufficient. Unsupported or missing evidence is unconfirmed and remains
advisory. A provider-specific native start event is optional supporting evidence,
not a prerequisite or a new public ACP capability.

Do not set `agentEventSincePrompt` for acceptance. The existing
`promptAttemptEvidenceSnapshot` uses it for both `OutputObserved` and
`EffectObserved`. Acceptance alone must not change those fields. Preserve
content that arrives before the dispatch acknowledgment; recording acceptance
must not reset already-observed output, effects, or the newer progress clock.
Completion or a successor generation invalidates the old acceptance snapshot.

## Progress clock

Retain the one-minute watchdog and five-minute threshold. Dispatch arms the
clock. Genuine content, reasoning, tool calls/updates, plans, permission requests,
terminal completion, and delivered same-turn user input retain their existing
clock behavior. Metadata and acceptance acknowledgments do not repeatedly reset
it. The completion-signal watchdog retains this same progress meaning and its
separate explicit-signal recovery policy.

## Watchdog and cancellation

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

An accepted advisory event never writes a launch-failure state or forces a
stop, including when acceptance is unconfirmed. On user **Cancel turn**, use
the existing `agent.cancel` route with the captured execution, turn, and
generation; busy state prevents duplicate operations. Confirmed terminal
cancellation follows normal reconciliation. A notification send, HTTP
acknowledgment, released local waiter, or timeout is not itself a confirmed
durable outcome; missing outcome evidence retains the matching block and
prevents prompt or queue dispatch.

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

## Desktop and phone

Reuse `action-message.tsx`, `action-message-actions.tsx`, and the current
`task-layout.tsx` phone chat composition. The transcript owns scrolling.
The short status and inline content-width Cancel turn control need no overlay
or new navigation. Phone and coarse-pointer targets remain at least 44px;
compact fine-pointer desktop styling remains unchanged. Reuse the shared
localized labels, keyboard behavior, cancellation pending state, and recovery
surface. No new animation or alert decoration is needed.

## Diagnostic snapshot

Extend the existing first-stall structured log. Emit at most one record per
execution/prompt generation before any subsequent user cancellation. Capture
from in-memory or already authenticated state; do not perform provider I/O
while holding the activity or cancellation lock.

Classification is a pure read of the snapshot. An advisory stall leaves task
state, session state, prompt admission, and process liveness untouched. A
never-started stall stops the execution; its outcome is recoverable when the
zero-event settlement succeeds and terminal only when the evidence blocks the
settlement.

The same record may include bounded stream-stage counters and ages for adapter
updates, agentctl writes, backend reads, and durable projection. An internal
heartbeat is sent only when the backend requests that capability; retained
older peers may omit it. Metadata is not a turn event or prompt progress.
Unavailable or stale measurements remain unknown, and a later connection's
projection counters cannot be replaced by an older connection.

Allowlist: task/session/execution identity; prompt and harness generations;
submission and stream identity when known; acceptance state; output/effect
booleans; last-progress time and elapsed milliseconds; agent type; managed
adapter and CLI versions when already known; current delivery state; received
and projected sequences when cached; the latest normalized retry/transport
category only if already observed. Unknown fields stay absent or explicitly
unknown. Do not infer bundled Codex version from the host CLI version.
Output and effect booleans share conservative turn-content evidence, as in
`PromptAttemptEvidence`; they do not independently prove persisted effects.

Use fixed enums, typed numbers, and strings capped at 128 UTF-8 bytes, with a
4 KiB total encoded record limit. Omit malformed UTF-8 input. Truncate valid
strings at the last complete rune boundary within the byte cap. Optional values
that fail validation are omitted. No prompts, environment, URLs, paths, tool
arguments, free-form provider errors, raw ACP frames or private logs enter the
record. Use the existing rotated backend log retention; no new table, file
writer or high-cardinality metric. Snapshot failure is logged as a fixed
category and cannot change the turn, notice or cancel availability.

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

## Verification

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

Use deterministic clocks to cover accepted silence, metadata-only traffic,
missing acceptance, late acknowledgment/output, completion races, restored
acceptance, and stale generations. Confirm the output/effect snapshot remains
unchanged by acceptance. Advisory-path handler tests assert no terminal state
write or stop; never-started tests assert the settlement outcomes above.

## Persistence

No schema change. The clock, discriminator, and epoch stay in memory for the
lifetime of the execution. The durable records are the existing session state
and error message, task state, the notice message (deterministic identity per
session, execution, and prompt generation), the `failed` delivery submission
with its `agent_delivery.never_started_settlement:<submissionID>` effect, and
the prompt generation stamped on the captured turn. The settlement writes no
terminal event, terminal sequence, or `fully_settled_at`.

No schema migration. Acceptance snapshots are execution-scoped and rebuilt
from current authenticated evidence on adoption. Notices and recovery blocks
keep existing persistence. Capture logs are diagnostic, never recovery proof.
All managed providers use the same policy; passthrough sessions retain their
existing exclusions. Office completion and scheduling rules remain separate.

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
- [ADR-2026-10-20-silent-prompt-acceptance](../../../decisions/2026-10-20-silent-prompt-acceptance.md)
- [ADR-2026-08-02-agent-terminal-diagnostics-over-stderr](../../../decisions/2026-08-02-agent-terminal-diagnostics-over-stderr.md)
