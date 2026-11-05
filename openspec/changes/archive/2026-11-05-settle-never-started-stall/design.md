# Design: settle-never-started-stall

## Context

A never-started stall is a prompt that produced zero turn events for 300 seconds while the agent process stayed alive. Today the lifecycle watchdog publishes one `agent.stalled` event with `never_started=true`; the orchestrator posts an error notice, marks the session and task `FAILED`, and force-stops the execution. The force-stop makes the prompt RPC return `ErrUncertainPromptDelivery`; the prompt path then records the SQL submission as `interrupted_unknown` / `prompt_dispatch_failed` and creates an `unknown_prompt_outcome` block. A manual resume retires the matching journal record and resolves the block, but deliberately leaves the SQL submission uncertain. After that resume there is no retry entry point, so the submission can never be settled.

The incident behind this change (session `6b9dd01c-352f-4b6a-97dc-2be0d4ad5642`, 2026-11-04) shows the full path twice. Two prompts (`prompt:b5d60bea`, `prompt:0e9ea5c7`) are still `interrupted_unknown` / `prompt_dispatch_failed` with `terminal_sequence=0` and `fully_settled_at=NULL`; the journal records ended `completed` + `retired=true` with no terminal event and no terminal sequence. The second prompt was only delivered after the user re-typed it following a manual resume.

The change adds a fourth durable-delivery settlement: zero-event + no-terminal-evidence + proven-dead-owner. It also changes the never-started stall outcome from terminal `FAILED` to a recoverable `WAITING_FOR_INPUT` with a clear notice and one bounded automatic resume. Partial-output stalls remain advisory and unchanged.

## Goals / Non-Goals

**Goals:**

- Settle a never-started durable submission as `failed` using the existing conservative evidence model, and retire the exact journal record in the same auditable flow.
- Leave the session recoverable (`WAITING_FOR_INPUT`) with a notice that states the message was not delivered.
- Perform at most one automatic resume per stall episode, without replaying the historical prompt.
- Keep all existing fail-closed paths: any missing or conflicting evidence leaves the submission uncertain and the block open.
- Make the settlement and auto-resume observable through bounded expvar counters.

**Non-Goals:**

- Do not settle a stall that observed any turn event (partial output).
- Do not backfill the two already-stuck incident rows automatically.
- Do not add an agentctl first-event dispatch timeout in this change.
- Do not change Office, automation, or passthrough stall ownership.
- Do not redispatch the historical prompt or release unrelated queue entries.
- Do not delete journal records or fabricate terminal events, sequences, turns, or `fully_settled_at`.

## Decisions

### D1: The lifecycle event carries the exact delivery identity and runtime proof

`AgentStalledPayload` gains `DeliverySubmissionID`, `DeliveryStreamID`, `DeliveryIncarnationID`, `DeliveryHarnessGeneration`, and `OriginalRuntime processidentity.Identity`. `PublishAgentStalled` fills them from the execution (`deliverySubmissionIDSnapshot()`, `DeliveryStreamID`, `DeliveryIncarnationID`, `DeliveryHarnessGeneration`) and from `execution.AcquireAgentCtlClient().RuntimeProcessIdentity()`. If the client is nil or identity validation fails, the fields are left empty and the orchestrator takes the fail-closed fallback. The payload is internal; no wire compatibility is involved.

Reason: the orchestrator cannot safely infer the exact submission from the session alone. A queued or older submission can coexist with the active one; the exact identity must come from the execution that owns the prompt.

### D2: The never-started handler chooses the final state after teardown and settlement

The handler no longer calls `recordSessionLaunchFailure` synchronously. It validates ownership/epoch, captures the turn ID and payload identity, claims teardown, and runs the existing detached force-stop. When the stop returns (success or `ErrExecutionNotFound`), the detached goroutine attempts the settlement and then applies one of two final outcomes:

- **Settled:** session `WAITING_FOR_INPUT` with the stall error message, turn completed/abandoned, managed input failed, task review state reconciled, notice posted, one auto-resume attempted.
- **Blocked:** `recordSessionLaunchFailure` with a stall-specific error, session/task `FAILED`, existing `interrupted_unknown` + open block preserved, fallback notice posted, no auto-resume.

During the bounded stop+settle window the session remains `RUNNING`; the prompt path's own error handling may already revert it to the previous state. This window is bounded by `neverStartedStopTimeout` (30s) plus a settlement timeout (20s). The alternative — marking `FAILED` immediately and correcting to `WAITING_FOR_INPUT` — would fire the frontend failure toast for a recoverable outcome, so it is rejected.

Reason: the final state must reflect the settlement evidence. A transient `FAILED` would produce the exact opaque-and-terminal experience this change removes.

### D3: Journal retirement gets an explicit zero-event authorization

The retained retirement contract keeps its default: only `interrupted_unknown` targets are retireable. A new request field, `NeverStartedSubmissionID`, authorizes exactly one target to be retired from any state that has no terminal evidence (`prepared`, `accepted`, `dispatching`, `interrupted_unknown`, `completed`, `failed`, `cancelled`). The authorization is bound to the exact submission ID; all other targets still require `interrupted_unknown`. The retirement normalizes a non-terminal state to `interrupted_unknown` (existing `RetireSubmission` behavior) and preserves payload and hash. The complete-unresolved-inventory rule and the proven-dead-owner rule remain.

The zero-event authorization is only available on the retained (unowned) path. If a live agentctl instance still owns the session, the lifecycle method returns blocked and the orchestrator takes the fail-closed fallback. This is deliberate: the live per-instance retire endpoint requires `HasUnprovenOutcome`, and extending it would widen the authorization to a live dispatcher. The force-stop deregisters the instance before `StopAgentWithReason` returns, so the retained path is the normal case.

Reason: SQL `failed` + unresolved journal (dispatching/unknown) blocks a later native resume. Settling only one side is not a valid outcome. The explicit authorization keeps the existing "a dispatch label alone is not evidence" invariant: the authorization is a separate, auditable input, not a relaxation of the generic retire rule.

### D4: The SQL settlement is a new sibling transaction with its own effect

New repository method `SettleNeverStartedAgentDeliverySubmission` and new effect type `agent_delivery.never_started_settlement:<submissionID>`. The transaction reuses the pre-intake settlement structure: session/turn locks, load the exact submission, CAS-check the expected state/outcome, `terminal_sequence=0`, `fully_settled_at IS NULL`, mark `failed`/`failed`, insert the effect, resolve only the open blocks bound to that exact submission (any stream ID), advance the recovery revision, commit. It accepts `dispatching`/`dispatching` or `interrupted_unknown`/`prompt_dispatch_failed`; the caller reads the observed state immediately before the call and retries once on a conflict.

Replay semantics: a submission already `failed` with the effect present returns `AlreadySettled` and resolves only the still-open bound blocks. A failed submission without the effect is a conflict, not a silent success.

Reason: the pre-intake effect type means "never entered the journal"; this case has a journal record, so reusing it would make the audit trail false. `fully_settled_at` stays NULL because no terminal event, sequence, or turn exists.

### D5: The prompt error path becomes terminal-aware

The settlement and the prompt goroutine race. Two guards make both orders converge:

1. **Block guard:** `finishPromptExecutorDispatch` checks the submission after `markInterrupted`. If the row is already terminal (the settlement won, or another terminal path won), it does not call `deliveryRecoveryError`, so no `unknown_prompt_outcome` block is created for a settled submission.
2. **State guard:** `handlePromptError` receives the delivery submission ID through `finishPromptDispatchFailure` → `handlePromptDispatchFailure`. If the submission is already terminal, it skips the revert to `previousSessionState` and the turn completion; the settlement owns those writes.

The DB is the source of truth, so the guards work regardless of which goroutine wins and survive a backend restart in the middle. The settlement's in-memory dedupe (`sessionID + executionID + promptGeneration`) only prevents duplicate notice/auto-resume when the same stall event is delivered twice.

Reason: the existing code always reverts to `previousSessionState` and always creates an uncertain block. Once the settlement makes the submission terminal `failed`, both actions would corrupt the recoverable outcome.

### D6: The session outcome reuses the provider-turn-failure pipeline

The settled path uses the same primitives as `settleRetainedTurnFailure`: patch the captured turn as error-terminated, settle the managed input as `failed` with `execution_failed_without_observed_effects`, complete the turn, set the session `WAITING_FOR_INPUT` with the stall error message, and reconcile the task to REVIEW via `writeTaskReviewState`. The notice is the user-facing signal; the task does not transition to `FAILED`.

The final session/turn write is guarded by the per-session cancel-in-flight guard and re-validates that the session still belongs to the stalled generation. If a user action intervened, the settlement still records the delivery outcome but does not overwrite the user's state. Auto-resume is skipped in that case.

Reason: `settleRetainedTurnFailure` already encodes the "provider failed without observed effects" semantics, including the managed-input disposition and the recoverable session state. Reusing it keeps the two provider-failure paths consistent.

### D7: Auto-resume is one launch per stall episode, no prompt replay

After the settled session is `WAITING_FOR_INPUT`, the detached flow checks `autoResumeEligibility`, archived/Office/automation/passthrough exclusions, and the existing auto-start gate, then calls `tryEnsureExecution(ctx, sessionID, seam3CallShapeViewing, launchOriginAutomatic, "")` once with a bounded context (60s). It does not call `RetrySessionDelivery` (which never dispatches the historical prompt but also never restarts the runtime) and it never re-dispatches the prompt. A failure is recorded and leaves the session recoverable; no retry loop is scheduled.

Reason: the watchdog publishes one stall event per prompt generation, so "one per episode" is naturally bounded. Auto-resume has no duplicate-prompt risk, unlike auto-redelivery.

### D8: Observability uses bounded expvar counters

New counters under `internal/orchestrator/provider_stall_metrics.go`, following the `office_stall_metrics.go` encoding:

- `provider_stall_settlement_total{outcome,reason}`: outcome in `settled`, `already_settled`, `blocked`, `partial_output`; reason in the closed set `identity_missing`, `owner_alive`, `journal_conflict`, `journal_unavailable`, `sql_failed`, `settlement_timeout`.
- `provider_stall_auto_resume_total{outcome}`: `attempted`, `succeeded`, `failed`, `skipped`.

No task, session, submission, execution, or payload identifier is a label. Root `CLAUDE.md` gains the corresponding observability entries.

### D9: Scope is interactive, non-passthrough, non-Office

The new settlement and recoverable outcome apply only to durable-delivery sessions whose task origin is interactive (not Office/automation) and whose session is not passthrough. Other origins keep the current stall handling. The guard is applied before teardown/settlement so those sessions never reach the new path.

Reason: Office and automation own their retry/concurrency semantics; changing their stall outcome would alter scheduler behavior outside this change.

## Components and Files

| Component | Responsibility |
|-----------|----------------|
| `internal/agent/runtime/lifecycle/event_types.go`, `events.go` | Publish exact delivery identity + runtime proof in the stall payload. |
| `internal/agent/runtime/lifecycle/delivery_record_retirement.go` | Add `RetireNeverStartedDeliverySubmission`; validate exact evidence; only retained path. |
| `internal/agentctl/journal/retained_retirement.go`, `journal_submissions.go` | `NeverStartedSubmissionID` authorization; allow non-terminal target with no terminal evidence. |
| `internal/agentctl/server/api/retained_delivery.go` | Accept and validate the new retirement field. |
| `internal/task/models/agent_delivery_never_started_settlement.go` | Effect type/key, request/result types. |
| `internal/task/repository/interface.go`, `sqlite/agent_delivery_never_started_settlement.go` | Settlement transaction and block resolution. |
| `internal/orchestrator/never_started_stall_settlement.go` | Detached settle flow, notice, turn/session/task writes, auto-resume, fallback. |
| `internal/orchestrator/event_handlers_stall.go` | Never-started branch delegates to the settle flow; advisory branch unchanged. |
| `internal/orchestrator/task_operations.go`, `agent_delivery_submission.go` | Terminal-aware prompt failure guards. |
| `internal/orchestrator/provider_stall_metrics.go` | Bounded expvar counters. |
| `docs/specs/agents/*`, `docs/specs/platform/*`, `docs/decisions/*`, root `CLAUDE.md` | Durable spec, design, ADR, observability updates. |

## Data Contracts

```go
// lifecycle payload additions
type AgentStalledPayload struct {
    // existing fields...
    DeliverySubmissionID     string
    DeliveryStreamID         string
    DeliveryIncarnationID    string
    DeliveryHarnessGeneration uint64
    OriginalRuntime          processidentity.Identity
}

// lifecycle retirement
type NeverStartedDeliveryRetirementRequest struct {
    TaskID, SessionID, ExecutionID, IncarnationID string
    HarnessGeneration uint64
    SubmissionID, StreamID, PayloadHash string
    OriginalRuntime processidentity.Identity
}
type NeverStartedDeliveryRetirementResult struct {
    Retired journal.Submission
    Descriptor journal.RecoveryDescriptor
    ProcessTerminated bool
}

// SQL settlement
type AgentDeliveryNeverStartedSettlementRequest struct {
    SessionID, IncarnationID string
    HarnessGeneration int64
    SubmissionID string
    ExpectedState models.DeliverySubmissionState
    ExpectedOutcome string
    BlockID string
    SettledAt time.Time
}
type AgentDeliveryNeverStartedSettlementResult struct {
    Settled bool
    AlreadySettled bool
    Submission *models.AgentDeliverySubmission
    ResolvedBlockIDs []string
}
```

The journal retirement request adds `NeverStartedSubmissionID string` (empty means the existing contract). The control API body limit stays 32 KiB; the request shape grows by one bounded ID.

## Control Flow

1. Watchdog publishes `agent.stalled` with `NeverStarted=true`, exact submission/stream/incarnation/generation, and `OriginalRuntime`.
2. `handleAgentStalled` validates session RUNNING, ownership, prompt generation, activity epoch, and active turn. Advisory stalls stop here with the existing notice.
3. Never-started: capture the turn ID; claim teardown; run the bounded force-stop detached.
4. After stop: `settleNeverStartedStall`:
   - Dedupe by session+execution+generation.
   - If the payload identity or runtime proof is incomplete, fall back.
   - `agentManager.RetireNeverStartedDeliverySubmission` (20s): inspect retained evidence with `StoredSubmissionID` + `OriginalRuntime`; require the descriptor inventory to be exactly the target, no terminal event/sequence, exact hash/stream/generation, `ProcessTerminated=true`; then retire with `NeverStartedSubmissionID`.
   - Load the SQL submission; call `SettleNeverStartedAgentDeliverySubmission` with the observed state/outcome (retry once on conflict). The transaction resolves only the exact bound block.
   - On success: patch the turn, settle managed input failed, complete the turn, set session `WAITING_FOR_INPUT` + stall error under the cancel guard, `writeTaskReviewState`, post the success notice, record metrics, attempt one auto-resume.
   - On any failure: `recordSessionLaunchFailure` with the stall error, post the fallback notice, record metrics, no auto-resume.
5. A late prompt error checks the submission terminal state before creating a block or reverting state.

## Error Handling and Fallback

| Failure | Outcome |
|---------|---------|
| Payload identity/runtime missing | Fallback: FAILED + existing uncertain path; metric `identity_missing`. |
| Journal descriptor truncated/mismatched/missing | Fallback; metric `journal_conflict`/`journal_unavailable`. |
| Owner not proven terminated (including a live instance) | Fallback; metric `owner_alive`. |
| Journal retired, SQL transaction fails after bounded retries | Fallback state write; the retired journal record still matches the SQL unknown row, so a later resume remains possible; metric `sql_failed`. |
| Session changed by a user action during settlement | Delivery settlement still recorded; session state write skipped; auto-resume skipped. |
| Duplicate stall event | In-memory dedupe returns; journal/SQL/notice are idempotent. |

## Testing Strategy

- Journal: zero-event authorization retires `dispatching`; the same target without authorization fails; other targets still require `interrupted_unknown`; payload/hash preserved; state normalized; complete-inventory rule enforced.
- Lifecycle: exact evidence success; truncated descriptor; terminal evidence; hash/stream/generation mismatch; owner not terminated; live instance blocked; idempotent retirement.
- Repository: settle from `dispatching` and from `interrupted_unknown`; effect uniqueness; idempotent replay; terminal-sequence conflict; block with non-empty stream resolved; unrelated block preserved; SQLite and gated PostgreSQL behavior.
- Orchestrator: success path (submission settled, session WAITING_FOR_INPUT, task not FAILED, notice, turn completed, one auto-resume); blocked path (FAILED + block, no auto-resume); partial-output unchanged; late prompt error no block/no revert; scope guards; duplicate stall event deduped.
- Service-level integration: real SQLite repository + real bbolt journal + fake runtime control; exercise the full success and fallback paths; assert the durable rows, journal retirement, session state, notice, and resume call count.

## Risks / Trade-offs

- **Transient RUNNING window:** the session stays RUNNING until the stop+settlement completes (bounded ~50s). Accepted to avoid a false FAILED toast; the watchdog already waited 300s.
- **Notice delayed until the outcome:** one definitive notice instead of two; the user sees the message after the bounded settlement. Accepted for correct wording.
- **Live-instance retirement unsupported:** if the agentctl instance is still registered, the settlement falls back. The normal force-stop deregisters it; a stuck teardown remains fail-closed.
- **Retired journal + failed SQL:** the journal is already retired when the SQL transaction fails. The SQL row remains `interrupted_unknown`; a later resume can still match the retired peer by ID/hash/generation. No data is lost.
- **Auto-resume does not test the provider:** a resumed agent can boot while the upstream is still dead; the next user prompt may stall again. That is a new episode with its own bounded attempt, not a loop.
- **No backfill:** the two incident rows stay uncertain until a separate manual action; this change does not add a sweep.

## Migration / Compatibility

No schema migration. The effect type is a new text value in the existing `agent_delivery_effects` table. The journal retirement request adds a backward-compatible optional field; old requests behave exactly as before. Old backends ignore the new stall payload fields (they are internal). A rollback leaves already-retired journal records and settled `failed` submissions terminal; old code treats them as terminal and does not reactivate them.

## Open Questions

None blocking. Deferred explicitly: agentctl first-event dispatch timeout; automatic backfill of the two incident rows; a live-instance zero-event retirement endpoint; Office/automation/passthrough adoption of the recoverable outcome.
