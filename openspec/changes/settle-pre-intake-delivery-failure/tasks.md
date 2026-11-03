# Pre-Intake Delivery Settlement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Apply tasks in order; each task ends with an independently testable deliverable.

**Goal:** Make definitively-undelivered durable prompts recoverable instead of permanently deadlocking SQL and the agentctl journal.

**Architecture:** Add explicit journal-absence evidence, a transactional pre-intake settlement in the SQLite repository, a retry/typing branch in the orchestrator, and admission-health truthfulness in agentctl. Write path prevention is separated from recovery: agentctl refuses before journal intake with a typed reason; the backend settles the rejected submission immediately; historic rows are settled by `session.recover` after authenticated evidence proves absence.

**Tech Stack:** Go backend, SQLite/PostgreSQL repository with real SQLite tests and env-gated PostgreSQL tests, bbolt agentctl journal, expvar metrics, testify.

**Spec:** `openspec/changes/settle-pre-intake-delivery-failure/proposal.md`, `design.md`, `specs/durable-delivery-pre-intake-settlement/spec.md`, `specs/durable-delivery-admission-health/spec.md`.

## Global Constraints

- No product code may hand-edit SQLite. Every durable state change goes through a repository transaction.
- Every behavior change needs a `*_test.go` alongside the source.
- No new table or column is planned. If one becomes necessary, use an additive migration in `runMigrations()` plus SQLite fresh/replay and PostgreSQL behavior coverage.
- Do not change successful terminal settlement, workflow outcome, or native-resume semantics.
- Keep `-trimpath` on Go build/test commands (repo build-cache rule).
- Commit format: Conventional Commits, header ≤100 chars; end commit bodies with `Co-Authored-By: Claude Code <noreply@anthropic.com>`.
- No new user-facing copy and no frontend changes; the existing `settled` copy is reused.
- Keep durable-delivery metrics label sets closed and bounded.

## Review Focus

These are the failure modes the spec implies but no single task's happy-path tests exercise. Each owning task adds the named test.

1. **Journal lost/replaced/unreachable** must keep the submission uncertain, never settle it (Task 3).
2. **Another unresolved journal candidate** must produce `retained_evidence_ambiguous`, not settle (Task 3).
3. **Block bound to a non-empty stream or another submission** must not be settled by the pre-intake transaction (Tasks 2, 3).
4. **Admission failure after the journal record was written** (transition failure) must stay uncertain and must not take the immediate-settlement path (Task 4).
5. **Oversized event or genuine storage exhaustion** must not wedge admission forever; oversized events are skipped observably, exhaustion stays fail-closed (Task 5).

## Verification Strategy

Primary verification type: **integration-test** over real components. Unit tests cover pure helpers, but acceptance is proven by:

- `apps/backend/internal/task/repository/sqlite`: real SQLite database, migrations, transaction and locking behavior; `KANDEV_TEST_POSTGRES_DSN` gated behavior test for the new transaction.
- `apps/backend/internal/agentctl/journal`: real bbolt journals for absence evidence, oversized-event classification, and retirement invariants.
- `apps/backend/internal/orchestrator`: real SQLite repository plus real bbolt journal through the existing `newServiceWithRealRepo`, `seedSession`, and `retainedJournalRecoveryManager` fixtures; fake agent manager only at the runtime boundary.
- `apps/backend/internal/agent/runtime/lifecycle` and `agent/runtime/agentctl`: real evidence transport and typed-error propagation.
- `apps/backend/internal/agentctl/server/process` and `server/api`: real manager/journal with the existing pressure harness.

No Playwright spec is added because this change has no UI surface change; the retry action and `settled` rendering already exist. A post-deploy manual check on the affected session is listed in the verification checklist.

Commands:

```bash
cd apps/backend
go test -trimpath -race ./internal/task/repository/sqlite ./internal/orchestrator \
  ./internal/agent/runtime/lifecycle ./internal/agent/runtime/agentctl \
  ./internal/agentctl/journal ./internal/agentctl/server/process ./internal/agentctl/server/api
go test -trimpath -race ./internal/persistence/storeconformance
go run ./cmd/sqlguard ./internal
cd ../..
bash scripts/lint-go-changed $(git diff --name-only | grep '\.go$')
```

## E2E 测试图

### Scenario A: provably unjournaled retry

```text
User -> session.recover(retry_connection)
  -> RetrySessionDelivery
  -> InspectDeliveryRecordEvidence(AbsentSubmissionID)
       retained journal: descriptor durable/untruncated + SubmissionAbsent=true
  -> SettlePreIntakeAgentDeliverySubmission (single tx)
       submission: interrupted_unknown/prompt_dispatch_failed -> failed
       effect:     agent_delivery.pre_intake_settlement:<id> completed
       block:      unknown_prompt_outcome open -> resolved
       session:    recovery revision advanced
  -> restorePendingQueueDispatchesForRecovery (best effort)
  -> response {outcome: settled}
```

### Scenario B: pre-write rejection

```text
promptTask -> resolveDeliveryProtocol (durable v1)
  -> beforeAdmission: prepareBackendDeliverySubmission (SQL dispatching)
  -> agentctl AdmitDeliverySubmission -> ErrDeliveryAdmissionRejected
  -> client returns ErrDurableDeliveryAdmissionRejected
  -> finishPromptExecutorDispatch: settle dispatching -> failed + effect
  -> no recovery block; send-now/queue claim restored by the existing failure path
```

### Scenario C: admission blocked before submission

```text
promptTask -> resolveDeliveryProtocol
  -> DurableDeliveryCapability.AdmissionBlocked=true
  -> DurableDeliveryBlocked{delivery_admission_blocked}
  -> typed transient error; no SQL submission, no recovery block
  -> queue/send-now claim restored by the existing rollback path
```

### Scenario D: adoption after settlement

```text
validateNativeResumeSQLRows / activeBackendSubmissions
  -> failed submission is terminal and excluded from active rows
  -> remaining active rows still match the journal
  -> adoption proceeds
```

### Scenario E: agentctl health recovery

```text
oversized event -> ErrEventTooLarge -> ERROR log + metric -> pump continues
storage exhaustion -> deliveryBlocked=true -> capability AdmissionBlocked=true
capacity recovered + pump alive -> storage_error cleared -> AdmissionBlocked=false
```

| Test object | Observation point | Pass condition |
| --- | --- | --- |
| SQL settlement | `agent_delivery_submissions`, `agent_delivery_effects`, `session_recovery_blocks` | one transaction commits all three or none |
| Retry path | `SessionDeliveryRecoveryResponse` + DB rows | `settled`, block resolved, no prompt dispatch |
| Immediate rejection | DB rows + queue claim | submission `failed`, no block, claim restored |
| Admission health | `DeliveryCapability()` / descriptor | blocked is visible, then cleared after recovery |
| Absence evidence | `DeliveryRecordEvidence.SubmissionAbsent` | true only for a missing exact ID on an authenticated journal |

## 实现清单

### Task 1: Retained journal absence evidence

**Files:**
- Modify: `apps/backend/internal/agentctl/journal/retained.go`
- Modify: `apps/backend/internal/agent/runtime/lifecycle/delivery_record_evidence.go`
- Modify: `apps/backend/internal/agent/runtime/lifecycle/delivery_record_transport.go`
- Modify: `apps/backend/internal/agent/runtime/agentctl/client_delivery.go`
- Create: `apps/backend/internal/agentctl/journal/retained_absence_test.go`
- Create: `apps/backend/internal/agent/runtime/lifecycle/delivery_record_evidence_absence_test.go`
- Test: `apps/backend/internal/agent/runtime/agentctl/client_delivery_test.go` (extend existing)

**Interfaces:**
- Consumes: existing `journal.GetSubmission`, `agentctl.DeliveryHTTPError`, `sameDeliveryRecordDescriptor`.
- Produces:
  - `journal.RetainedReconstructionEvidenceRequest{ AbsentSubmissionID string }`
  - `journal.RetainedReconstructionEvidence{ SubmissionAbsent bool }`
  - `lifecycle.DeliveryRecordEvidenceRequest{ AbsentSubmissionID string }`
  - `lifecycle.DeliveryRecordEvidence{ SubmissionAbsent bool }`
  - `agentctl.IsDeliverySubmissionNotFound(err error) bool`

- [x] **Step 1: Write the failing journal test**

Test: `TestReadRetainedReconstructionEvidenceReportsMissingSubmission` and `...ReportsPresentSubmission`. Open a real journal in `t.TempDir()`, put one submission, then call `ReadRetainedReconstructionEvidence` with `AbsentSubmissionID` set to a missing ID (expect `SubmissionAbsent=true`) and to the stored ID (expect `false`). Add an owner-mismatch case expecting an error.

- [x] **Step 2: Run to verify failure**

Run: `cd apps/backend && go test -trimpath ./internal/agentctl/journal -run 'TestReadRetainedReconstructionEvidenceReports' -v`
Expected: FAIL (unknown field `AbsentSubmissionID`).

- [x] **Step 3: Implement the journal fields and lookup**

Add `AbsentSubmissionID` to the request and `SubmissionAbsent` to the result. In `readStoredReconstructionEvidence`, after the existing stored-record branch, call `GetSubmission`; map `ErrSubmissionNotFound` to `SubmissionAbsent=true`; return other errors unchanged; leave `false` when found. Do not set both `StoredSubmissionID` and `AbsentSubmissionID` in one request.

- [x] **Step 4: Run journal tests**

Run: `cd apps/backend && go test -trimpath ./internal/agentctl/journal -run 'TestReadRetainedReconstructionEvidenceReports' -v`
Expected: PASS.

- [x] **Step 5: Write the failing lifecycle transport test**

Test: `TestInspectDeliveryRecordEvidenceRetainedAbsenceIsStable` with a fake control client that returns a descriptor and `SubmissionAbsent`; assert a changed second descriptor yields `ErrDeliveryRecordEvidenceStale`. Test `TestInspectDeliveryRecordEvidenceLiveAbsenceMaps404` with a fake HTTP server returning 404 `{"code":"SUBMISSION_NOT_FOUND"}`; assert `SubmissionAbsent=true`. Add `TestIsDeliverySubmissionNotFound` for the helper.

- [x] **Step 6: Run to verify failure**

Run: `cd apps/backend && go test -trimpath ./internal/agent/runtime/lifecycle ./internal/agent/runtime/agentctl -run 'Absence|DeliverySubmissionNotFound' -v`
Expected: FAIL (unknown fields / missing helper).

- [x] **Step 7: Implement lifecycle and client mapping**

Add the request/result fields. In `inspectRetainedDeliveryRecord`, pass `AbsentSubmissionID`; when set, call the retained evidence read twice around the absence lookup and require `sameDeliveryRecordDescriptor` and identical `SubmissionAbsent`; otherwise return `ErrDeliveryRecordEvidenceStale`. In `readLiveStoredEvidence`, call `client.GetDeliverySubmission` for `AbsentSubmissionID`; map `IsDeliverySubmissionNotFound` to `true`; nil result means present; other errors use `deliveryEvidenceReadError`. Extend the existing post-read descriptor comparison condition to include `AbsentSubmissionID`. Add `IsDeliverySubmissionNotFound` checking `*DeliveryHTTPError` with 404 and `SUBMISSION_NOT_FOUND`.

- [x] **Step 8: Run lifecycle/client tests**

Run: `cd apps/backend && go test -trimpath ./internal/agent/runtime/lifecycle ./internal/agent/runtime/agentctl -run 'Absence|DeliverySubmissionNotFound' -v`
Expected: PASS.

- [x] **Step 9: Update the existing reconstruction test fake**

Update `apps/backend/internal/orchestrator/session_delivery_reconstruction_test.go` so `retainedJournalRecoveryManager.InspectDeliveryRecordEvidence` forwards `AbsentSubmissionID` and copies `SubmissionAbsent`. Run `go test -trimpath ./internal/orchestrator -run TestRetrySessionDeliveryReconstructsMissingSubmission -v`; expected PASS (compile + no regression).

- [x] **Step 10: Commit**

```bash
git add apps/backend/internal/agentctl/journal apps/backend/internal/agent/runtime/lifecycle apps/backend/internal/agent/runtime/agentctl apps/backend/internal/orchestrator/session_delivery_reconstruction_test.go
git commit -m "feat(delivery): add retained journal submission absence evidence"
```

---

### Task 2: Pre-intake settlement transaction

**Files:**
- Modify: `apps/backend/internal/task/models/agent_delivery.go`
- Create: `apps/backend/internal/task/models/agent_delivery_pre_intake.go`
- Modify: `apps/backend/internal/task/repository/repoerrors/agent_delivery.go`
- Modify: `apps/backend/internal/task/repository/interface.go`
- Create: `apps/backend/internal/task/repository/sqlite/agent_delivery_pre_intake_settlement.go`
- Create: `apps/backend/internal/task/repository/sqlite/agent_delivery_pre_intake_settlement_test.go`

**Interfaces:**
- Consumes: `lockSessionTurnWrites`, `lockAgentDeliveryWrites`, `advanceRecoverySessionRevisionTx`, `insertDeliveryEffectTx`.
- Produces:
  - `models.DeliveryPreIntakeEffectType`, `models.DeliveryPreIntakeEffectKey(submissionID string) string`
  - `models.AgentDeliveryPreIntakeSettlementRequest`
  - `models.AgentDeliveryPreIntakeSettlementResult`
  - `repository.AgentDeliveryPreIntakeSettlementRepository`
  - `repoerrors.ErrAgentDeliveryPreIntakeSettlementConflict`, `...Stale`

- [x] **Step 1: Write the failing repository test**

Test: `TestSettlePreIntakeAgentDeliverySubmissionSettlesAndResolvesBlock` with a real SQLite repo: seed session, incarnation/generation, a `dispatching` submission (terminal_sequence 0), and an open `agent_delivery` block with empty `delivery_stream_id`. Call the new method with `ExpectedState=dispatching`, `ExpectedOutcome=dispatching`. Assert submission `failed`/`failed`, effect row `agent_delivery.pre_intake_settlement:<id>` completed with empty stream and sequence 0, block resolved with `durable_delivery_not_delivered`, and session `updated_at` advanced.

- [x] **Step 2: Run to verify failure**

Run: `cd apps/backend && go test -trimpath ./internal/task/repository/sqlite -run TestSettlePreIntakeAgentDeliverySubmission -v`
Expected: FAIL (method/model missing).

- [x] **Step 3: Add models and repository interface**

Add the effect type/key helper, request/result structs, the two repoerrors, and the interface. Request fields: `SessionID`, `IncarnationID`, `HarnessGeneration`, `SubmissionID`, `ExpectedState`, `ExpectedOutcome`, `BlockID`, `SettledAt`. Result fields: `Settled`, `AlreadySettled`, `Submission`, `ResolvedBlockIDs`.

- [x] **Step 4: Implement the transaction**

In `agent_delivery_pre_intake_settlement.go`: validate the request; begin tx; lock session turn writes and delivery writes; load the submission row. Verify owner, `ExpectedState`, `ExpectedOutcome` (when non-empty), `terminal_sequence=0`, `fully_settled_at IS NULL`. If already `failed` with the pre-intake effect present, resolve any still-open matching blocks and return `AlreadySettled=true`. Otherwise conditionally update the submission to `failed`/`failed`; require one row. Insert the effect with `insertDeliveryEffectTx` (`stream_id=''`, `sequence=0`, `state=completed`, `completed_at=settledAt`, `outcome=failed`, `turn_id=''`). Select open matching blocks (`consumer_reference='agent_delivery'`, exact submission, empty stream, expected generation, optional `BlockID`), advance the session revision, and resolve them with `delivery_outcome='failed'`, `authorized_action='durable_delivery_not_delivered'`. Commit.

- [x] **Step 5: Run the settlement tests**

Add and run: `TestSettlePreIntakeAgentDeliverySubmissionIsIdempotent`, `...RejectsStateConflict`, `...LeavesUnrelatedBlocksOpen`.
Run: `cd apps/backend && go test -trimpath ./internal/task/repository/sqlite -run TestSettlePreIntakeAgentDeliverySubmission -v`
Expected: PASS.

- [x] **Step 6: Add the PostgreSQL gated test**

Add `TestSettlePreIntakeAgentDeliverySubmissionPostgres` guarded by `KANDEV_TEST_POSTGRES_DSN`, using the existing repository PostgreSQL fixture. Assert the same rows and lock behavior. Document the skip when the DSN is unset.

- [x] **Step 7: Run persistence checks**

Run: `cd apps/backend && go test -trimpath ./internal/task/repository/sqlite -run TestSettlePreIntakeAgentDeliverySubmission -v && go run ./cmd/sqlguard ./internal && go test -trimpath ./internal/persistence/storeconformance`
Expected: PASS (PostgreSQL test skips without the DSN).

- [x] **Step 8: Commit**

```bash
git add apps/backend/internal/task/models apps/backend/internal/task/repository
git commit -m "feat(delivery): settle pre-intake submissions transactionally"
```

---

### Task 3: Retry recovery branch

**Files:**
- Modify: `apps/backend/internal/orchestrator/session_delivery_reconstruction.go`
- Create: `apps/backend/internal/orchestrator/pre_intake_delivery_settlement.go`
- Create: `apps/backend/internal/orchestrator/session_delivery_pre_intake_test.go`

**Interfaces:**
- Consumes: Task 1 evidence (`SubmissionAbsent`), Task 2 repository interface, `restorePendingQueueDispatchesForRecovery`, `publishAgentDeliveryRecoveryState`.
- Produces:
  - `func (s *Service) settlePreIntakeDelivery(ctx context.Context, session *models.TaskSession, submissionID string, expectedState models.DeliverySubmissionState, expectedOutcome, blockID string, releaseQueue bool) (*models.AgentDeliveryPreIntakeSettlementResult, error)`
  - `func (s *Service) commitPreIntakeDeliverySettlement(ctx context.Context, session *models.TaskSession, incarnationID string, generation int64, block *models.SessionRecoveryBlock, evidence *agentruntime.DeliveryRecordEvidence) (*SessionDeliveryRecoveryResponse, error)`

- [x] **Step 1: Write the failing retry test**

Test: `TestRetrySessionDeliverySettlesProvablyUnjournaledSubmission`. Use `newServiceWithRealRepo`, seed task/session/generation, insert an `interrupted_unknown`/`prompt_dispatch_failed` submission with `terminal_sequence=0`, insert an open `unknown_prompt_outcome` block with empty stream, create a real bbolt journal with only completed submissions, and use `retainedJournalRecoveryManager`. Assert the response is `settled`; submission `failed`; block resolved; no prompt dispatch; queue release invoked (spy on the message queue or assert queued rows remain and status event published).

- [x] **Step 2: Run to verify failure**

Run: `cd apps/backend && go test -trimpath ./internal/orchestrator -run TestRetrySessionDeliverySettlesProvablyUnjournaledSubmission -v`
Expected: FAIL (blocked `retained_evidence_ambiguous`).

- [x] **Step 3: Implement the retry branch**

Refactor `readReconstructionSelection` so the caller loads `InspectDeliveryRecordEvidence` once with `AbsentSubmissionID=block.DeliverySubmissionID` (skip the absence branch when the block has no submission ID). If `SubmissionAbsent` and the conservative conditions hold (`block.Reason=="unknown_prompt_outcome"`, `SubmissionCount==0`, `!SubmissionsTruncated`, empty block stream, exact owner/generation), call `commitPreIntakeDeliverySettlement`; otherwise continue the existing selection path. `commitPreIntakeDeliverySettlement` loads the SQL submission, verifies state/outcome/terminal_sequence, calls the Task 2 transaction with `expectedState=interrupted_unknown`, `expectedOutcome=prompt_dispatch_failed`, `blockID=block.ID`, and returns `settled`. Map conflict to `delivery_identity_mismatch`, stale to `stale_recovery`, unavailable to `recovery_state_unavailable`.

- [x] **Step 4: Run the happy-path test**

Run: `cd apps/backend && go test -trimpath ./internal/orchestrator -run TestRetrySessionDeliverySettlesProvablyUnjournaledSubmission -v`
Expected: PASS.

- [x] **Step 5: Add conservative-evidence tests**

Add `TestRetrySessionDeliveryKeepsBlockWhenJournalHasAnotherCandidate`, `...WhenJournalTruncated`, `...WhenBlockStreamIsNotEmpty`, `...WhenSubmissionOutcomeDiffers`, `...PreIntakeSettlementIsIdempotent` (two retries replay the same open-block snapshot), and `...KeepsArchivedTaskBlocked`. Cover Review Focus 1-3. Assert each returns `blocked` with the expected reason and leaves the block open; the archived case must be refused by `ensureTaskNotArchived` before any evidence read.

- [x] **Step 6: Run the new tests**

Run: `cd apps/backend && go test -trimpath ./internal/orchestrator -run 'TestRetrySessionDelivery(.*PreIntake|KeepsBlock|SettlesProvably)' -v`
Expected: PASS.

- [x] **Step 7: Commit**

```bash
git add apps/backend/internal/orchestrator
git commit -m "fix(orchestrator): recover provably unjournaled delivery submissions"
```

---

### Task 4: Typed admission rejection and immediate settlement

**Files:**
- Modify: `apps/backend/internal/agentctl/server/process/submission_delivery.go`
- Modify: `apps/backend/internal/agentctl/server/process/manager.go`
- Modify: `apps/backend/internal/agentctl/server/api/agent.go`
- Modify: `apps/backend/internal/agent/runtime/agentctl/agent.go`
- Modify: `apps/backend/internal/agent/runtime/durable_delivery.go`
- Modify: `apps/backend/internal/orchestrator/task_operations.go`
- Modify: `apps/backend/internal/orchestrator/pre_intake_delivery_settlement.go`
- Create/Modify tests:
  - `apps/backend/internal/agentctl/server/process/submission_delivery_test.go`
  - `apps/backend/internal/agentctl/server/api/agent_admission_test.go`
  - `apps/backend/internal/agent/runtime/agentctl/agent_test.go`
  - `apps/backend/internal/orchestrator/pre_intake_immediate_test.go`

**Interfaces:**
- Produces:
  - `process.ErrDeliveryAdmissionRejected` and `process.ErrDeliveryAdmissionUncertain`
  - `agentctl.ErrDurableDeliveryAdmissionRejected` and its `agentruntime` alias
  - `ws.ErrorPayload.Details{kind, reason}` for admission failures
  - `func (s *Service) settleRejectedPreIntakeDelivery(ctx context.Context, taskID, sessionID string, delivery *agentDeliverySubmissionRuntime) (bool, error)`

- [x] **Step 1: Write failing process tests**

Tests: `TestSubmissionDeliveryAdmitDistinguishesPreWriteRejection` (fake journal returns `ErrJournalFull` from `PutSubmission`; expect `errors.Is(err, ErrDeliveryAdmissionRejected)`) and `TestSubmissionDeliveryAdmitDistinguishesPostWriteUncertainty` (prepared record then transition failure; expect `ErrDeliveryAdmissionUncertain`). Add a pressure-block case in `manager_test.go` if not covered.

- [x] **Step 2: Run to verify failure**

Run: `cd apps/backend && go test -trimpath ./internal/agentctl/server/process -run 'AdmitDistinguishes' -v`
Expected: FAIL (sentinels missing).

- [x] **Step 3: Implement sentinels and API typing**

Wrap `PutSubmission` failures and pressure/blocked failures in `ErrDeliveryAdmissionRejected`; wrap `TransitionSubmission` failures in `ErrDeliveryAdmissionUncertain`. In `admitDurablePrompt`, return `Details{"kind":"durable_delivery_admission_rejected"|"durable_delivery_admission_uncertain","reason":...}` and record the bounded metric. Add the same kind to `durableSubmissionResponse`'s uncertain case.

- [x] **Step 4: Write failing client/orchestrator tests**

Tests: `TestPromptClientMapsRejectedKind` (WS error with the rejected kind returns `errors.Is(err, agentruntime.ErrDurableDeliveryAdmissionRejected)`); `TestPromptDispatchFailureSettlesRejectedSubmissionImmediately` (fake agent manager returns a `%w`-wrapped sentinel; assert submission `failed`, no block, no `markInterrupted`); `TestPromptDispatchFailureKeepsUncertainWhenSettlementFails` (repository error; assert fallback to `interrupted_unknown` + block).

- [x] **Step 5: Run to verify failure**

Run: `cd apps/backend && go test -trimpath ./internal/agent/runtime/agentctl ./internal/orchestrator -run 'MapsRejectedKind|SettlesRejectedSubmission|KeepsUncertainWhenSettlementFails' -v`
Expected: FAIL.

- [x] **Step 6: Implement client and orchestrator branch**

Parse `errPayload.Details["kind"]` in `Client.prompt`; return `fmt.Errorf("%w: %s", ErrDurableDeliveryAdmissionRejected, reason)` for the rejected kind. Re-export through `agent/runtime`. In `finishPromptExecutorDispatch`, before `markInterrupted`, when `delivery != nil && !dispatchAccepted && errors.Is(execErr, agentruntime.ErrDurableDeliveryAdmissionRejected)`, call `settleRejectedPreIntakeDelivery` with `context.WithoutCancel(ctx)`; on success skip `markInterrupted`/`deliveryRecoveryError`; on failure fall back to the existing path and join the error. The immediate settlement uses `expectedState=dispatching`, `expectedOutcome=dispatching`, `blockID=""`, `releaseQueue=false`.

- [x] **Step 7: Run the task suite**

Run: `cd apps/backend && go test -trimpath ./internal/agentctl/server/process ./internal/agentctl/server/api ./internal/agent/runtime/agentctl ./internal/orchestrator -run 'Admit|MapsRejectedKind|SettlesRejectedSubmission|KeepsUncertain' -v`
Expected: PASS.

- [x] **Step 8: Commit**

```bash
git add apps/backend/internal/agentctl/server apps/backend/internal/agent/runtime apps/backend/internal/orchestrator
git commit -m "fix(agentctl): distinguish pre-write admission rejection"
```

---

### Task 5: Admission health visibility and recoverable blocked state

**Files:**
- Modify: `apps/backend/internal/agentctl/journal/location.go`
- Modify: `apps/backend/internal/agentctl/journal/journal.go` (`prepareAppendEvents`)
- Modify: `apps/backend/internal/agentctl/journal/metrics.go`
- Modify: `apps/backend/internal/agentctl/server/process/manager.go`
- Modify: `apps/backend/internal/agentctl/server/process/delivery_pressure.go`
- Modify: `apps/backend/internal/agentctl/server/process/delivery_batch.go`
- Modify: `apps/backend/internal/agent/runtime/lifecycle/durable_delivery_activation.go`
- Modify: `apps/backend/internal/agent/runtime/lifecycle/manager_interaction.go`
- Modify: `apps/backend/internal/agent/runtime/agentctl/client_delivery.go`
- Modify: `apps/backend/internal/orchestrator/agent_delivery_submission.go` (`deliveryCapabilityForExecution` and `resolveDeliveryProtocol` helpers)
- Create/Modify tests:
  - `apps/backend/internal/agentctl/journal/event_too_large_test.go`
  - `apps/backend/internal/agentctl/server/process/delivery_pressure_test.go`
  - `apps/backend/internal/agentctl/server/api/durable_delivery_test.go`
  - `apps/backend/internal/orchestrator/agent_delivery_blocked_capability_test.go`

**Interfaces:**
- Produces:
  - `journal.StorageCapability{ AdmissionBlocked bool }`
  - `lifecycle.DurableDeliveryCapability{ AdmissionBlocked bool }`
  - `journal.ErrEventTooLarge`
  - `journal.RecordAdmissionRejected(reason string)`, `journal.RecordEventRejected(reason string)`
  - orchestrator `deliveryAdmissionBlockedReason = "delivery_admission_blocked"` and typed `errDeliveryAdmissionBlocked`

- [x] **Step 1: Write failing journal tests**

Test: `TestAppendBatchOversizedEventReturnsEventTooLarge` (payload > `MaxEventBytes`; assert `errors.Is(err, journal.ErrStreamFull)` and `errors.Is(err, journal.ErrEventTooLarge)`); `TestAdmissionRejectedMetricUsesBoundedLabels`.

- [x] **Step 2: Run to verify failure**

Run: `cd apps/backend && go test -trimpath ./internal/agentctl/journal -run 'EventTooLarge|AdmissionRejectedMetric' -v`
Expected: FAIL.

- [x] **Step 3: Implement journal classification and metrics**

Add `ErrEventTooLarge`; in `prepareAppendEvents` wrap both `ErrStreamFull` and `ErrEventTooLarge`; in `deliveryEventWriter.persist` wrap the queue-size `ErrJournalFull` with `ErrEventTooLarge`. Add `RecordAdmissionRejected` and `RecordEventRejected` with closed label sets added to `boundedDeliveryLabels`.

- [x] **Step 4: Write failing process tests**

Tests: `TestDeliveryCapabilityReportsAdmissionBlocked` (after `recordDeliveryStorageFailure`, `AdmissionBlocked=true`, `Durable=true`); `TestDeliveryCapabilityClearsAfterCapacityRecovery` (recovered capacity + pump alive clears); `TestForwardUpdatesSkipsOversizedEventWithoutBlockingAdmission` (feed an oversized event, then a normal event; assert the pump survives, the normal event is forwarded, and `AdmissionBlocked=false`); `TestForwardUpdatesMarksPumpStoppedOnStorageFailure`.

- [x] **Step 5: Run to verify failure**

Run: `cd apps/backend && go test -trimpath ./internal/agentctl/server/process -run 'DeliveryCapability|ForwardUpdatesSkips|MarksPumpStopped' -v`
Expected: FAIL.

- [x] **Step 6: Implement manager health and oversized skip**

Add `deliveryPumpStopped` guarded by `deliveryActiveMu`; set it true when `forwardUpdates` exits due to an error and false when it starts. In `forwardUpdates`, on `journal.ErrEventTooLarge` log ERROR + `RecordEventRejected`, then `continue`; other errors keep the current block-and-return path. `DeliveryCapability()` reads the blocked/pump flags and sets `AdmissionBlocked`/reason while keeping `Durable=true` for an open journal. `applyDeliveryCapacityLocked` clears `storage_error` when `capacity.Recovered && !deliveryPumpStopped && !CancellationPending`.

- [x] **Step 7: Propagate through lifecycle/client**

Add `AdmissionBlocked` to `lifecycle.DurableDeliveryCapability` and `agentctl.DurableDeliveryInfo`; copy it in `DurableDeliveryCapabilityForExecution` and `Client.DurableDeliveryCapability`. In `orchestrator.deliveryCapabilityForExecution`, return `DurableDeliveryBlocked{Reason:"delivery_admission_blocked"}` when set. In `resolvePendingDeliveryProtocol`/`validateV1DeliveryProtocol`, return `fmt.Errorf("%w: %s", errDeliveryAdmissionBlocked, reason)` for that reason instead of `deliveryRecoveryError`.

- [x] **Step 8: Write the failing orchestrator capability test**

Test: `TestResolveDeliveryProtocolBlockedAdmissionCreatesNoSubmissionOrBlock` with a fake capability reader returning `AdmissionBlocked=true`; assert the prompt fails with the typed error, no `agent_delivery_submissions` row, and no open recovery block.

- [x] **Step 9: Run the full task suite**

Run: `cd apps/backend && go test -trimpath -race ./internal/agentctl/journal ./internal/agentctl/server/process ./internal/agentctl/server/api ./internal/agent/runtime/lifecycle ./internal/agent/runtime/agentctl ./internal/orchestrator -run 'EventTooLarge|DeliveryCapability|ForwardUpdates|BlockedAdmission' -v`
Expected: PASS.

- [x] **Step 10: Commit**

```bash
git add apps/backend/internal/agentctl apps/backend/internal/agent/runtime apps/backend/internal/orchestrator
git commit -m "fix(agentctl): expose and recover durable admission health"
```

---

### Task 6: Specs, ADR, and observability documentation

**Files:**
- Modify: `docs/specs/platform/requirements/durable-agent-delivery.md`
- Modify: `docs/specs/platform/system-design/durable-agent-delivery.md`
- Modify: `docs/specs/platform/system-design/durable-agent-record-recovery.md`
- Create: `docs/decisions/2026-11-03-pre-intake-delivery-settlement.md`
- Modify: `AGENTS.md` (durable delivery metrics line)

**Interfaces:** none (documentation only).

- [x] **Step 1: Update requirements**

Add acceptance criteria under REQ-006 and REQ-007:
- `AC-PLATFORM-DURABLE-AGENT-DELIVERY-006.25`: a pre-journal rejection settles `failed` and creates no `unknown_prompt_outcome` block.
- `AC-PLATFORM-DURABLE-AGENT-DELIVERY-006.26`: retry settles a submission only when the authenticated retained journal proves exact absence, no other unresolved candidate, and exact block/owner binding; otherwise it stays blocked.
- `AC-PLATFORM-DURABLE-AGENT-DELIVERY-007.7`: admission health is reflected in capability/status and blocks the prompt before any durable submission exists.
Also add a note to REQ-001.4 / REQ-006 that an unjournalable oversized event must not permanently block admission.

- [x] **Step 2: Update system designs**

In `durable-agent-delivery.md`'s Reconciliation/Discovery sections, document the pre-intake branch, the evidence matrix, the absence proof, and the `AdmissionBlocked` capability. In `durable-agent-record-recovery.md`'s Candidate and evidence rules, add the explicit-absence row and the conservative rejection conditions.

- [x] **Step 3: Write the ADR**

Create `docs/decisions/2026-11-03-pre-intake-delivery-settlement.md` with: context (the two stuck sessions), decision (absence evidence + failed settlement + no resend + admission health), consequences, and rejected alternatives (reuse terminal effect with sequence 0; treat journal absence alone as proof; downgrade to legacy; auto-resend).

- [x] **Step 4: Update observability docs**

Append `agent_delivery_admission_rejected_total` and `agent_delivery_event_rejected_total` with their closed label sets to the root `AGENTS.md` durable-delivery metrics bullet.

- [x] **Step 5: Run spec/document checks**

```bash
uv run --no-project --python 3.12 python scripts/lint-spec-files.py --all
uv run --no-project --python 3.12 python scripts/list-docs.py validate
openspec validate settle-pre-intake-delivery-failure --strict --no-interactive
```

Expected: all pass.

- [x] **Step 6: Commit**

```bash
git add docs/specs docs/decisions AGENTS.md
git commit -m "docs(delivery): record pre-intake settlement contract"
```

---

### Task 7: Full verification and live-session smoke

**Files:** none (verification only).

- [x] **Step 1: Run the focused backend suites**

```bash
cd apps/backend
go test -trimpath -race ./internal/task/repository/sqlite ./internal/orchestrator \
  ./internal/agent/runtime/lifecycle ./internal/agent/runtime/agentctl \
  ./internal/agentctl/journal ./internal/agentctl/server/process ./internal/agentctl/server/api
go test -trimpath -race ./internal/persistence/storeconformance
go run ./cmd/sqlguard ./internal
```

Expected: PASS.

- [x] **Step 2: Run lint on changed Go files**

```bash
cd /media/zuo/AigoData/kandev-home/tasks/fix-durable-delivery_f1xplj7p/kandev
bash scripts/lint-go-changed $(git diff --name-only | grep '\.go$')
```

Expected: PASS.

- [ ] **Step 3: Post-deploy smoke on the affected session (operational, not a code edit)**

With the fixed backend running against the real Kandev home, open task `1799e739-8599-40aa-814e-0c03027a8005` / session `062a1281-4012-41f0-a6bc-f503aa4999b4`, click **Retry connection**, and confirm:
- the response is `settled`;
- the recovery banner disappears;
- the queued user message remains in the queue and can be sent;
- no prompt is dispatched by the recovery itself.

Do not hand-edit SQLite. If the retry still returns blocked, capture the response reason and the backend log line instead.

- [x] **Step 4: Record evidence**

Record the commands, exit codes, and smoke result in `verify.md` during the Verify step.

## 验证清单

- [ ] Pre-intake retry settles the exact live-state fixture: submission `failed`, effect completed, block resolved, queue released, no prompt dispatch.
- [ ] Retry remains blocked for journal lost/unreachable, truncated descriptor, another unresolved candidate, non-empty block stream, or owner/generation mismatch.
- [ ] Repeated retry is idempotent and does not duplicate the effect, block resolution, or queue release.
- [ ] Adoption (`validateNativeResumeSQLRows` / `activeBackendSubmissions`) ignores the settled `failed` submission and proceeds when the remaining active rows match.
- [ ] Write-before rejection settles immediately with no `unknown_prompt_outcome` block; post-write uncertainty still creates the existing block.
- [ ] `DeliveryCapability().AdmissionBlocked` is visible during storage failure and clears after capacity recovery when the pump is alive.
- [ ] Oversized events are skipped with an ERROR log and bounded metric; the pump continues and admission is not blocked.
- [ ] Genuine storage exhaustion keeps admission blocked and leaves Stop/status/replay/ACK available.
- [ ] No recovery block is created when the prompt is refused before a SQL submission exists.
- [ ] `cd apps/backend && go test -trimpath -race ...` focused suites pass; `scripts/lint-go-changed` passes.
- [ ] `scripts/lint-spec-files.py --all`, `scripts/list-docs.py validate`, and `openspec validate ... --strict` pass.
- [ ] Post-deploy retry on session 062a1281 returns `settled` without any SQLite hand edit.
