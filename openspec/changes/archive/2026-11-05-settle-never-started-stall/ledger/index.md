# Apply Ledger: settle-never-started-stall

> Maintained by `openspec-apply-change`. Executor: `subagent-driven` (8 implementation tasks, ≥5; tasks 1, 2, 4 are independent roots, so the work is not a single linear chain).

## Executor / Process Rulings

- Executor: `subagent-driven` — one fresh implementer per top-level task, sequential in this worktree, full report written to `.superpowers/sdd/tasks/task-<N>-report.md`.
- Ruling: per-task spec/quality dual-review subagents are not spawned by default (user instruction). The controller inspects each task diff and test evidence; the final whole-branch review is the code-review gate. Cost if wrong: a defect a per-task reviewer would have caught earlier is found at final review instead.

## Preflight Scan

| Shared file / interface | Task A produces | Task B consumes | Finding |
|---|---|---|---|
| `lifecycle` stall payload fields | Task 1: `DeliverySubmissionID`, `DeliveryStreamID`, `DeliveryIncarnationID`, `DeliveryHarnessGeneration`, `OriginalRuntime` | Task 5: reads the same five fields from `AgentStalledPayload` | Names match design.md D1; no conflict |
| journal retirement request | Task 2: `RetirementRequest.NeverStartedSubmissionID` + `RetireSubmissionWithEvidence` | Task 3: passes `NeverStartedSubmissionID` through the lifecycle wrapper | Task 3's file list does not include the journal request; it consumes it from Task 2. Consistent |
| lifecycle retirement method | Task 3: `Manager.RetireNeverStartedDeliverySubmission` | Task 5: type-asserts that method; Task 8: fake implements it | Consistent |
| SQL settlement repository | Task 4: `SettleNeverStartedAgentDeliverySubmission` + models | Task 5.7: calls with observed state/outcome, retry once on conflict | Names and semantics match design.md D4 |
| new orchestrator file | Task 5 creates `never_started_stall_settlement.go` | Task 6 modifies the same file (metrics + auto-resume) | Ordered dependency, no conflict |
| stall handler regression | Task 5 changes never-started branch | Task 8.4 asserts advisory branch unchanged | Explicit regression task; no conflict |
| effect type | Task 4: `agent_delivery.never_started_settlement` | Task 7 docs; Task 8 asserts effect key | Same constant referenced; no conflict |
| per-task self-consistency | Tasks 1–8 | own Files vs Interfaces vs exit criteria | Each task's file list covers the interfaces it produces; no internal contradiction found |

No plan-level conflicts found. Task 7 (docs) depends on Tasks 1–6 behavior; Task 8 depends on Tasks 1–6. Both are scheduled last.

## Per-Task Ledger

| task id | executor | 验证方法 | 结果 | 状态 | 时间戳 |
|---------|----------|----------|------|------|--------|
| 1 | subagent-driven | `go test ./internal/agent/runtime/lifecycle -run TestPublishAgentStalledCarriesDeliveryIdentity` (RED→GREEN) + full lifecycle package | 5 fields populated; nil client/legacy execution zero-value; package `ok` | `done` | `2026-11-04 21:22` |
| 2 | subagent-driven | journal + api packages; 3 brief tests + primitive authorization test + rejection table | Target-bound authorization works; default `dispatching` still rejected; state normalized, payload/hash preserved | `done` | `2026-11-04 21:45` |
| 3 | subagent-driven | lifecycle package; success + 8 blocked branches + live-instance + idempotent replay; `lint-go-changed` | Journal retired only on exact evidence; live instance and every missing/conflicting branch fail closed; replay converges | `done` | `2026-11-04 22:19` |
| 4 | subagent-driven | focused sqlite settlement tests + `go test ./internal/task/repository/sqlite/...` (818s) + `go build ./...`; Postgres gated test compiled/skipped locally | `dispatching` and `interrupted_unknown` sources settle `failed/failed` with one effect; exact-block resolution; replay and terminal-sequence conflict covered | `done` | `2026-11-04 23:23` |
| 5 | subagent-driven | 6 brief tests + fix-round tests (post-stop owner removal, prompt-error-first, blocked-after-stop, successor skip); orchestrator package (only 11 pre-existing env failures); no goleak | Settlement success/fallback/race/scope/idempotency pass; durable ownership fix applied in `fcf1da202` | `done` | `2026-11-05 01:21` |
| 6 | subagent-driven | focused NeverStarted/ProviderStall tests (race-clean) + full orchestrator package (8 known env failures verified on baseline) | One bounded launch with skip matrix; settlement and auto-resume counters use closed labels; no identifiers | `done` | `2026-11-05 02:02` |
| 7 | subagent-driven | `scripts/list-docs.py validate` (399 decisions, 1686 specs) + `scripts/lint-spec-files.py --all`; all pre-commit hooks | New ADR, superseded old ADR, agents/platform requirements and designs, root observability entry; no claims beyond implemented behavior | `done` | `2026-11-05 02:42` |
| 8 | subagent-driven | real SQLite + real bbolt journal end-to-end tests (`-count=5`, `-race -count=3`); journal/lifecycle/sqlite/orchestrator suites; `lint-go-changed`; `openspec validate --strict` | Success path settles both stores and recovers; blocked path writes nothing and fails closed; advisory unchanged; only the 8 known pre-existing env failures remain | `done` | `2026-11-05 03:45` |

### Final Whole-Branch Review

Verdict: **fix-before-merge** (1 Important blocking, 4 Minor). Controller verified the reported pre-existing failures also fail on a detached baseline worktree at `d9a1c0647` (same external mount): `TestCompletedRelocation*`, `TestSessionOpen*`, and the `internal/orchestrator/executor` workspace-inventory/checkout-proof tests.

- Important: `promptErrorDefersToSettledDelivery` defers on any terminal submission, so the pre-intake admission-rejection path (which settles `failed`) loses its session/task revert and can leave the session RUNNING. Narrow to `ErrUncertainPromptDelivery` (or the never-started effect).
- Minor: journal retirement commits before validating the observed SQL source state; a conflicting-but-identifiable row gets a journal write before the SQL conflict.
- Minor: recovered/fallback outcome writes share the 50s stop+settle budget and can be starved after a committed settlement.
- Minor: the publication-error branch still creates an `unknown_prompt_outcome` block without the terminal guard.
- Minor (required by reviewer): eligibility-path identity rejections record no `identity_missing` metric; pull the deferred minor out of deferral.

Fix wave: `77b8d58d9` `fix(stall): close final-review findings` (all five findings; targeted tests, full orchestrator root with only the 8 known environment failures, `go build ./...`, `lint-go-changed`). Scoped re-review dispatched.
Scoped re-review: **clean** — Findings 1-5 all ADDRESSED with regression tests; no new Critical/Important breakage in the fix diff.

### Rulings

- Task 4 collateral refactor accepted: `agent_delivery_abandoned_settlement.go` gained a behavior-preserving effect-match helper because the pre-commit duplicate-block lint rejected the otherwise-duplicated new settlement code. Covered by the abandoned settlement tests; cost if wrong: an unrelated 21-line diff in the PR.
- Task 4 added `repoerrors.ErrAgentDeliveryNeverStartedSettlementConflict/Stale` outside the brief's file list to match the named error-surface pattern; cost if wrong: extra sentinels in a shared errors file.
- Task 5/6 overlap resolved: Task 5.1 expects one auto-resume call, while Task 6 owns the bounded helper and metrics. Task 5 will implement the minimal one-shot call needed by its success-path test; Task 6 refines it into the 60s-bounded helper with the full skip matrix and bounded metrics.
- Task 5: fix round 1/5 dispatched. Finding: the recovery predicate used the live `OwnsPromptGeneration` check after a force-stop that removes the execution from the lifecycle store, and required the session state to still be RUNNING; production would therefore skip every recovered/fallback write, and the fake manager masked it. Fix: durable session-execution + captured-turn generation identity, allow WAITING_FOR_INPUT from the prompt-error revert, persist the stall error on same-state, and cover both orderings. Cost if wrong: recovery can still be skipped or can overwrite a successor/user action.
- Task 5 collateral accepted: runtime type aliases (`AgentStalledPayload`, never-started retirement contract) through the ARCH-RUNTIME-IMPORT seam, `sessionErrorKey*` constants replacing repeated event-key literals, and a behavior-preserving split of `retryAbandonedJournalDelivery`. All were required by the commit hooks' merge-base lint and covered by existing tests; cost if wrong: a larger diff than the task brief listed.
- Task 6 minor (deferred to final review): a payload whose delivery identity/runtime proof is incomplete is routed to the legacy path by `neverStartedStallSettlementEligible` without recording `provider_stall_settlement_total{outcome=blocked,reason=identity_missing}`. The post-eligibility seam/row failures do record it. Cost if wrong: one design error-table row is under-observed.
- Task 7 rulings: both requirement docs were already at 49/595 bytes below the hard 20480-byte spec-lint cap, so the new ACs forced repo-convention phrasing tightening (one scenario pair merged, behavior preserved). Root `AGENTS.md` (the `CLAUDE.md` symlink target) was already 301/300 lines; two wrapped i18n lines were reflowed with no word changes so the required observability entry could commit. Cost if wrong: the requirement docs have almost no room for the next edit, and the reflow is a formatting-only line outside the observability entry.

## Exit Gate

- [x] **Verification Strategy 入口通过**：`cd apps/backend && go test -trimpath -count=1 -timeout=30m ./internal/agentctl/journal/... ./internal/agent/runtime/lifecycle/... ./internal/task/repository/sqlite/... ./internal/orchestrator/...`
  - changed packages: journal `ok 7.3s`, lifecycle `ok 244.2s`, sqlite `ok 942.9s`.
  - orchestrator root: only the 8 known pre-existing worktree-environment failures (`TestCompletedRelocation*`, `TestSessionOpen*`); `internal/orchestrator/executor`: only pre-existing workspace-inventory/checkout-proof failures. Both reproduce identically on a detached baseline worktree at `d9a1c0647` on the same external mount (verified; temp worktree removed).
  - after the final fix wave, the fixer re-ran the full orchestrator root (only the 8 known failures) and `go build ./...` (ok).
- [x] **`openspec validate settle-never-started-stall --strict --no-interactive` 通过** — `Change 'settle-never-started-stall' is valid` (re-run after the fix wave).
- [x] **验收方法与证据**：
  - 方法：service-level integration (real SQLite repository + real bbolt journal + fake runtime control) plus unit tests across journal, lifecycle evidence, repository transaction, and orchestrator; whole-branch final review + scoped re-review of the fix wave.
  - 场景：success (journal retired + SQL `failed/failed` + effect + block resolved + `WAITING_FOR_INPUT` + REVIEW + notice + one auto-resume), blocked (zero journal/SQL writes, FAILED, open block, no resume), partial-output advisory unchanged, late prompt error after settlement, prompt-error-first ordering, post-stop owner removal, duplicate stall, scope guards, journal zero-event authorization and target binding, SQL replay/terminal-sequence conflict, PostgreSQL gated test compiled (DSN absent locally).
  - 结果：all changed-package tests pass; `go test -race` on the settlement tests is clean; `grep`-verified no duplicate effects/blocks; `bash scripts/lint-go-changed` on all 29 changed Go files → `0 issues`; docs `list-docs.py validate` (399 decisions, 1686 specs) and `lint-spec-files.py --all` pass; final re-review clean.
