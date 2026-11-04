# durable-delivery-abandoned-settlement Specification

## Purpose
Define the conservative settlement for journal-present submissions whose terminal evidence is lost and whose historical owner is proven dead, without fabricating a terminal outcome.
## Requirements
### Requirement: Retry SHALL settle a journal-present unproven submission after proven owner death

When an `interrupted_unknown` submission exists in the authenticated retained journal without `terminal_event_retained` or a terminal sequence, the persisted recovery record is in the `uncertain` phase without a recorded history gap, and the historical dispatch owner is proven terminated, `RetrySessionDelivery` SHALL retire the exact journal record and settle the SQL submission as `failed` together with its bound recovery block.

#### Scenario: 已证明死亡且无 terminal evidence
- **WHEN** retry 命中一个 `interrupted_unknown` submission：journal 中该 submission 无 terminal event/sequence，SQL 行为非终态，绑定 block 精确匹配，recovery 记录处于 `uncertain` phase 且没有 `HistoryGap`，且 recovery 记录中的 `OriginalRuntime` 经 `processidentity.OwnedSessionTerminated` 证明已终止
- **THEN** retry 返回 `settled`；journal 记录被 retire 且保留原 payload/state；SQL submission 变为 `failed`/`failed`；绑定 block 被解析；没有发送任何 prompt

#### Scenario: recovery phase 不是 uncertain
- **WHEN** recovery 记录不是 `uncertain` phase，或存在 `HistoryGap`
- **THEN** 不执行 abandoned settlement；retry 走既有 reconciliation 路径，本路径不产生 journal 或 SQL 写入

#### Scenario: terminal evidence 仍然优先
- **WHEN** journal 中该 submission 已保留 terminal event 与 terminal sequence
- **THEN** 走既有 terminal settlement 路径，不执行 abandoned settlement，不改写 journal 的 terminal 证据

#### Scenario: owner 未证明死亡时保持 blocked
- **WHEN** `OriginalRuntime` 缺失、无效，或 `OwnedSessionTerminated` 返回 false/error
- **THEN** retry 返回 blocked；journal 与 SQL 均不发生改变

### Requirement: Abandoned settlement evidence MUST be authenticated and conservative

The settlement MUST require an authenticated journal owner, a supported protocol version, an untruncated descriptor whose complete unresolved inventory consists only of the exact target or other provably retireable `interrupted_unknown` peers, exact session/incarnation/generation/stream/submission binding, and a matching immutable payload hash.

#### Scenario: descriptor 截断或存在其它 unresolved 形态
- **WHEN** descriptor 被截断，或 unresolved inventory 中存在 `prepared`/`accepted`/`dispatching`，或存在非 `interrupted_unknown` 的无 terminal 记录
- **THEN** retry 返回 blocked，不 retire 任何记录，不结算 SQL

#### Scenario: 身份或 payload 不一致
- **WHEN** session、incarnation、generation、stream、submission ID 或 journal hash 与 SQL 行不匹配
- **THEN** retry 返回 blocked，不执行任何写操作

#### Scenario: block 绑定不匹配
- **WHEN** block 的 consumer、submission ID、stream、incarnation 或 generation 与证据不一致
- **THEN** retry 返回 blocked，只允许解析 exact bound block 的规则保持不变

### Requirement: Settlement MUST retire journal evidence without fabricating terminal outcomes

The journal record MUST be marked retired rather than deleted, and the SQL settlement MUST NOT claim a terminal event, terminal sequence, terminal turn, or `fully_settled_at`.

#### Scenario: journal retirement 保留证据
- **WHEN** abandoned settlement 成功
- **THEN** journal submission 的 `retired=true`，state 仍为 `interrupted_unknown`，payload 与 hash 保持不变，`terminal_event_retained=false`、`terminal_sequence=0`

#### Scenario: SQL 不伪造 terminal proof
- **WHEN** abandoned settlement 写入 SQL
- **THEN** submission 为 `failed`/`failed`，`terminal_sequence=0`，`fully_settled_at IS NULL`，不产生 `agent_delivery.terminal_settlement` effect

#### Scenario: native resume 忽略 retired peer
- **WHEN** 后续 native resume / `GetNativeResumeDeliveryEvidence` 读取同一 session
- **THEN** 已 retired 且没有 retained terminal evidence 的 journal peer 不再触发 terminal evidence 校验，也不再计入 `Unresolved`；带 terminal evidence 的 retired peer 仍作为 terminal proof 校验

### Requirement: Abandoned settlement MUST be idempotent and crash-convergent

Repeated retries and crashes between journal retirement and SQL settlement MUST converge without duplicate effects, duplicate block resolutions, or prompt redispatch.

#### Scenario: 重复 retry
- **WHEN** 同一个已完成的 abandoned settlement 再次被 retry
- **THEN** retry 再次返回 `settled`；effect 只存在一份；block 只解析一次；不重复释放队列

#### Scenario: journal retire 后 SQL settlement 失败
- **WHEN** journal 已 retire，但 SQL 事务因冲突或数据库错误失败
- **THEN** 下一次 retry 能识别已 retired 记录并重新提交同一结算；最终状态与一次成功结算一致

#### Scenario: 清理过期 recovery notice
- **WHEN** abandoned settlement 成功
- **THEN** 绑定该 submission、incarnation 与 generation 的 `agent_delivery_recovery` metadata 被清理；其它 recovery 原因或其它 submission 的 metadata 保持不变

### Requirement: Abandoned settlement MUST preserve authorization, queue and owner boundaries

The settlement MUST NOT dispatch the historical prompt, MUST NOT resolve unrelated blocks or queue entries, and MUST NOT make archived tasks, Office/automation-owned sessions, or remote executor journals recoverable through this path.

#### Scenario: 不重发历史 prompt
- **WHEN** abandoned settlement 成功
- **THEN** 不发送历史 instruction，不重放工具调用；队列只按既有恢复策略释放

#### Scenario: 无关 block 与队列保持不变
- **WHEN** session 上还有其它 consumer 的 open block 或未关联队列条目
- **THEN** 这些记录保持原状

#### Scenario: 排除所有者
- **WHEN** task 已归档、session 属于 Office/automation，或 journal 不在本地 retained runtime 可达范围内
- **THEN** retry 在任何 journal/SQL 写入之前拒绝该路径

## Known Failure Modes

| Symptom | Cause | Fix |
|---------|-------|-----|
| retry 永久返回 `terminal_outcome_missing` | journal 无 terminal evidence，运行时没有结算入口 | abandoned settlement + journal retirement |
| native resume 报 `native resume terminal evidence disagrees` | journal-only `interrupted_unknown` peer 无 terminal evidence | retire exact peer，并让 peer loop 忽略无 terminal evidence 的 retired 记录 |
| SQL settled 后 admission 仍报 `unresolved_durable_work` | journal 记录未 retire | settlement 必须同时 retire journal，而不是只改 SQL |
| 结算后 UI 显示 completed 或 terminal 结果 | 复用了 terminal effect / 伪造了 terminal proof | 使用独立 effect type，`fully_settled_at` 保持 NULL |
| Office/automation session 被 abandoned settlement 结算 | retry 入口只校验归档与用户授权，新路径未检查所有者 | 在读取证据前排除 Office/automation-owned session |

