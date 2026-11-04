# durable-delivery-retry-succession Specification

## Purpose
Define when a delivery retry accepts a recovery-created successor execution and which identity checks must remain fail-closed.
## Requirements
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

