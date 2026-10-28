# subagent-card-status Specification

## Purpose

聊天 transcript 中子代理（Task 工具）卡片的状态解析与渲染契约：依据 payload（`normalized.subagent_task`）区分「后台运行态」与终态。tool status `complete` 只代表 dispatch 终态，不隐含子代理完成；payload `async_launched`（或 `is_async` 且无终态 agent 结果）的卡片渲染活跃指示 + 显式后台运行标签，不受所在 turn 活跃性门控，折叠 turn 组头同样保持运行指示。终态 agent 结果优先结算；无完成信号是已确认边界（卡片停留后台运行态），禁止用 `active_subagent_count` 等粗粒度计数归零伪造完成或虚构用量。前台（同步）子代理行为不变。

## Requirements
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
| --- | --- | --- |
| 后台卡片又显示完成勾 | 状态解析回退到只看 tool status，或后台运行态重新被 turn 活跃性门控 | 回归测试覆盖「async + turn 失活」组合 |
| fan-out 场景 transcript 被撑满 | 把后台运行态并入自动展开条件 | 自动展开维持 turn 域语义，后台卡片折叠 |
| 后台卡片突然批量变完成 | 用 `active_subagent_count` 归零做翻转启发式 | 禁止；完成只能来自 payload 终态 agent 结果 |
| 标签缺翻译 | 新 i18n key 未补齐 7 语言 | `pnpm run i18n:check` + `i18n:ratchet` 门禁 |

