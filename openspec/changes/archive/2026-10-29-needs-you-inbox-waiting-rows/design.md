# Design: Needs-you Inbox Waiting Rows and Unified Notification

## Context

Needs-you Inbox 目前只有一种行：clarification bundle。任务进入手工工作流步骤（`StepRunsOnEntry` 为 false 且 `CompleteTaskOnEnter` 为 false）后，既不产生通知，也不产生 Inbox 行，操作者只能看看板。跨实例侧，relay 把远端 occurrence 只广播给侧边栏专用模块，并被规定为静默；本地事件则按通知设置的订阅规则推送。两套逻辑并存。

landed 的 remote-questions change 已提供每实例远端组合：SPA 按实例读本地后端 `/api/v1/instances/:id/clarification-inbox`，本地后端用 `internal/instances/remoteinbox` 按远端 workspace fan-out 读远端自己的 clarification-inbox，聚合成一页，sidecar 写回远端，按实例降级。本 change 在其上增加等待行与统一通知，不改 question 契约。

约束：
- 计数与列表必须来自同一读（REQ-005）。
- 远端行不落本地 task 表、board cache 或 sidecar 表（ADR `2026-10-28-multi-instance-federation`）。
- `features.NeedsYouInbox` 与 `features.multiInstance` 不新增、不改默认值。
- 新用户可见文案走 `t()`，7 个 locale + pseudo；不使用 Unicode em dash。
- 每个新逻辑都有测试；后端测试优先包内 `*_test.go`，前端用 `*.test.ts(x)`，跨层路径用 Playwright E2E。

## Goals / Non-Goals

**Goals:**

- 本地手工步等待行：派生读、一个 episode 一行、dismiss/snooze/restore 复用 per-user sidecar、合并 question 与 waiting 的 count/list/hidden/truncation。
- `task.manual_step_entered` 语义事件：与 clarification 同一通知待遇，本人移动不推送，投递与 Inbox flag 无关；既有默认 provider 幂等补齐订阅。
- 远端与本地一套通知逻辑：relay occurrence 带 origin 进 shared pipeline，relayed set v2，侧边栏专用模块与 REST 移除，relay store 只做去重与恢复。
- 远端手工步等待行：复用 landed 每实例组合，live 解析，按实例降级。
- 规格与文档对齐，编号统一为 REQ-UI-NEEDS-YOU-INBOX-003/004/005。

**Non-Goals:**

- 远端 clarification 行契约（读取、就地回答、sidecar 转发、workspace 映射、降级）的任何改动。
- Office inbox items 进入 needs-you Inbox；Threads needs-action 分类器改动（只加差异测试）。
- 新 runtime flag、profile 默认值改动。
- 通知打开动作与 `tauri-plugin-notification` 升级（用户已选择 Inbox rows only；见 D8）。
- waiting/remote 行的 history 条目；首次启用时的通知回填。
- 任何本地投影表，或把远端行写入本地表。

## Research Log

本阶段不重复 Explore 的代码核对，以下为影响 design 的结论摘要；完整证据见 `proposal.md` 的 Research Log 与 Explore step summary。

### 未知 1：手工步 episode 的真实起点

- **验证方式**：读 `apps/backend/internal/orchestrator/event_handlers_workflow.go`，枚举 `publishSessionWaitingEvent` 调用点。
- **结论（试通证据）**：共 11 个调用点（4591/4705/4721/4744/4811/4855/4864/4878/4932/4949/7339）。`:4580` 只是 `len(step.Events.OnEnter)==0 && !sessionSwitched` 分支；仅含 `reset_agent_context` 等非启动 on_enter 的手工步走 `launchAfterOnEnterDispatch`。emitter 必须收敛到 WAITING_FOR_INPUT 收口，而不是两处。
- **证据摘要**：`event_handlers_workflow.go:4591,4705,...`；`StepRunsOnEntry` 在 `workflow/models/step_semantics.go:7`。

### 未知 2：episode 身份的 durable 来源

- **验证方式**：读 `recordManualStepTransition`（`task/service/service_workflow.go:1608`）与 workflow service 的 `CreateStepTransition`/`CreateHistory`。
- **结论（试通证据）**：`session_step_history` 是异步、尽力而为的审计，`sessionID == ""` 时跳过，不能作为唯一来源。`task_step_transitions` 在每次移动的同一事务写入，`session_id` 可空，含 `occurred_at`、`trigger`、`actor_kind`、`actor_id`；读取模型需要补 actor 列（列已存在，无 schema 变更）。
- **证据摘要**：`task/repository/sqlite/step_transitions.go:180`；`task/models/step_transitions.go:6`。

### 未知 3：无 session 任务与 queued turn

- **验证方式**：读 `handleTaskMovedNoSession`、`task_sessions.state`、`queued_messages` schema。
- **结论（试通证据）**：无 session 移动不会创建 session；等待读必须 LEFT JOIN 或不依赖 session。"执行中" 用 `task_sessions.state IN ('STARTING','RUNNING')`；"排队 turn" 用 `queued_messages.task_id`；两者都与 task repo 同库，后者已被跨 store join 的先例覆盖（`workflow_steps`）。
- **证据摘要**：`event_handlers_workflow.go:986-991`；`task/models/models.go:2347-2362`；`orchestrator/messagequeue/repository_sqlite.go:626`。

### 未知 4：relay 事件的收件人解析

- **验证方式**：读 `notifications/service/service.go` 的 `handleSemanticOccurrence`/`recipients`。
- **结论（试通证据）**：`recipients` 通过本地 `workspaceOwnerForTask(taskID)` 解析，远端 task_id 在本地不存在会直接丢弃。relay 必须使用显式收件人（`inst.OwnerUserID`），且不写本地 occurrence 历史，避免双向联邦回灌。
- **证据摘要**：`notifications/service/service.go:239-261,359-368`。

### 未知 5：origin 与默认订阅

- **验证方式**：读 Local provider payload、`AvailableEvents`、`ensureDefaultProviders`、通知设置表。
- **结论（试通证据）**：Local provider payload 无 origin；`AvailableEvents` 只有 4 种；默认 Local/System provider 订阅澄清/Office/update。需要新增事件、扩展 payload、补设置页标签（7 locale），并用现有 `notification_migrations` 模式给既有默认 provider 一次性补订阅。
- **证据摘要**：`providers/local.go:21-56`；`service.go:110-112,559-623`；`notifications/store/sqlite.go:136-188`；`notification-events-table.tsx:31-48`。

### 未知 6：远端组合的实际形态

- **验证方式**：读 `use-remote-inbox-controller.ts`、`remote-inbox-api.ts`、`backendapp/instance_inbox_routes.go`、`remoteinbox/client.go`。
- **结论（试通证据）**：SPA 侧组合，本地后端 fan-out。设计里「本地聚合响应附带远端分页」的措辞不精确；远端等待行应通过新的远端聚合端点，由同一每实例组合读取。
- **证据摘要**：`use-remote-inbox-controller.ts:33-117`；`remote-inbox-api.ts:1-46`；`instance_inbox_routes.go:72-93`；`remoteinbox/client.go:101-132`。

### 未知 7：通知的打开动作与桌面插件

- **验证方式**：读前端通知 handler、`apps/desktop/src-tauri/Cargo.toml`；查 `tauri-plugin-notification` 2.3.3 与 2.6.0 changelog。
- **结论（拒绝理由 + 用户决策）**：当前前端没有打开路径；插件 2.3.3 不支持桌面通知点击回调，2.6.0 才提供 `on_action`。用户选择 Inbox rows only：不新增通知打开动作，不升级插件；AC-004.3 据此收窄。需求文档、系统设计与 spec delta 已同步修订。
- **证据摘要**：`apps/web/lib/ws/handlers/notifications.ts:47-50`；`Cargo.toml:53`；Tauri notification changelog 2.6.0。

## Decisions

### D1：episode emitter 收敛到 WAITING_FOR_INPUT 收口

- **选择**：在 orchestrator 新增 `manualStepWaitNotifier`（新文件 `apps/backend/internal/orchestrator/manual_step_wait.go`）。在 `publishSessionWaitingEvent` 内调用一次，另在无 session 的入口（`handleTaskMovedNoSession`/`autoStartTaskForStep` 确认未被队列拦住）调用一次。闸门：步骤可由 `StepRunsOnEntry` 与 `CompleteTaskOnEnter` 判定为手工步；当前 episode 未通知过（由 occurrence id 去重）。
- **理由**：11 个等待收敛点共享同一语义，逐点插桩易漏；move 时发事件对 queued move 太早。
- **已考虑 alternative**：逐点插桩（易漏、难测）；在 move service 发事件（queued move 尚未真正进入）；在 `session_step_history` 变化时发（异步、无 session 时缺失）。

### D2：episode 身份用 `task_step_transitions`

- **选择**：`mw:<taskID>:<stepID>:<transitionID>`，取 `task_step_transitions` 中 `to_workflow_step_id = 当前步骤` 的最新一行；无行时回退 `mw:<taskID>:<stepID>:0` 并 WARN。`StepTransition` 读取模型增加 `ActorKind`、`ActorID`，`ListTaskStepTransitions` 同步 select。
- **理由**：该行与移动同事务、覆盖 sessionless、不可变且天然区分重入；`moved_at` 的意图（episode 区分）由不可变 id 更稳地实现。
- **已考虑 alternative**：`session_step_history.created_at`（异步/缺失）；`tasks.updated_at`（会被任意更新改变，重入无法区分）。

### D3：无 session 任务与队列谓词

- **选择**：等待读不要求 session。执行中：`NOT EXISTS task_sessions state IN ('STARTING','RUNNING')`；排队：`NOT EXISTS queued_messages WHERE task_id = t.id`。不把 `queued_for_step_id` 当作唯一依据，因为普通 queued prompt 也可能存在。
- **理由**：满足 AC-003.1 的「无执行中或排队 turn」，同时覆盖无 session 的手工步任务。
- **已考虑 alternative**：JOIN 活跃 session（漏掉无 session 任务）；只看 `queued_for_step_id`（漏掉 queued prompt）。

### D4：手工步判定只在 Go 内做一次

- **选择**：SQL 先按 workspace、非终态、`complete_task_on_enter = 0`、无执行中/排队 turn 收窄；取有界 overscan（`limit*4+1`，上限 `maxInboxLimit` 的倍数），在 Go 内 unmarshal `workflow_steps.events` 并调用 `wfmodels.StepRunsOnEntry`，过滤后再截断到 limit。`HasMore` 由过滤后是否仍有候选或 overscan 触顶决定。
- **理由**：谓词只有一个定义；避免在 SQL 里复制 `on_enter` action 列表。
- **已考虑 alternative**：SQL `LIKE '%auto_start_agent%'`（会误判、与模型漂移）；新增规范化 action 表（超出本 change）。

### D5：合并读与 sidecar

- **选择**：新 repository 方法 `ListManualStepWaits`（`needs_you_waiting_query.go`）返回 waiting 行与 hidden 摘要；新 handler `needs_you_inbox_handlers.go` 以同一 limit 取 bundle 页与 waiting 页，按 `(reference_time, id)` 合并，count = 合并后行数，hidden_count = 两类隐藏数之和，has_more = 任一源截断或合并溢出。sidecar 以 `mw:` id 复用现有 `clarification_inbox_sidecar` 方法。`InboxBootSummary` 扩展为调用同一个 merge helper。
- **理由**：保证 count/list 同源；sidecar 不新增表。
- **已考虑 alternative**：前端分别读两类再合并（count 与 list 不同源，违反 REQ-005）；新建 waiting sidecar 表（重复且破坏 per-user 语义）。

### D6：relay 使用显式收件人，且不写本地 occurrence 历史

- **选择**：relay 在 store 去重成功后调用 notification service 的新入口 `HandleRelayedOccurrence(ctx, userID, originInstanceID, originInstanceName, eventType, taskID, sessionID, occurrenceID, title, body)`。该入口只用 `deliverOccurrence`，不调用 `recordOccurrence`；收件人固定为实例 owner。
- **理由**：本地 task→owner 解析对远端 id 必然失败；写本地历史会让对端 relay 再次看到并回灌。
- **已考虑 alternative**：给 `recipients` 加 remote task 映射（引入本地投影，违反数据隔离）；记录到 occurrence 并加 origin 排除（仍污染本地历史，且需要额外迁移）。

### D7：origin 以 payload 字段贯穿

- **选择**：`notificationPayload`、`providers.Message`、Local provider payload 增加 `origin_instance_id`/`origin_instance_name`；System provider 在 origin 存在时把实例名并入标题/正文。前端 handler 用 origin 做标签与查看抑制。
- **理由**：不改 wire 结构，只在现有 notification payload 上增量；浏览器与桌面原生通知都能显示实例名。
- **已考虑 alternative**：新增独立 origin WS 通道（重复传输）；只在前端按 occurrence 反查实例（需要额外状态）。

### D8：通知不提供打开动作，导航只经 Inbox 行

- **选择**：不新增 `Notification.onclick` 或 Tauri `on_action`；桌面插件保持 2.3.3。relayed needs-you 任务通过打开 Inbox 行导航；信息型 relayed 通知没有 Inbox 行，只能手动切换实例。AC-004.3 已按用户决定修订。
- **理由**：当前没有通知打开路径；桌面点击回调需要升级到 `tauri-plugin-notification >= 2.6.0`，用户选择不扩大依赖与平台验证范围。
- **已考虑 alternative**：升级插件并接 `on_action`（完整满足旧 AC-004.3，但增加桌面构建/E2E 与跨平台语义）；新增可点击 in-app toast（会改变本地通知行为，违反 AC-004.6）。

### D9：默认 provider 订阅用一次性迁移补齐

- **选择**：沿用 `notification_migrations` 模式，新增 `manualStepWaitSubscriptionMigration`，在 `initSchema` 中为 type 为 local/system 且缺少 `task.manual_step_entered` 订阅的 provider 插入订阅；迁移标记保证只跑一次，用户之后取消订阅不会被重新加回。
- **理由**：只改 `ensureDefaultProviders` 的创建列表只覆盖新用户；现有安装（含动机中的每周 routine）收不到通知。
- **已考虑 alternative**：每次 `ListProviders` 幂等补齐（用户取消后被反复加回）；只在启动时无条件 INSERT（重复/冲突）。

### D10：远端等待行复用 landed 组合

- **选择**：把本地聚合端点作为远端 API surface：远端暴露 `GET /api/v1/needs-you-inbox?workspace_id=...`（+`/hidden`）；本地 `remoteinbox.Client` 新增 `ReadNeedsYou`/`ReadNeedsYouHidden`，本地转发路由新增 `/api/v1/instances/:id/needs-you-inbox`（+hidden）；SPA 的 `use-remote-inbox-controller` 改读新转发端点。404 映射为 `ReasonInboxDisabled`，401/403 为 unauthorized，传输失败为 unreachable。
- **理由**：与 landed 的每实例组合、limit、降级和 workspace 映射一致；旧版本远端自然降级。
- **已考虑 alternative**：本地后端服务端聚合所有远端（改变 landed 的 SPA 组合架构，且把远端故障耦合进本地读）；在 clarification-inbox 端点里混入 waiting 行（破坏 landed question 契约与 history 语义）。

### D11：模块移除完全位于 `features.multiInstance` 之后

- **选择**：删除 `notifications-section.tsx` 及其在 `app-sidebar.tsx`、`app-nav-sheet.tsx` 的挂载、`remote-notifications` slice/hook/API、`remote_notification_routes.go` 与注册；relay store 的 read/dismiss 列保留但不再有展示消费方。所有删除路径当前都只在 multiInstance 开启时可见，删除前用测试/搜索确认。
- **理由**：flag-off 面板必须与今天完全一致（AC-004.6）。
- **已考虑 alternative**：保留模块但置空（仍存在远端专用展示路径，违反 AC-004.5）。

### D12：前端 Inbox 行类型从 bundle 扩展为 union

- **选择**：`needs-you-inbox` slice 的 `bundles` 改为 `rows: NeedsYouInboxRow[]`，`NeedsYouInboxRow = QuestionRow | WaitingRow`，用 `kind` 区分；remote state 同样改 `rows`。question 行保持可展开回答，waiting 行只提供打开任务与 dismiss/snooze 菜单。API 客户端新增 `needs-you-inbox-api.ts`。
- **理由**：一个列表/计数承载两类行；避免 `bundles` 名称与实际内容不符。
- **已考虑 alternative**：保留 `bundles` 并在旁边加 `waiting`（两个列表/两个 count 来源，易漂移）。

## Risks / Trade-offs

- [Risk] 既有默认 provider 迁移遗漏或重复 → Mitigation：迁移标记 + `(provider_id, event_type)` 唯一约束；测试覆盖「已有 provider 补一次」「用户取消后不再补」。
- [Risk] relayed occurrence 写入本地历史导致双向回灌 → Mitigation：`HandleRelayedOccurrence` 不调用 `recordOccurrence`；relay 测试断言本地 occurrence 表无新行。
- [Risk] `queued_messages` 是 queue-owned 表，join 可能跨 store 边界 → Mitigation：与 `workflow_steps` 相同的同库 join 先例；查询限定 `task_id` 并走索引；如后续拆库，改为 handler 层批量 count。
- [Risk] Go 内过滤导致 SQL overscan 不够，漏行 → Mitigation：overscan 有界且 `HasMore` 反映截断；测试构造全部为启动步骤的候选，断言结果为空且不误报。
- [Risk] 删除侧边栏模块影响 flag-off 面板 → Mitigation：删除前搜索所有挂载点，保持 multiInstance 闸门；E2E 断言 flag-off 无该模块且行为不变。
- [Risk] 远端旧版本没有聚合端点 → Mitigation：404 映射为 per-instance degraded，不失败本地读；多实例 E2E 覆盖。
- [Risk] `bundles` → `rows` 的前端重命名面较大 → Mitigation：类型 union + 逐组件迁移；测试覆盖 question 行行为不变。
- [Trade-off] AC-004.3 收窄为 Inbox-only 导航 → 接受理由：不升级桌面通知插件、不改变本地通知行为；用户已显式选择。
- [Trade-off] 手工步等待行按 read 派生，15 分钟/60 秒刷新窗口内可能短暂滞后 → 接受理由：AC-003.2 要求通过 re-read 收敛，不要求零延迟事件。

## Migration Plan

1. 后端先落：`task_step_transitions` 读取模型补 actor（无 schema 变更）、notification service 新事件 + 迁移、relay 新入口、聚合端点、远端转发、boot summary。
2. 前端随后：slice/controller/API 切到聚合端点、行 union、通知 handler origin、模块删除、i18n。
3. 远端等待行依赖远端运行本版本；旧远端自然降级，无需协调升级顺序。
4. Rollback：关闭 `features.needsYouInbox` 即回到只有 clarification 的 Inbox；关闭 `features.multiInstance` 即无 relay 与远端行。通知迁移是追加订阅，无破坏性 schema 变更；relay store 列保留，回滚不需要数据修复。
5. 验收：`openspec validate --strict`、后端包测试、前端 vitest/typecheck/i18n、E2E waiting rows 与 multi-instance、`list-docs.py validate`、`lint-spec-files.py --all`。

## Open Questions

无。D8 的导航范围已由用户决定；`toast` 措辞已修正为浏览器/桌面原生通知；queued turn 谓词、远端 workspace 映射均已在上文定案。
