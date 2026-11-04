# durable-delivery-pre-intake-settlement Specification

## Purpose
TBD - created by archiving change settle-pre-intake-delivery-failure. Update Purpose after archive.
## Requirements
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

