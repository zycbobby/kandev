# cross-instance-notifications Specification

## Purpose

远端实例的语义通知（会话完成 `session.turn_finished`、等待回答 `session.clarification_requested`、Office 收件箱 `office.inbox_item`、手工步等待 `task.manual_step_entered`）只在远端自己的浏览器连接里可见：本地面板开着也收不到，用户不看远端面板就漏事件。本机后端已为每个注册实例持有代理连接（见 `multi-instance-management`），通知事件的获取复用同一条后端到远端的连接面：本机后端作为远端的客户端订阅事件，再发布给本地面板。

远端事件与本地事件使用同一套通知逻辑：事件类型决定订阅、provider、声音、浏览器/桌面原生通知与查看抑制，origin 只决定实例标签；不再有侧边栏专用通知模块，relay store 只做去重与恢复。通知不提供打开动作，远端任务经其 Inbox 行到达。
## Requirements
### Requirement: 远端事件中继

The local backend SHALL maintain, for each enabled registered instance, a persistent event subscription to that remote instance plus a periodic reconciliation poll, both independent of which instance the panel is currently viewing, and SHALL deliver received remote semantic notification occurrences into the same notification pipeline local events use, carrying the origin instance identity. A relayed needs-you occurrence SHALL additionally refresh the Inbox read; a relayed informational occurrence SHALL NOT create or alter an Inbox row.

#### Scenario: 看本机时收到远端事件

- **WHEN** the panel is viewing the local instance and a remote session finishes a turn
- **THEN** the occurrence is delivered through the local notification settings (subscriptions, provider set, sound, browser-native or desktop-native notification, and viewing suppression) with its origin instance identity, rather than appearing only in a sidebar module

#### Scenario: 远端 needs-you 事件刷新 Inbox

- **WHEN** a relayed `session.clarification_requested` or `task.manual_step_entered` occurrence arrives
- **THEN** the Inbox read for that instance is refreshed, while a relayed `session.turn_finished` occurrence leaves the Inbox unchanged

#### Scenario: 断线后补齐

- **WHEN** the subscription to a remote instance drops and reconnects
- **THEN** events missed during the gap are recovered by the reconciliation poll within one poll interval, without duplicates

#### Scenario: 单实例故障不拖累其他

- **WHEN** one remote instance is unreachable
- **THEN** other instances' relays and the local instance's own notifications continue unaffected, and the failed instance shows a degraded status

### Requirement: 实例身份幂等投递

Relayed admission SHALL be idempotent keyed on (instance, event, occurrence); the relayed event set SHALL be the closed list defined by the relay (v2: `session.turn_finished`, `session.clarification_requested`, `office.inbox_item`, `task.manual_step_entered`); the relay SHALL forward occurrences only at the semantic boundary so the remote's deferred-notification semantics for parked sessions are inherited rather than reimplemented; and a relayed occurrence MUST be delivered at most once per local user and MUST NOT be re-recorded into the local occurrence history that peers reconcile against.

#### Scenario: 同一 occurrence 重放只投一次

- **WHEN** the same remote occurrence is observed twice (subscription replay plus reconciliation)
- **THEN** the local notification pipeline delivers it exactly once

#### Scenario: relayed occurrence 不进入本地历史

- **WHEN** a relayed occurrence has been delivered
- **THEN** no local occurrence row exists for it, so a peer's reconciliation cannot observe and re-relay it

#### Scenario: parked 会话延迟语义保持

- **WHEN** a remote session parks on background work and its turn-finished notification is withheld on the remote
- **THEN** no local notification appears until the remote actually dispatches the occurrence

#### Scenario: 不同实例的 ID 碰撞不混淆

- **WHEN** two instances report occurrences with the same ID for the same event type
- **THEN** both are kept in the relay ledger and addressed under their own instance identity, while one recipient's notification delivery deduplicates the same event and occurrence ID across origins so one logical occurrence re-observed through another instance is not announced twice

### Requirement: 读取状态与留存

Relayed occurrences SHALL no longer carry presentation state: the relay store SHALL retain (instance, event, occurrence) rows solely as the deduplication and recovery ledger under a bounded retention window, and no Inbox row, sidebar badge, notification entry, or unread state SHALL be derived from it. Notification entries and unread state SHALL come from the shared notification surfaces.

#### Scenario: 对账窗口有界

- **WHEN** a relayed occurrence exceeds the retention bound
- **THEN** it is removed from the relay store and can no longer cause a second delivery

#### Scenario: 展示不由 relay store 派生

- **WHEN** the side panel renders notifications and Inbox rows
- **THEN** none of them is read from the relay store, and the store's read/dismissed columns never change what is displayed

### Requirement: 提醒方式统一

A relayed occurrence SHALL receive the same notification treatment as a locally emitted event of the same kind: the same event-kind subscriptions, provider set, sound, browser-native or desktop-native notification, viewing-suppression behavior, and default-subscription state. A kind that is quiet by default locally SHALL stay quiet when relayed. The origin instance SHALL be visible as a label on the notification and on Inbox rows. Relayed notifications SHALL NOT expose an open action: navigation to a relayed task SHALL be available only by opening its Inbox row, and informational relayed kinds, which have no Inbox row, SHALL expose no task navigation and SHALL be reached by switching to the origin instance through the standard instance-switch path. Viewing suppression SHALL compare the active instance and task across origins. With federation disabled, local notification behavior SHALL be identical to before this capability.

#### Scenario: 已订阅事件在远端同样提醒

- **WHEN** the operator subscribes to an event kind and a remote instance emits that kind
- **THEN** it notifies with the same providers, sound, and browser-native or desktop-native notification as a local event of that kind

#### Scenario: 默认安静的事件保持安静

- **WHEN** an event kind is not subscribed by default locally and a remote instance emits it
- **THEN** no sound and no browser-native or desktop-native notification is produced for the relayed occurrence

#### Scenario: origin 标签

- **WHEN** a relayed notification is presented
- **THEN** it shows the origin instance label, and an Inbox row for the same occurrence also shows the origin label

#### Scenario: 通知不提供打开动作

- **WHEN** the operator interacts with a relayed notification
- **THEN** no open action or task navigation is offered; a relayed needs-you task is reached by opening its Inbox row, and an informational relayed kind is reached through the standard instance-switch path

#### Scenario: Inbox 行跳转

- **WHEN** the operator opens a relayed needs-you Inbox row whose origin is a remote instance
- **THEN** the panel switches to that instance via the standard switch path and opens the task route

#### Scenario: 查看抑制跨实例生效

- **WHEN** the panel is currently viewing the same task on the local instance or through the active remote instance
- **THEN** the relayed notification for that task is suppressed by the same viewing rule as a local event

#### Scenario: federation 关闭时行为不变

- **WHEN** federation is disabled
- **THEN** no relayed notification exists and local notification behavior is identical to before this capability

## Known Failure Modes

| Symptom | Cause | Fix |
|---|---|---|
| 看本机实例时收不到远端事件 | 中继订阅挂在了「当前活动实例」的连接上 | 订阅生命周期归属注册表而非面板选择；测试断言面板看本机时远端条目仍出现 |
| 同一远端事件重复出现 | 去重键漏了实例维度，或订阅重放与对账双路写入 | 去重键固定 (instance, event, occurrence)，写入走单一入口；重连+对账双路用例必须断言仅一条 |
| parked 会话被提前打扰 | 中继在远端分发前自行监听状态变化并提前投递 | 中继只消费语义 occurrence 边界（远端已 dispatch 的事件），spec 明示不重造延迟规则 |
| 远端事件被静默丢弃 | 收件人按本地 task_id 解析，远端任务在本地不存在 | 中继用显式收件人（instance owner）投递，不写本地 occurrence 历史 |
| 中继通知被对端再次中继 | 中继后的通知以事件名广播，对端 relay 当成本地事件接收并忽略 origin 字段 | relay 跳过带 `origin_instance_id` 的帧；中继帧不得二次入账 |
| relay 台账无界增长 | 去重行永不清理 | 留存窗口定期清理（启动清扫 + 周期清扫），沿用 SQLite 维护任务的 admission 纪律 |
| 远端长时间离线后对账风暴 | 重连后一次性拉全部历史 | 对账有分页与上限，超限只取最近窗口并给「更早事件未同步」提示 |
| 新事件对现有用户静默 | 只改默认 provider 的创建清单 | 一次性迁移为既有 Local/System provider 幂等补齐订阅，用户取消后不回填 |
| 设置页显示原始事件名 | `EVENT_MESSAGE_KEYS` 未登记新事件 | 为 `task.manual_step_entered` 增加标题/描述并补 7 locale |

