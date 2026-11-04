# durable-delivery-retry-succession

## 背景

durable delivery 的 retry 以 recovery metadata 中的 `agent_execution_id` 作为历史 owner 身份。一次 interrupted turn 的 resume 可能先写入 phase `uncertain` 的 recovery 记录（绑定旧 execution），随后为同一会话创建新的 successor execution 并更新 `executors_running.agent_execution_id`。此时 retry 的 `recoverySessionMatches` 仍要求 `executors_running.agent_execution_id` 与 recovery 记录完全相等，于是 retry 在毫秒级以 `recovery_identity_mismatch` 失败。

运行时层其实已经具备 successor 证据：`RecoverAgentPromptStreamWithIdentity` 在检测到当前 execution 与历史 identity 不同时，会调用 `loadedRecoverySuccessorMatches`，要求同一 task/session/incarnation/generation、session 已初始化、无 pending dispatch，并通过 `proveHistoricalDeliveryOwnerDead` 证明旧 owner 已终止，然后走 `recoverLoadedSuccessorDelivery`。问题在于 orchestrator 的数据库层 guard 先于该路径 fail，使 runtime 的合法 successor 证据没有机会被使用。

## 问题

- `recoverySessionMatches` 把 execution ID 当成静态身份等值校验，忽略了 recovery 流程自己创建的 successor。
- 该 fail 发生在读取 journal / runtime 证据之前，因此无法区分「当前绑定是合法 successor」与「当前绑定属于无关 execution」。
- 结果是「original execution absent」的 retry 在 successor 存在时反而被拒，与既有契约（retry 应在 original execution 缺失时仍可用）冲突。

## 方案与决策

- retry 继续校验 recovery 的 session、incarnation、harness generation、submission、stream 与 prompt generation；这些字段是 recovery 记录与 SQL/journal 绑定的基础。
- 去掉 `recovery.AgentExecutionID == session.AgentExecutionID` 的等值比较，把 execution 归属交给运行时的 authenticated successor 证据裁决。
- 运行时仍保持 fail-closed：旧 owner 未证明终止、successor 未初始化、successor 有 pending prompt、或 incarnation/generation 不匹配时返回 owner mismatch。
- 没有 `executors_running` 行（`session.AgentExecutionID == ""`）与「当前 execution 就是 recovery execution」的路径行为不变。
- 不改变 native resume 的 acknowledgement 规则；本能力只影响 retry 的 entry guard。

## ADDED Requirements

### Requirement: Retry MUST defer execution succession to authenticated runtime evidence

`RetrySessionDelivery` MUST validate the recovery record's session, incarnation, harness generation, submission, stream, and prompt generation, and MUST NOT reject the retry solely because `executors_running.agent_execution_id` differs from the recorded execution. Execution ownership MUST be decided by the runtime's authenticated successor evidence.

#### Scenario: recovery 创建的 successor execution
- **WHEN** recovery 记录的 execution 已死亡，而 `executors_running` 指向同一 task/session/incarnation/generation 的 successor execution，且运行时能证明旧 owner 已终止、successor 已初始化且没有 pending prompt
- **THEN** retry 进入运行时 reconciliation，而不是返回 `recovery_identity_mismatch`

#### Scenario: live 且非 successor 的 execution
- **WHEN** 当前 execution 存活，但不满足 successor 证据（incarnation/generation 不同、未初始化、有 pending prompt 或旧 owner 未证明死亡）
- **THEN** retry 返回 blocked/owner mismatch，不读取或修改 journal/SQL 结算状态

### Requirement: Retry MUST retain non-execution identity checks

The retry MUST continue to reject recovery records whose session, incarnation, harness generation, submission, stream, or prompt generation do not match the current canonical identity.

#### Scenario: 非 execution 身份不匹配
- **WHEN** recovery 的 session/incarnation/generation/submission/stream/prompt generation 与当前 session 或 SQL 行不一致
- **THEN** retry 返回 `recovery_identity_mismatch` 或等价 blocked 结果，且不调用运行时 reconciliation

### Requirement: Successor acceptance MUST remain fail-closed

The runtime successor path MUST require a complete recovery identity, a matching task/session/incarnation/generation, an initialized native session, no pending prompt dispatch, and proof that the historical owning process terminated.

#### Scenario: 旧 owner 未证明死亡
- **WHEN** `OriginalRuntime` 缺失、无效，或 `OwnedSessionTerminated` 返回 false/error
- **THEN** retry 返回 blocked，不把当前 execution 当作 successor

#### Scenario: successor 状态不满足
- **WHEN** 当前 execution 不是 ready、尚未初始化 native session、存在 pending prompt，或 stream 与 recovery 记录不匹配
- **THEN** retry 返回 blocked，不进入 successor recovery

### Requirement: Existing execution-absent and same-execution paths MUST remain unchanged

A retry with no `executors_running` row MUST continue to use retained evidence, and a retry whose current execution equals the recorded execution MUST follow the existing reconciliation path.

#### Scenario: 没有 execution 行
- **WHEN** `executors_running.agent_execution_id` 为空
- **THEN** retry 继续使用 retained journal evidence，行为与改动前一致

#### Scenario: 当前 execution 就是 recovery execution
- **WHEN** `executors_running.agent_execution_id` 与 recovery 记录一致
- **THEN** retry 走既有 reconciliation 路径，不触发 successor 分支

## Known Failure Modes

| Symptom | Cause | Fix |
|---------|-------|-----|
| retry 立即返回 `recovery_identity_mismatch` | `executors_running` 已被 recovery resume 重绑到新 execution | 去掉 execution ID 等值比较，交给运行时 successor 证据 |
| 合法 successor 被当成 foreign execution | 数据库层缺少 successor 生命周期语义 | 运行时 `loadedRecoverySuccessorMatches` + dead-owner proof |
| 放宽后误用 live execution | 只删除了 guard，没有依赖运行时 fail-closed 校验 | 保留完整 successor 证据校验 |
