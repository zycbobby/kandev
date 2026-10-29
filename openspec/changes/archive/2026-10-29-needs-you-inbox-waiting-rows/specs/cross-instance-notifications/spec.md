# cross-instance-notifications

## 背景

远端实例的语义通知过去只在远端自己的浏览器连接里可见；本机通过每个实例一条 relay 订阅加对账轮询把 occurrence 收回，但只投进侧边栏专用通知模块，并明文规定不弹 toast、不弹浏览器原生通知、不响声音。于是操作者要维护两套通知心智：本地按通知设置的订阅规则推送，远端永远只是侧边栏里的一条条记录。本 change 把两者统一：relay 收回的 occurrence 携带 origin 进入与本地事件相同的 shared notification pipeline，按事件类型使用同一套订阅与提醒规则；侧边栏专用模块移除，relay store 退回只做去重与恢复。relayed set 同时升级为 v2，加入手工步等待事件 `task.manual_step_entered`。

## 方案与决策

- **统一投递，不统一来源解析**：relay 已知 `inst.OwnerUserID`，投递时以显式收件人进入 notification service；不经过本地 task→workspace owner 解析（远端 task_id 在本地不存在，会被直接丢弃）。
- **不回写本地 occurrence 历史**：外来 occurrence 只进 relay store 的去重账本，不写入本地 `/api/v1/notifications/occurrences` 对应的历史表，否则双向联邦会把同一条事件再中继回去。去重键仍为 (instance, event, occurrence)。
- **origin 只影响标签与查看抑制，不影响提醒规则**：事件类型决定订阅、provider、声音、浏览器/桌面原生通知与查看抑制；origin 只决定通知与 Inbox 行上的实例标签，以及查看抑制时的实例比较。通知本身不提供打开动作，导航经 Inbox 行。
- **默认订阅与本地一致**：`task.manual_step_entered` 进入 `AvailableEvents` 与默认 Local/System provider 订阅，并对既有默认 provider 幂等补齐，使现有安装也能收到。
- **read/dismissed 不再是展示状态**：relay store 的读取与忽略列失去展示含义，保留 bounded retention 作为对账窗口。

## RENAMED Requirements

- FROM: `### Requirement: 提醒方式限定`
- TO: `### Requirement: 提醒方式统一`

## REMOVED Requirements

### Requirement: 侧边栏通知模块

**Reason**: 远端事件与本地事件使用同一套通知逻辑后，专用侧边栏模块是只对远端事件存在的第二套展示路径，与新契约冲突；origin 标签已经提供实例上下文。

**Migration**: 移除 `components/app-sidebar/sections/notifications-section.tsx` 及其在桌面 sidebar 与手机 AppNavSheet 的挂载、`remote-notifications` slice/hook/API 客户端，以及模块的 REST 读取与 dismiss 端点。relayed 事件改由通知设置订阅的 provider 与通知中心呈现；不再保留模块级的未读/已读状态，relay store 只做去重与恢复。手机端随模块一起移除入口，不新增替代的远端专用页面。

## MODIFIED Requirements

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

### Requirement: 读取状态与留存

Relayed occurrences SHALL no longer carry presentation state: the relay store SHALL retain (instance, event, occurrence) rows solely as the deduplication and recovery ledger under a bounded retention window, and no Inbox row, sidebar badge, notification entry, or unread state SHALL be derived from it. Notification entries and unread state SHALL come from the shared notification surfaces.

#### Scenario: 对账窗口有界

- **WHEN** a relayed occurrence exceeds the retention bound
- **THEN** it is removed from the relay store and can no longer cause a second delivery

#### Scenario: 展示不由 relay store 派生

- **WHEN** the side panel renders notifications and Inbox rows
- **THEN** none of them is read from the relay store, and the store's read/dismissed columns never change what is displayed

## Known Failure Modes

| Symptom | Cause | Fix |
|---------|-------|-----|
| 远端事件被静默丢弃 | relay 复用了按本地 task_id 解析收件人的入口 | 使用显式收件人（instance owner）的新入口 |
| 同一条远端事件被中继回源实例 | relayed occurrence 被写入本地 occurrence 历史 | relayed 投递不写本地历史；去重仍以 relay store 为准 |
| 远端事件在 flag-off 时出现 | 移除侧边栏模块时未保持 `features.multiInstance` 闸门 | 删除的路径全部位于 multiInstance 之后；flag-off 面板行为与今天一致 |
| 新事件对现有用户静默 | 只改了默认 provider 的创建清单 | 对既有默认 Local/System provider 幂等补齐订阅 |
| 设置页显示原始事件名 | `EVENT_MESSAGE_KEYS` 未登记新事件 | 为 `task.manual_step_entered` 增加标题/描述并补 7 locale |
