## Why

Needs-you Inbox 目前只回答一种等待：clarification bundle。任务进入手工工作流步骤（不自动启动 agent、也不在进入时完成任务）时，既不产生通知，也不产生 Inbox 行，操作者只能盯看板才能发现任务在等自己。跨实例侧则是另一套逻辑：远端事件只进侧边栏专用模块，且被明文规定不弹 toast、不弹浏览器原生通知、不响声音，与本地事件的通知规则不一致。

已提交的设计包（`docs/specs/ui/requirements/needs-you-inbox-waiting-rows.md`、同目录 system design、`docs/plans/needs-you-inbox-waiting-rows/`）已裁定终态：等待行成为第三种 Inbox 行，远端与本地共用一套通知逻辑，计数与列表仍来自同一读。landed 的 remote-questions change 已提供每实例远端组合基础。本 change 把该设计包落成可实施变更，并按 Explore 阶段对当前代码的核对结果修正 5 处实现前提，避免照字面实现后违反验收标准。

## What Changes

**本地手工步等待行（REQ-UI-NEEDS-YOU-INBOX-003）**
- From: Inbox 只列 clarification bundle；手工步任务只有在看板上可见。
- To: `GET /api/v1/needs-you-inbox` 合并 question 与 waiting 两类行；waiting 行由任务当前步骤语义派生，一个 episode 一行，dismiss/snooze/restore 复用现有 per-user sidecar。
- Reason: 任务在手工步等操作者时必须有与澄清同级的可见性。
- Impact: non-breaking，纯新增行种；question 行契约不变。

**手工步 episode 通知（REQ-UI-NEEDS-YOU-INBOX-003.4/.5/.7）**
- From: 手工步等待没有任何推送信号。
- To: 新增语义事件 `task.manual_step_entered`，与 clarification 走同一通知 pipeline；本人移动不推送；通知投递与 `features.NeedsYouInbox` 无关，启用 flag 不回填已开始的 episode。
- Reason: 让操作者在任务开始等待时就被提醒，而不是靠轮询看板。
- Impact: 既有安装的默认 Local/System provider 需要幂等补齐该事件订阅，否则现有用户收不到。

**远端事件统一通知（REQ-UI-NEEDS-YOU-INBOX-004）**
- From: relay 把 occurrence 广播成 `ws.ActionRemoteNotification`，只由侧边栏专用模块消费；relayed 事件被规定为静默。
- To: relay 把去重后的 occurrence 连同 origin 标签交给 shared notification service；relayed set 升级为 v2（增加 `task.manual_step_entered`）；按事件类型的订阅、provider、声音、浏览器/桌面原生通知与查看抑制与本地完全一致；通知本身不提供打开动作，远端任务通过打开其 Inbox 行到达，信息型通知只能手动切换实例；侧边栏模块、remote-notifications slice/hook/API 及其 REST 读写移除；relay store 只保留去重与恢复职责。
- Reason: 一套设置、一套心智模型覆盖所有实例，同时不引入桌面通知插件的升级与跨平台点击语义。
- Impact: 移除用户可见的侧边栏模块；relayed occurrence 不得回写本地 occurrence 历史，避免双向联邦回灌；AC-004.3 已按此决定收窄（通知不提供打开动作）。

**统一等待读与计数（REQ-UI-NEEDS-YOU-INBOX-005）**
- From: 计数、列表、隐藏数、截断只覆盖 bundle；boot 只 seed 本机页。
- To: 同一聚合响应给出合并行的 rows/count/hidden_count/has_more；排序按各类 reference time 升序、id 升序；隐藏行不占页位；boot summary、badge 与列表复用同一 merge helper 与同一 bound；本地读失败渲染既有错误态，远端降级不算本地失败。
- Reason: 扩大行集不能放松「count 与 list 同源」的保证。
- Impact: 新增聚合端点，既有 `/api/v1/clarification-inbox` 保留给 history 与远端转发契约。

**远端手工步等待行（REQ-UI-NEEDS-YOU-INBOX-005.6）**
- From: 远端实例只组合 claimable clarification rows。
- To: 聚合端点作为远端 API surface 暴露；本地 Inbox 通过 landed 的每实例组合读取远端自己的等待行；远端不再报告时行消失；不可达、需凭据或版本不支持时按实例降级，绝不拖垮本地读。
- Reason: 远端任务在手工步等待时也不能被 workspace 选择或实例边界藏掉。
- Impact: 扩展 `cross-instance-inbox` 组合契约。

**规格与文档对齐（Task 05）**
- From: 跨实例通知 spec、instances AGENTS、公开多实例指南仍描述「侧边栏专用模块 + relayed 静默」；设计包与部分文档仍用旧编号 002–004。
- To: 以 spec delta 修订 `cross-instance-notifications`；更新 `apps/backend/internal/instances/AGENTS.md` 与 `docs/public/multi-instance.md`；把 docs/specs 草稿提升为 active/current；全仓统一为 REQ-UI-NEEDS-YOU-INBOX-003/004/005，不引入 -002。
- Reason: 避免实现与持久契约继续分叉。

## Capabilities

### New Capabilities

（无新增 OpenSpec capability。Inbox 行集与统一读的产品契约由 `docs/specs/ui/requirements/needs-you-inbox-waiting-rows.md` 的 REQ-UI-NEEDS-YOU-INBOX-003/004/005 持有，Task 05 负责提升；按 ADR `2026-10-28-multi-instance-federation` 的边界，`openspec/specs` 不重复承载 UI 产品契约。）

### Modified Capabilities

- `cross-instance-notifications`: relayed set 升级为 v2 并增加 `task.manual_step_entered`；relayed occurrence 携带 origin 进入 shared notification pipeline；移除侧边栏通知模块 requirement；提醒方式从「relayed 静默」改为「与本地同一逻辑」；relay store 降级为去重与恢复账本。
- `cross-instance-inbox`: 每实例组合新增远端手工步等待行与对应降级态；question 行与 sidecar 转发契约不变。

需求映射：REQ-UI-NEEDS-YOU-INBOX-003 的本地等待行与 episode 通知契约，以及 -005 的本地合并读与计数契约，由 `docs/specs/ui/requirements/needs-you-inbox-waiting-rows.md` 持有（Task 05 提升为 active）；其中远端等待行部分由 `cross-instance-inbox` delta 承接，通知统一与 relay 契约部分由 `cross-instance-notifications` delta 承接。REQ-UI-NEEDS-YOU-INBOX-004 的本地与远端统一通知规则由 `cross-instance-notifications` delta 完整承接。按 ADR `2026-10-28-multi-instance-federation` 的边界，不在 `openspec/specs` 新建 `needs-you-inbox` capability，避免与 `docs/specs/ui` 的产品契约形成第二份事实来源。

## Research Log

本节结论来自本 change 的 Explore 步骤（只做只读核对，不重复设计包内容），用于纠正设计包与当前代码的 5 处偏差，Plan/Apply 必须按此实现。

### 未知 1：手工步 episode 是否只在设计点名的两处开始
- **验证方式**：读 `apps/backend/internal/orchestrator/event_handlers_workflow.go`，grep `publishSessionWaitingEvent` 全部调用点。
- **结论（试通证据）**：不是。`publishSessionWaitingEvent` 有 11 个调用点（4591/4705/4721/4744/4811/4855/4864/4878/4932/4949/7339）。设计点名的 `:4580` 只是 `len(step.Events.OnEnter)==0 && !sessionSwitched` 分支；仅含 `reset_agent_context` 等非启动 on_enter 的手工步走 `launchAfterOnEnterDispatch`，同样满足 `StepRunsOnEntry=false` 且 `CompleteTaskOnEnter=false`。只在两处发事件会造成「Inbox 有等待行但永不通知」，违反 AC-003.4。emitter 应收敛到 WAITING_FOR_INPUT 的收口（建议 `publishSessionWaitingEvent` 或其封装），以步骤语义与非本人移动为闸门，并用 episode id 去重。
- **证据摘要**：`event_handlers_workflow.go:4591,4705,4721,4744,4811,4855,4864,4878,4932,4949,7339`；`StepRunsOnEntry` 在 `workflow/models/step_semantics.go:7`；`CompleteTaskOnEnter` 在 `workflow/models/models.go:223`。

### 未知 2：episode 身份能否取 design 指定的审计行
- **验证方式**：读 `task/service/service_workflow.go:1608` 的 `recordManualStepTransition` 与 workflow service 的 `CreateStepTransition`/`CreateHistory`。
- **结论（试通证据）**：不能作为唯一来源。该路径写的是 `session_step_history`，`sessionID == ""` 时直接跳过，且是异步、尽力而为的审计；无 session 的移动永远没有记录（违反 AC-003.1），异步丢写还会让行不稳定。durable 来源是 `task_step_transitions`：它在每次移动的同一事务写入，`session_id` 可空，含 `occurred_at`、`trigger`、`actor_kind`、`actor_id`。当前 `models.StepTransition` 与 `ListTaskStepTransitions` 未 select actor 列，需要暴露（列已存在，无需 schema 变更）。建议行身份 `mw:<task>:<step>:<occurred_at|transition_id>` 取自此表。
- **证据摘要**：`task/repository/sqlite/step_transitions.go:180` 的 INSERT 列；`task/models/step_transitions.go:6` 的读取模型；`TaskMovedEventData.StepTransitionID` 携带同一 id（`orchestrator/watcher/watcher.go:134-147`）。

### 未知 3：无 session 的任务是否会被等待行漏掉
- **验证方式**：读设计要求与 `handleTaskMovedNoSession`（`event_handlers_workflow.go:989`）。
- **结论（试通证据）**：设计要求「joining the active session」；AC-003.1 不要求 session，且无 session 移动不会创建 session。读取查询必须用 LEFT JOIN 或不依赖 session，只用「无执行中/排队 turn」过滤。执行中可由 `task_sessions.state` ∈ {STARTING,RUNNING} 判断；排队 turn 可由 `queued_messages` 表（messagequeue repository，同 DB）与 `tasks.queued_for_step_id` 判断，Plan 需明确谓词与读取边界。
- **证据摘要**：`event_handlers_workflow.go:986-991`；`task/models/models.go:2347-2362`；`orchestrator/messagequeue/repository_sqlite.go:626`。

### 未知 4：relay 事件能否直接复用现有通知入口
- **验证方式**：读 `notifications/service/service.go` 的 `handleSemanticOccurrence` 与 `recipients`。
- **结论（试通证据）**：不能。`recipients` 通过 `workspaceOwnerForTask(taskID)` 查本地 task/workspace；远端 task_id 在本地不存在，返回 `ok=false`，事件被直接丢弃。必须提供显式收件人（relay 已知 `inst.OwnerUserID`）的新入口。同时，外来 occurrence 不应写进本地 occurrence 历史（`/api/v1/notifications/occurrences` 是对端 relay 的恢复来源），否则双向联邦会回灌，违反 AC-004.5 的「replay/reconciliation 不重复通知」。relay store 继续是唯一去重账本。
- **证据摘要**：`notifications/service/service.go:239-261,322-368`；`relay/relay.go:185-207`。

### 未知 5：通知 wire 是否已有 origin 与手工步事件位
- **验证方式**：读 `notifications/providers/local.go`、`notifications/service/service.go` 的 `AvailableEvents`/`ensureDefaultProviders`、前端 `notification-events-table.tsx`。
- **结论（试通证据）**：没有。Local provider 的 payload 是固定字段白名单，`notificationPayload` 无 origin；`AvailableEvents` 只有 4 种事件；默认 Local/System provider 订阅 clarification/Office/update，不含 `turn_finished`。需要扩展 payload/类型以携带 origin；`task.manual_step_entered` 要加入 `AvailableEvents`、默认订阅创建清单，并对既有默认 provider 幂等补齐订阅（只改创建清单只覆盖新用户，现有安装仍收不到）。设置页 `EVENT_MESSAGE_KEYS` 需新增该事件的标题/描述（7 locale），否则回落显示原始 wire 字符串。
- **证据摘要**：`providers/local.go:21-56`；`service.go:110-112,559-623`；`apps/web/components/settings/notification-events-table.tsx:31-48`。

### 未知 6：远端组合是服务端聚合还是 SPA 侧组合
- **验证方式**：读 `use-remote-inbox-controller.ts`、`remote-inbox-api.ts`、`backendapp/instance_inbox_routes.go`、`instances/remoteinbox/client.go`。
- **结论（试通证据）**：SPA 侧组合。前端按实例读本地后端 `/api/v1/instances/:id/clarification-inbox`；本地后端再按远端 workspace fan-out 读远端 `/api/v1/clarification-inbox?workspace_id=` 并聚合。设计里「本地聚合响应附带远端分页」的措辞不精确。Task 04 的「把聚合 needs-you 端点作为远端 API surface，沿用每实例组合」与 landed 机制一致；AC-005.1 的 one read 应实现为「本地合并一次读 + 每实例一次读」，count/截断仍各自来自单次响应。
- **证据摘要**：`use-remote-inbox-controller.ts:33-117`；`remote-inbox-api.ts:1-46`；`backendapp/instance_inbox_routes.go:72-93`；`instances/remoteinbox/client.go:101-132`。

### 未知 7：规范与文档现状
- **验证方式**：读 `openspec/specs/cross-instance-notifications/spec.md`、`apps/backend/internal/instances/AGENTS.md`、`docs/public/multi-instance.md`、ADR。
- **结论（试通证据）**：现 OpenSpec spec 明文要求 relayed set v1 三事件、侧边栏通知模块、relayed 通知「SHALL NOT trigger toasts, browser native notifications, or sound」（`:25-26,40-56`），与本 change 的 AC-004.1/.5 正面冲突，必须由本 change 的 delta 修订。`instances/AGENTS.md:44` 与 `docs/public/multi-instance.md:51-52` 同。`cross-instance-notifications/spec.md:58` 与 ADR `:30,:53` 仍写旧编号 002–004，Task 05 改为 003–005；-002 属于 `cross-instance-inbox.md`。
- **证据摘要**：上述文件行号。

### 未知 8：通知的「打开后导航」在桌面端是否可行
- **验证方式**：读前端通知 handler 与 `apps/desktop/src-tauri/Cargo.toml`；查 `tauri-plugin-notification` 2.3.3 的 API 文档与 2.6.0 changelog。
- **结论（拒绝理由 + 用户决策）**：当前前端没有通知点击/打开路径；Tauri 插件 2.3.3 不会把桌面通知点击回传给应用，该能力要到 2.6.0 才提供 `on_action`。用户已选择「Inbox rows only」：不新增通知打开动作，也不升级桌面插件；远端任务只能通过打开 Inbox 行到达，信息型 relayed 通知保持 push-only。据此收窄 AC-004.3，并同步修订需求文档、系统设计与 spec delta。
- **证据摘要**：`apps/web/lib/ws/handlers/notifications.ts:47-50`；`apps/desktop/src-tauri/Cargo.toml:53`（`tauri-plugin-notification =2.3.3`）；Tauri notification changelog 的 2.6.0 条目（desktop notification click/action reaches `on_action`）。

## Impact

- **后端**：`orchestrator/event_handlers_workflow.go`（episode emitter 收敛与自我移动抑制）、`notifications/service/service.go`（新事件、显式收件人的 relayed 入口、默认订阅补齐）、`instances/relay/relay.go`（allowed set v2、origin 投递、去重后不广播专用 action）、`backendapp/gateway.go`（bus 订阅）、`clarification/handlers.go` + 新聚合 handler（needs-you-inbox、hidden、boot summary）、`task/repository/sqlite`（派生 waiting 查询、sidecar 复用、`StepTransition` 读取模型补 actor）、`backendapp/boot_state_routes.go`、`instances/remoteinbox` 与 `backendapp/instance_inbox_routes.go`（远端聚合读转发）。
- **前端**：`app/needs-you-inbox/*`（三类行、菜单、空态/错误态/降级）、`lib/state/slices/needs-you-inbox/*`、`hooks/domains/needs-you-inbox/*`、`lib/api/domains/*`、`lib/ws/handlers/notifications.ts`、`lib/notifications/events.ts`、`components/settings/notification-events-table.tsx`；移除 `components/app-sidebar/sections/notifications-section.tsx`（含 `app-sidebar.tsx` 与 `app-nav-sheet.tsx` 的挂载）、`lib/state/slices/remote-notifications/`、`hooks/domains/remote-notifications/`、`lib/api/domains/remote-notifications-api.ts` 与对应 REST 端点。
- **数据**：不新增表，不新增本地投影；等待行身份复用 `clarification_inbox_sidecar.pending_id`（`mw:` 前缀）；`task_step_transitions` 的 `actor_kind`/`actor_id` 列已存在，只扩展读取模型。远端行仍不落本地任何表。
- **接口**：新增 `GET /api/v1/needs-you-inbox`、`GET /api/v1/needs-you-inbox/hidden`；新增远端转发 `GET /api/v1/instances/:id/needs-you-inbox`（+hidden）；既有 `/api/v1/clarification-inbox` 保持给 history 与远端转发契约。
- **配置**：不新增 runtime flag；`features.NeedsYouInbox` 与 `features.multiInstance` 的默认值不变。
- **测试与文档**：按设计包 Tests 表新增/扩展后端、前端、E2E；i18n 7 locale + pseudo，zh-hant 用 `pnpm run i18n:zh-hant`；Task 05 修订 `cross-instance-notifications`、instances AGENTS、公开多实例指南、ADR 修订段与 docs/specs 提升。
- **风险**：既有默认 provider 订阅补齐若遗漏，现有安装（含本次动机中的每周 routine 任务）收不到通知；relayed occurrence 若写入本地历史会造成联邦回灌；无 session 与「queued turn」谓词若定义不清会出现漏行/多行；侧边栏模块删除必须完全位于 `features.multiInstance` 之后，保证 flag-off 面板与今天一致。
