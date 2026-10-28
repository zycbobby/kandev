# cross-instance-notifications Specification

## Purpose

远端实例的语义通知（会话完成 `session.turn_finished`、等待回答 `session.clarification_requested`、Office 收件箱 `office.inbox_item`）只在远端自己的浏览器连接里可见：本地面板开着也收不到，用户不看远端面板就漏事件。本机后端已为每个注册实例持有代理连接（见 `multi-instance-management`），通知事件的获取复用同一条后端到远端的连接面：本机后端作为远端的客户端订阅事件，再发布给本地面板。

用户已拍板提醒形态：远端事件仅侧边栏通知模块 + 未读角标，不弹 toast、不弹浏览器原生通知、不响声音；本地实例既有通知行为完全不变。

## Requirements
### Requirement: 远端事件中继
The local backend SHALL maintain, for each enabled registered instance, a persistent event subscription to that remote instance plus a periodic reconciliation poll, both independent of which instance the panel is currently viewing, and SHALL re-publish received remote semantic notification events to local browser clients.

#### Scenario: 看本机时收到远端事件
- **WHEN** the panel is viewing the local instance and a remote session finishes a turn
- **THEN** a notification entry for that remote instance appears in the sidebar notifications module

#### Scenario: 断线后补齐
- **WHEN** the subscription to a remote instance drops and reconnects
- **THEN** events missed during the gap are recovered by the reconciliation poll within one poll interval, without duplicates

#### Scenario: 单实例故障不拖累其他
- **WHEN** one remote instance is unreachable
- **THEN** other instances' relays and the local instance's own notifications continue unaffected, and the failed instance shows a degraded status

### Requirement: 实例身份幂等投递
Relayed delivery SHALL be idempotent keyed on (instance, event, occurrence), the relayed event set SHALL be the closed list defined by the relay (v1: `session.turn_finished`, `session.clarification_requested`, `office.inbox_item`), and the relay SHALL forward occurrences only at the semantic boundary so the remote's deferred-notification semantics for parked sessions are inherited rather than reimplemented.

#### Scenario: 同一 occurrence 重放只投一次
- **WHEN** the same remote occurrence is observed twice (subscription replay plus reconciliation)
- **THEN** the local module holds exactly one entry for it

#### Scenario: parked 会话延迟语义保持
- **WHEN** a remote session parks on background work and its turn-finished notification is withheld on the remote
- **THEN** no local entry appears until the remote actually dispatches the occurrence

#### Scenario: 不同实例的 ID 碰撞不混淆
- **WHEN** two instances report occurrences with the same ID for the same event type
- **THEN** both entries are kept and rendered under their own instances

### Requirement: 侧边栏通知模块
The web UI SHALL surface relayed notifications in a sidebar notifications module grouped by instance with unread badges on the module entry, and clicking an entry SHALL switch the panel to that instance and navigate to the referenced task.

#### Scenario: 未读角标随事件增长
- **WHEN** a relayed event arrives and its entry is unread
- **THEN** the module badge and the originating instance's badge increase, and both clear when the entries are read

#### Scenario: 点击条目跳转
- **WHEN** the user clicks an entry for a task on another instance
- **THEN** the panel switches to that instance via the standard switch path and opens the task's route

#### Scenario: 手机端可达
- **WHEN** the sidebar is rendered at phone width
- **THEN** the notifications module is reachable from phone app navigation with the same unread state, per mobile parity requirements

### Requirement: 提醒方式限定
Relayed remote notifications SHALL NOT trigger toasts, browser native notifications, or sound; the local instance's own notification behavior SHALL remain unchanged by this capability.

> **修订预告（2026-10-28）:** 本条描述的是当前已发布行为，不是终态契约。已裁定的新方向是远端与本地事件使用同一套通知逻辑：relay 恢复的 occurrence 携带实例 origin 标签进入与本地事件相同的 shared notification pipeline（`internal/notifications`），按事件类型同规则（订阅了才推送、默认安静、查看抑制、点击走标准 instance-switch 跳转）；侧边栏专用通知模块在终态移除，relay store 只保留 (instance, event, occurrence) 去重与恢复职责。该终态由后续变更 needs-you-inbox-waiting-rows（REQ-UI-NEEDS-YOU-INBOX-002~004）实现并随其修订本条与「侧边栏通知模块」要求；在此之前本条仍约束当前行为。

#### Scenario: 远端事件不弹打扰
- **WHEN** a relayed remote event arrives while the user is working in the panel
- **THEN** no toast, browser notification, or sound is produced for it

#### Scenario: 本地通知行为不变
- **WHEN** a local instance session finishes a turn or requests clarification
- **THEN** the existing Local provider delivery (toast, browser notification, sound, and viewing suppression) behaves exactly as before this change

### Requirement: 读取状态与留存
Read and dismissed state SHALL persist locally per (instance, occurrence), and notification entries SHALL be retained under a bounded window after which they are removed automatically.

#### Scenario: 刷新后已读保留
- **WHEN** the page is reloaded after reading entries
- **THEN** those entries remain read and the badge count stays zero for them

#### Scenario: 过期自动清理
- **WHEN** an entry exceeds the retention bound
- **THEN** it is removed from the module without user action and never reappears for the same occurrence

## Known Failure Modes

| Symptom | Cause | Fix |
|---|---|---|
| 看本机实例时收不到远端事件 | 中继订阅挂在了「当前活动实例」的连接上 | 订阅生命周期归属注册表而非面板选择；测试断言面板看本机时远端条目仍出现 |
| 同一远端事件重复出现 | 去重键漏了实例维度，或订阅重放与对账双路写入 | 去重键固定 (instance, event, occurrence)，写入走单一入口；重连+对账双路用例必须断言仅一条 |
| parked 会话被提前打扰 | 中继在远端分发前自行监听状态变化并提前投递 | 中继只消费语义 occurrence 边界（远端已 dispatch 的事件），spec 明示不重造延迟规则 |
| 通知模块无界增长 | 条目与读取状态永不清理 | 留存窗口定期清理（启动清扫 + 周期清扫），沿用 SQLite 维护任务的 admission 纪律 |
| 远端长时间离线后对账风暴 | 重连后一次性拉全部历史 | 对账有分页与上限，超限只取最近窗口并给「更早事件未同步」提示 |
| 手机端模块不可达 | 只在桌面 sidebar 挂了 section | 手机导航接入点（AppNavSheet 分组）与角标状态纳入 mobile parity 验证清单 |

