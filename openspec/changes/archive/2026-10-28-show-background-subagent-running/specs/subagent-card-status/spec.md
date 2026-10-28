# subagent-card-status

## 背景

聊天 transcript 把子代理（Task 工具）渲染为卡片，卡片状态由 message metadata 解析：`metadata.status` 是 tool 调用状态，`metadata.normalized.subagent_task.status` 是子代理级 agent 状态。对后台（异步）子代理，后端按既有规格（`docs/specs/agents/system-design/subagent-context-persistence-03.md` Terminality 节）在 dispatch 终态时把 tool status 定为 `complete`，同时原样保留 `agent_status = "async_launched"`；子代理本身带外继续运行，且不回写任何 transcript 消息。前端此前的解析只看 tool status，且活跃性被「所在 turn 是否活跃」门控，导致 turn 结束后后台卡片渲染为完成勾。

## 问题

- payload `async_launched`（或 `is_async` 且无终态 agent 结果）的卡片在所在 turn 结束后渲染完成勾，用户误以为子代理已结束（实测后台子代理可连续运行 15-40 分钟）。
- 唯一的子代理级真相（`agent_status`）已存在于 payload，但状态解析从未消费它。

## 方案与决策

- **后台运行态是 payload 驱动的显示态**：判定只看 `subagent_task`（`status === "async_launched"`，或 `is_async` 且无终态 agent 结果），与 `metadata.status`（tool 状态）及所在 turn 活跃性无关。`is_async` 的两个生产方（Claude async envelope、Cursor `isBackground`）都出现在「tool 调用终态、子代理转后台」的帧上，语义一致。
- **tool status `complete` 不再隐含子代理完成**：payload 处于后台运行态时，禁止把 tool status `complete` 渲染为完成勾。
- **状态与布局解耦**：活跃指示 + 显式标签由后台运行态驱动；自动展开与 meta 行维持既有 turn 域语义，turn 结束后后台卡片折叠为紧凑单行（活跃指示 + 标签 + meta 行含 background chip），避免 fan-out 场景下大量卡片永久展开。
- **无完成信号是已知边界**：Claude 的后台完成通知不带工具调用身份，无法归因到具体卡片（后端亦只能按 FIFO 粗略清账）。卡片停留在后台运行态是该边界下的正确行为，不是缺陷；禁止用 `active_subagent_count` 等粗粒度计数归零伪造完成，禁止虚构用量数值（对齐 `subagent-context-persistence` 规格 AC-7）。未来 `features.agentBackgroundWork` 投影（`docs/specs/agents/system-design/background-work.md`）上线后可提供可归因信号，届时另行变更。
- **前台（同步）子代理语义不变**：turn 活跃时 spinner，终态 agent 结果到达后勾/叉。

## ADDED Requirements

### Requirement: 后台子代理卡片渲染为后台运行态

A subagent card whose payload reports `async_launched`, or `is_async` without a terminal agent status, SHALL render a live indicator plus an explicit background-running label, and SHALL NOT render a completed, failed, or cancelled terminal icon, regardless of whether its containing turn is still active. Aggregate liveness surfaces that derive their running cue from subagent state (such as a collapsed turn group header) SHALL treat such a subagent as still running.

#### Scenario: turn 结束后仍显示后台运行态
- **WHEN** the card's payload status is `async_launched` and its tool status is `complete` and the containing turn is no longer active
- **THEN** the card renders a live indicator with the background-running label and does not render a completed checkmark

#### Scenario: is_async 且无终态 agent 结果同样进入后台运行态
- **WHEN** the card's payload has `is_async: true` and no terminal agent status (e.g. Cursor `isBackground`)
- **THEN** the card renders the background-running state instead of a completed checkmark

#### Scenario: turn 活跃期间使用同一后台运行标签
- **WHEN** the card is background-running and its containing turn is still active
- **THEN** the header shows the explicit background-running label rather than the generic working label

#### Scenario: turn 结束后折叠为紧凑行
- **WHEN** a background-running card's containing turn ends
- **THEN** the card does not auto-expand and its meta row (including the background chip) remains visible

#### Scenario: 折叠 turn 组的头保持运行指示
- **WHEN** a turn group contains a background-running subagent and the containing turn is no longer active
- **THEN** the group header keeps its running indicator

### Requirement: 前台子代理保持既有状态语义

Foreground (synchronous) subagent cards SHALL keep the existing status behavior: a live indicator while the containing turn is active, and the terminal icon derived from the terminal agent result after it arrives.

#### Scenario: 前台子代理行为不变
- **WHEN** a subagent card's payload has no async marker
- **THEN** its status rendering is unchanged from before this capability (spinner while the turn is active; completed, failed, or cancelled icon after the terminal result)

### Requirement: 终态 agent 结果优先于后台运行态

WHEN the card's payload carries a terminal agent status, THEN the card SHALL settle through the existing terminal resolution, and the background-running state SHALL NOT apply.

#### Scenario: is_async 但已终态则正常结算
- **WHEN** the card's payload has `is_async: true` and a terminal agent status such as `completed`
- **THEN** the card renders the terminal icon and the background chip as it does today

### Requirement: 不伪造完成与用量

The frontend SHALL NOT transition a background-running card to a terminal state without a terminal agent result in its payload, and SHALL NOT derive or invent usage values for it.

#### Scenario: 无完成信号时保持后台运行态
- **WHEN** a background-running subagent completes out-of-band and no per-card completion signal exists
- **THEN** the card remains in the background-running state indefinitely

#### Scenario: 不用粗粒度计数伪造完成
- **WHEN** a session-level subagent count (e.g. `active_subagent_count`) reaches zero while a card's payload still reports the background-running condition
- **THEN** the card is not settled to completed on that basis alone

## Known Failure Modes

| Symptom | Cause | Fix |
|---------|-------|-----|
| 后台卡片又显示完成勾 | 状态解析回退到只看 tool status，或后台运行态重新被 turn 活跃性门控 | 回归测试覆盖「async + turn 失活」组合 |
| fan-out 场景 transcript 被撑满 | 把后台运行态并入自动展开条件 | 自动展开维持 turn 域语义，后台卡片折叠 |
| 后台卡片突然批量变完成 | 用 `active_subagent_count` 归零做翻转启发式 | 禁止；完成只能来自 payload 终态 agent 结果 |
| 标签缺翻译 | 新 i18n key 未补齐 7 语言 | `pnpm run i18n:check` + `i18n:ratchet` 门禁 |
