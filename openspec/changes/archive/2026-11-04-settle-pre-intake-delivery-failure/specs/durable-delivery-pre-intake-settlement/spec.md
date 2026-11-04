# durable-delivery-pre-intake-settlement

## 背景

durable delivery 的恢复模型以「journal 是保留证据」为前提：retry 的 reconstruction 要求 journal 恰好保留一个未解决候选（`selectReconstructionEvidence`），native resume adoption 要求 SQL 中的每个活跃 submission 都能在 journal 中匹配到证据（`validateNativeResumeSQLRows`）。这两个前提覆盖了「journal 有记录、SQL 丢失关联」和「journal 有记录、结果未知」，但没有覆盖「dispatch 在写 journal 之前就确定性失败」：此时 SQL 有 `interrupted_unknown` submission，journal 永远没有它，双方各自 fail-closed，恢复死锁。

线上事故会话 062a1281 就是该状态：SQL submission `prompt:send-now:eb53cfb8-e9e7-4bd7-8fec-85ca79413a71` 为 `interrupted_unknown` / `prompt_dispatch_failed` / `terminal_sequence=0`，保留 journal 只有三条 completed submission，该 ID 0 次出现；retry 返回 `retained_evidence_ambiguous`，native resume 返回 "native resume peer and SQL submission evidence disagree"。

## 问题

- 「dispatch 返回错误」与「结果未知」被当成同一件事：后端在 `finishPromptExecutorDispatch` 中对任意 `execErr` 执行 `markInterrupted("prompt_dispatch_failed")` 并创建 `unknown_prompt_outcome` 恢复块（`task_operations.go:7575-7588`、`agent_delivery_submission.go:40-56`）。
- 正常 settlement 只能由已投影的 terminal effect 驱动（`SettleAgentDeliveryTerminal` 以 stream+sequence 查 effect）；pre-intake 没有 terminal event，因此没有任何入口写 `updateDeliverySubmissionOutcomeTx` / `resolveBoundDeliveryBlocksTx`。
- 恢复证据只接受「恰好一个 journal 候选」；零候选被当作 ambiguous，而不是「从未投递」的证明。

## 方案与决策

- 把「确定性未投递」作为独立证据类别，而不是「不确定」的子情况。
- 结算只允许在同时满足以下条件时发生：submission 非终态且 `terminal_sequence = 0`、无 terminal effect；保留 journal 经认证属于同一 session/incarnation/generation、Durable、未截断、owner/lost marker 校验通过，并显式证明该 submission ID 不存在；没有其它未解决候选；恢复块与 submission/stream/generation 精确绑定。
- 结算在一个事务内完成：submission → `failed`；写入一个立即 completed 的 settlement effect（独立 effect type，避免被 `ListPendingAgentDeliverySettlements` 当作 terminal effect 再次结算）；解析匹配 block；推进 session recovery revision。结算不重发 prompt、不改 queue claim。
- 「无法证明」继续保持 fail-closed：journal 丢失、实例不可达、descriptor 截断、存在其它未解决候选、身份不匹配都返回 blocked。
- 新增「写前确定性拒收」的快速路径：agentctl 返回 typed admission 拒收且确认未写 journal 时，backend 直接结算 `failed`，不创建恢复块。
- 归档任务、Office 与自动化所有者保持既有排除规则。

## ADDED Requirements

### Requirement: 确定性写前拒收的 submission SHALL 结算为 failed
When agentctl refuses a durable prompt before writing the submission to its journal, Kandev SHALL settle the SQL submission as `failed` and MUST NOT create an `unknown_prompt_outcome` recovery block.

#### Scenario: 写前拒收直接结算
- **WHEN** agentctl 在 journal admission 写入之前拒绝 prompt（storage pressure 或前置检查失败），且没有写入任何 submission 记录
- **THEN** SQL submission 进入 `failed`，不产生活跃 submission，不产生新的恢复块

#### Scenario: 无法证明时保持不确定
- **WHEN** dispatch 错误可能发生在 journal admission 之后（例如响应丢失或 transport 中断）
- **THEN** submission 保持 `interrupted_unknown`，并创建 `unknown_prompt_outcome` 恢复块

### Requirement: Retry SHALL 结算可证明未进入保留 journal 的 submission
`RetrySessionDelivery` SHALL prove that an `interrupted_unknown` submission is absent from the authenticated retained journal and, when the proof holds, settle it `failed` and resolve only its bound recovery block in one transaction.

#### Scenario: pre-intake 缺失恢复成功
- **WHEN** 用户对一个 `prompt_dispatch_failed` / `terminal_sequence = 0` 的 submission 执行 retry，认证 journal 证明该 submission ID 不存在且没有其它未解决候选
- **THEN** retry 返回 `settled`，submission 变为 `failed`，绑定 block 解析，队列恢复派发，且没有发送任何 prompt

#### Scenario: 并发或重放的 retry 幂等
- **WHEN** 两个 retry 持有同一个仍处于 open 的 block 快照并先后执行
- **THEN** 都返回 `settled`，且只写入一次 effect、只解析一次 block、不重复释放队列

### Requirement: 结算证据 MUST 是认证且保守的
The settlement MUST require authenticated journal ownership, a supported durable protocol version, an untruncated descriptor, explicit absence of the exact submission ID, no other unresolved candidate, and exact session/incarnation/generation/block binding; any missing or conflicting evidence MUST keep recovery blocked.

#### Scenario: journal 丢失或不可达
- **WHEN** 保留 journal 无法认证、已丢失或实例不可达
- **THEN** retry 返回 blocked，submission 保持 `interrupted_unknown`，不结算

#### Scenario: 存在其它未解决候选
- **WHEN** journal 中还有其它未解决 submission
- **THEN** retry 返回 blocked（ambiguous），不结算当前 submission

### Requirement: 结算 MUST 保持授权与队列边界
The settlement MUST NOT dispatch the historical prompt, MUST NOT release unrelated queue entries or blocks, MUST NOT authorize legacy delivery fallback, and MUST NOT make archived tasks recoverable.

#### Scenario: 结算不重发
- **WHEN** pre-intake 结算成功
- **THEN** 不发送历史 instruction，仅允许已存在的队列消息按既有策略派发

#### Scenario: 无关 block 保留
- **WHEN** session 上还有其它 consumer 的 open block
- **THEN** 该 block 保持 open

## Known Failure Modes

| Symptom | Cause | Fix |
|---------|-------|-----|
| retry 永久返回 retained_evidence_ambiguous | journal 没有该 submission，恢复分支只支持「恰好一个候选」 | pre-intake absence 分支 |
| native resume 报 peer/SQL evidence disagree | SQL 活跃 submission 不在 journal | 先结算 failed，再 adoption |
| 结算后历史 prompt 被重放 | 把结算当成重发入口 | 结算只改状态，不派发 |
