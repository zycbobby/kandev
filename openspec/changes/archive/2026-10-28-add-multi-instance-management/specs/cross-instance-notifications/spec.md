# cross-instance-notifications

## 背景

远端实例的语义通知（会话完成 `session.turn_finished`、等待回答 `session.clarification_requested`、Office 收件箱 `office.inbox_item`）只在远端自己的浏览器连接里可见：本地面板开着也收不到，用户不看远端面板就漏事件。本机后端既然已为每个注册实例持有代理连接（见 `multi-instance-management`），通知事件的获取可以复用同一条后端到远端的连接面：本机后端作为远端的客户端订阅事件，再发布给本地面板。

用户已拍板提醒形态：远端事件**仅**侧边栏通知模块 + 未读角标，不弹 toast、不弹浏览器原生通知、不响声音；本地实例既有通知行为完全不变。

## 方案与决策

- **事件获取 = 每注册实例一条持久 WS 订阅 + 周期 REST 对账**。订阅连接独立于面板当前查看哪个实例（否则"看本机时漏 3090Ti"的痛点没解决）；远端零改动（不需要在 3090Ti 上部署任何东西）。断线用退避重连，对账轮询补齐错过的事件。
- **中继点挂在语义 occurrence 边界**：远端的通知服务已按 provider+event+occurrence 幂等去重，且 parked 会话的延迟投递语义（`docs/specs/parked-notification-deferral/spec.md`）在远端分发前已生效。中继只转发已发生的语义 occurrence，不在本地再发明第二套延迟/抑制规则。
- **去重键扩展实例身份**：本地侧投递幂等键为 (instance, event, occurrence)，因为不同实例的 turn/clarification ID 空间会碰撞。
- **中继事件集合是封闭列表**：由中继方定义（v1：`session.turn_finished`、`session.clarification_requested`、`office.inbox_item`），不是 agent 或远端任意可控的通道；`system.update_available` 不中继（远端的更新提示与本机面板无关）。
- **通知模块 = 侧边栏新 section**：按实例分组、未读角标（模块入口 + 实例选择器处）、点击条目 = 切换到对应实例并导航到该任务（`/t/:id` 路由在目标实例上下文中打开）。读取状态本地持久化、有界留存。

## ADDED Requirements

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
|---------|-------|-----|
| 看本机实例时收不到远端事件 | 中继订阅挂在了"当前活动实例"的连接上 | 订阅生命周期归属注册表而非面板选择；测试断言面板看本机时远端条目仍出现 |
| 同一远端事件重复出现 | 去重键漏了实例维度，或订阅重放与对账双路写入 | 去重键固定 (instance, event, occurrence)，写入走单一入口；重连+对账双路用例必须断言仅一条 |
| parked 会话被提前打扰 | 中继在远端分发前自行监听状态变化并提前投递 | 中继只消费语义 occurrence 边界（远端已 dispatch 的事件），spec 明示不重造延迟规则 |
| 通知模块无界增长 | 条目与读取状态永不清理 | 留存窗口定期清理（启动清扫 + 周期清扫），沿用 SQLite 维护任务的 admission 纪律 |
| 远端长时间离线后对账风暴 | 重连后一次性拉全部历史 | 对账有分页与上限，超限只取最近窗口并给"更早事件未同步"提示 |
| 手机端模块不可达 | 只在桌面 sidebar 挂了 section | 手机导航接入点（AppNavSheet 分组）与角标状态纳入 mobile parity 验证清单 |
