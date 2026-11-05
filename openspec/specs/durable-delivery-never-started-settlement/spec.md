# durable-delivery-never-started-settlement Specification

## Purpose
Define the conservative settlement for journal-present submissions with zero turn events and no terminal evidence, retiring the exact journal record and settling the SQL submission as failed once the historical owner is proven dead.
## Requirements
### Requirement: Zero-event never-started settlement SHALL settle the exact submission and retire its retained journal record

When the lifecycle watchdog classifies a prompt generation as never-started (zero turn events since dispatch and the activity epoch is still current) and the execution has been stopped with the historical owner proven terminated, Kandev SHALL settle the exact durable submission as `failed` and retire its retained journal record in one auditable operation.

#### Scenario: 成功结算 dispatching submission
- **WHEN** the SQL submission is `dispatching` or `interrupted_unknown`, its outcome matches the dispatch state, `terminal_sequence=0` and `fully_settled_at IS NULL`; the retained journal contains the exact submission with no `terminal_event_retained` and no `terminal_sequence`; the descriptor is durable and untruncated; the historical owner is proven terminated; and the watchdog attests zero turn events for the current prompt generation
- **THEN** the journal record is marked retired; a `prepared` / `accepted` / `dispatching` state is normalized to `interrupted_unknown` by the existing retirement primitive, while payload and hash are preserved; the SQL submission becomes `failed` / `failed`; the `agent_delivery.never_started_settlement:<submissionID>` effect is written; and the bound delivery block, if any, is resolved in the same transaction

#### Scenario: 首个 prompt 同样适用
- **WHEN** the never-started prompt is an initial session prompt with a durable submission
- **THEN** the same settlement applies using that submission's exact identity

#### Scenario: 迟到 turn 事件使证据失效
- **WHEN** a turn event or another terminal signal arrives after the watchdog snapshot but before settlement
- **THEN** the activity epoch or generation no longer matches, and the settlement MUST NOT run; the existing uncertain path remains authoritative

### Requirement: Settlement evidence MUST be authenticated and conservative

The settlement MUST require exact session, incarnation, harness generation, submission, stream, and payload-hash binding; an authenticated retained journal whose complete unresolved inventory consists only of the exact target; no retained terminal event and no terminal sequence on that target; and proof that the historical owner process terminated. Any missing, truncated, or conflicting evidence MUST keep the submission uncertain and the recovery block open.

#### Scenario: journal 保留 terminal evidence
- **WHEN** the exact journal record has `terminal_event_retained=true` or `terminal_sequence > 0`
- **THEN** no journal retirement and no SQL settlement occur

#### Scenario: 存在其它未解决候选
- **WHEN** the authenticated descriptor contains any unresolved submission other than the exact target
- **THEN** the settlement is blocked and no journal or SQL write occurs

#### Scenario: 身份或哈希不一致
- **WHEN** session, incarnation, generation, stream, submission ID, or payload hash does not match the SQL row and journal record
- **THEN** the settlement is blocked and no journal or SQL write occurs

#### Scenario: owner 未证明终止
- **WHEN** the historical runtime identity is missing, invalid, or `OwnedSessionTerminated` returns false or an error
- **THEN** the settlement is blocked and the existing uncertain recovery path remains

#### Scenario: 描述符截断
- **WHEN** the retained descriptor is truncated or its storage is not durable
- **THEN** the settlement is blocked instead of inferring absence or completion

### Requirement: Settlement MUST NOT fabricate terminal outcomes, re-dispatch, or release unrelated work

The settlement MUST preserve the journal record as retired evidence, MUST NOT delete it, MUST NOT write a terminal event, terminal sequence, terminal turn, or `fully_settled_at`, and MUST NOT dispatch the historical prompt or release unrelated queue entries or recovery blocks.

#### Scenario: journal retirement 保留原证据
- **WHEN** the settlement succeeds
- **THEN** the journal record remains with `retired=true`; a non-terminal dispatch state is normalized to `interrupted_unknown`, while payload and hash are unchanged; `terminal_event_retained=false`, `terminal_sequence=0`; and no terminal effect is created

#### Scenario: SQL 不伪造完整结算
- **WHEN** the settlement writes SQL
- **THEN** the submission is `failed` / `failed`, `terminal_sequence=0`, `fully_settled_at IS NULL`, and the effect type is `agent_delivery.never_started_settlement`

#### Scenario: 不重发历史 prompt
- **WHEN** the settlement succeeds
- **THEN** no historical instruction is sent and no tool call is replayed; only the existing queue recovery policy may release parked work

#### Scenario: 无关 block 与队列保持不变
- **WHEN** the session has other consumers' open blocks or unrelated queue entries
- **THEN** those records remain unchanged

### Requirement: Settlement MUST be idempotent and crash-convergent

Repeated settlements and crashes between journal retirement and SQL settlement MUST converge without duplicate effects, duplicate block resolutions, or prompt redispatch.

#### Scenario: 重复结算
- **WHEN** the same settled submission is settled again
- **THEN** the operation returns already-settled; exactly one effect exists; the bound block is resolved at most once; no queue work is released twice

#### Scenario: journal retire 后 SQL 失败
- **WHEN** the journal has been retired but the SQL transaction fails or the backend crashes
- **THEN** the next settlement attempt recognizes the retired record and commits the same SQL effect; the final state matches a single successful settlement

#### Scenario: SQL 已结算后迟到错误信号
- **WHEN** the prompt's uncertain-delivery handler runs after the submission was settled
- **THEN** it MUST NOT transition the submission back to `interrupted_unknown`, MUST NOT create a new `unknown_prompt_outcome` block for the settled submission, and MUST NOT revert the session to `RUNNING`

### Requirement: Missing evidence MUST fall back to the existing uncertain path

When any required evidence is unavailable or conflicts, the settlement MUST perform no journal or SQL write and MUST leave the submission in its existing uncertain state with its recovery block, preserving the current fail-closed behavior.

#### Scenario: 证据不可用
- **WHEN** the journal cannot be authenticated, the descriptor read fails, or the owner proof is unavailable
- **THEN** the submission remains `interrupted_unknown` and the existing `unknown_prompt_outcome` block remains open

#### Scenario: 部分输出
- **WHEN** the prompt produced at least one turn event before the stall
- **THEN** the zero-event precondition fails and no settlement is attempted

## Known Failure Modes

| Symptom | Cause | Fix |
|---------|-------|-----|
| 结算后 native resume 报 peer/SQL evidence disagree | 只结算 SQL、未 retire journal | 同一操作必须 retire exact journal 记录 |
| SQL 已 failed 但 resume 仍 blocked | journal 仍为 `dispatching` / `interrupted_unknown` 且未被 retire | retirement 必须接受带零事件授权的无 terminal 记录 |
| 迟到 uncertain 信号重建 block | prompt goroutine 在结算后仍走旧路径 | 结算事务与迟到信号必须识别已终态 submission，禁止重建 block |
| journal retire 成功但 SQL 未结算 | 事务或进程在两步之间失败 | 以 retired 记录为幂等证据重放 SQL 结算 |
| 部分输出被误结算 | 只看 journal 状态、不看 turn 事件证据 | never-started 分类与 activity epoch 必须仍然成立 |
| 其它未解决 submission 被连带释放 | descriptor 库存未校验 | 要求完整 unresolved inventory 只含 exact target |

