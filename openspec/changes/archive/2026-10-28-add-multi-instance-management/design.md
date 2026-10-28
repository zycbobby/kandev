# design.md — add-multi-instance-management

> 实现入口：本文件 + `proposal.md` + `specs/multi-instance-management/spec.md` + `specs/cross-instance-notifications/spec.md`。任务拆分见 `tasks.md`。

## 目标与范围

单一面板管理多个 Kandev 实例（直接操作远端任务）+ 跨实例通知（仅侧边栏模块 + 未读角标）。架构、用户决策、边界见 proposal；本文件定义 HOW，并修正一处 proposal 表述：**远端零"配置"改动，但需要运行 ≥ 本 change 发布的版本**（对账 API 是本 change 新增的，见 D5）。

## 架构总览

```text
浏览器（单一 SPA，由本机 Go shell 服务）
  │  HTML shell：?kandev_instance= / cookie → 注入 window.__KANDEV_API_PORT=<代理端口> + 标题重写
  │  API/WS base = http(s)://<本机host>:<代理端口>   （现有 getBackendConfig/ws-connector 机制，零改动）
  ▼
本机后端（LOCAL）
  ├─ InstanceRegistry（SQLite，per-owner）── HealthPoller（healthpoll 模式，90s）
  ├─ ProxyServer（每启用实例一个本机端口，同主机名）
  │     HTTP：反向代理 <remote>/api/v1/**，注入 PAT；WS：/ws 每连接双向桥接
  ├─ RemoteEventRelay（每启用实例一条持久 WS 客户端 + 对账轮询）
  │     收 remote "notification" 帧 → occurrences 表（(instance,event,occurrence) 唯一键）
  │     → 新 occurrence 时 hub.BroadcastToUser(owner, "remote_notification")
  └─ REST：/api/v1/instances（CRUD+select 设 cookie）、/api/v1/remote-notifications（列表/已读）
                │
                ▼ （ProxyServer 转发）
远端实例（REMOTE，3090Ti）：现有全部 API/WS + 本 change 新增 GET /api/v1/notifications/occurrences
```

## 关键设计决策

- **D1 代理形态 = 每实例本机监听端口**（同主机名）。`httpmw.AllowedOrigin` 仅比较主机名（`origin.go:39-45`），同主机名不同端口零策略放宽；SPA 全部 API 调用经 `resolveUrl(path, apiBaseUrl)`（`api/client.ts`）+ WS 经 `ws-connector.tsx:15-16`，base 指向代理端口即全局生效。路径前缀方案需改所有 API 路径构造，弃。
- **D2 远端 boot = shell 注入端口全局 + 省略注入 boot payload**。`webapp/handler.go:20` 的 `PayloadBuilder` 按请求构建：读到实例选择（cookie `kandev_instance` 或 query `?kandev_instance=`，query 优先）时，注入 `window.__KANDEV_API_PORT = <proxyPort>`（`config.ts:36` 现有读取）并以实例名重写 `<title>`（`shell.go:66` ComposeTitle 现有机制）；**不注入** `__KANDEV_BOOT_PAYLOAD__`，令 `loadBootPayload`（`boot-payload.ts:274-290`）走既有 app-state 回退路径，从代理端口取远端完整 boot 态。两个机制均为现有代码路径的 verbatim 复用。
- **D3 切换 = 本地 REST + 整页重载**。`POST /api/v1/instances/:id/select`（本地后端写 cookie 并返回目标 href）→ `window.location.assign(href)`。直达任务：`/t/:taskId?kandev_instance=<id>`（shell 对 SPA 路径照常服务）。单活动实例：SPA store 任何时刻只持一个实例的活跃态；刷新（F5）因 cookie 保持当前实例。
- **D4 v1 不做 payload URL 重写**。远端响应中的绝对 URL（share 链接等）视为"跨实例深链到远端面板"，新标签打开即可；宿主绑定面（会话 port-proxy/端口预览、桌面特权动作）按 spec 降级为不可用说明。跨实例任意 TCP 端口代理为非目标。此决策移除逐端点 body 重写的高风险面。
- **D5 事件源 = 远端通知帧 + 本 change 新增的对账端点**。实时：远端 `LocalProvider` 经 `hub.BroadcastToUser(userID)` 推通知帧，帧的 `action` **就是语义事件名**（`ws.NewNotification(message.EventType, payload)`，`providers/local.go:58`；`message.go:30` 的 `"notification"` 是 MessageType 而非 action），`msg.ID` = occurrence ID，帧含 task_id/session_id/title/body。中继以远端用户身份（PAT 或无认证）持 WS 连接，按 action 封闭集过滤（v1：`session.turn_finished`/`session.clarification_requested`/`office.inbox_item`）。对账：远端现有 REST 无历史 occurrence 面 → 本 change 在 `internal/notifications` 新增 dispatch 时落库的 `notification_occurrences` 表（有界留存）+ `GET /api/v1/notifications/occurrences?since=&limit=`（per-user，上限 200）。**远端版本下限 = 本 change 版本**，健康探测读远端 `/api/v1/system/info` 的版本比对，不满足给版本不匹配错误。
- **D6 去重与身份**。本地 `remote_notification_occurrences` 表唯一键 `(instance_id, event, occurrence_id)`；中继只在"新 occurrence"时广播。中继消费语义 occurrence 边界（远端已 dispatch 的事件），parked 延迟语义天然继承，不重造。
- **D7 所有权模型**。`Instance.OwnerUserID`（per-owner，对齐 notification providers 模式）：设置路由按 owner 鉴权；中继事件 `BroadcastToUser(owner)`；本地 `remote_notification_occurrences` 行带 owner；桥接在 auth 开启时校验本地会话 + 实例所有权。auth 关闭时合成 admin 拥有一切（用户现状）。
- **D8 relay/proxy 生命周期**。每实例 ProxyServer 与 Relay 各自 `Start(ctx)/Stop()` + `sync.WaitGroup`（backend AGENTS goroutine 约定），包级 `goleak.VerifyTestMain`；重连退避用 `time.NewTimer`+`select`（禁 `time.Sleep`）；对账轮询同门。启停随实例 Enabled 变更与后端关闭联动。
- **D9 前端通知面只加不改**。新 WS action `remote_notification` → 新 handler `lib/ws/handlers/remote-notifications.ts` 只写新 slice；不触碰现有 `notifications.ts` handler、sonner、浏览器通知、声音（本地实例行为不变的硬约束）。
- **D10 feature toggle**。`features.multiInstance`（env `KANDEV_FEATURES_MULTI_INSTANCE`）：`internal/runtimeflags/registry.go` 注册 + 根 `profiles.yaml` 全部 shipped profile 默认 `false`。关闭时：无注册表 UI/路由、不开代理端口、不起 relay、shell 不读实例参数——行为零差异（含测试断言）。
- **D11 端口分配**。ProxyServer 监听 `127.0.0.1`？——否：监听与本地 UI 同主机名的所有接口（`net.Listen("tcp", ":0")` 动态分配），端口持久化到 `instances.proxy_port` 列，重启优先重绑；被占则重分配并回写。
- **D12 通知提醒限定**。远端事件只进侧边栏模块 + 角标（用户拍板）；本地事件链路 byte 级不动。

## 文件结构（File map）

**后端（新增 `internal/instances/`，修改 4 处）**

```text
internal/instances/
├── models.go          # Instance、HealthReport、常量（事件集、留存、轮询间隔）
├── service.go         # CRUD + 所有权鉴权 + select-cookie + 版本下限判定
├── store/sqlite/      # instances 表 + remote_notification_occurrences 表 + 留存清扫
├── proxy/server.go    # 每实例 HTTP 反向代理 + /ws 每连接桥 + 桥接会话守卫 + 结构化错误
└── relay/relay.go     # 每实例持久 WS 客户端 + 对账轮询 + 去重落库 + BroadcastToUser
internal/notifications/ # 修改：dispatch 时写 notification_occurrences（远端侧）；handlers 加 GET /notifications/occurrences
internal/webapp/         # 修改：handler.go/payload.go/shell.go 实例参数 → 端口注入 + 标题 + 省略 payload
internal/backendapp/     # 修改：装配（flag 门控）+ 路由 + e2e reset 清表
internal/runtimeflags/   # 修改：注册 features.multiInstance
profiles.yaml            # 修改：三 profile 默认 false
internal/persistence/    # 修改：requiredstores 描述符 + storeconformance 适配器（新 SQL schema owner 义务）
```

**前端（新增 6 类，修改 3 处）**

```text
lib/api/domains/instances-api.ts               # CRUD/select/health
lib/api/domains/remote-notifications-api.ts    # 列表/已读/清角标
lib/state/slices/instances/                    # 注册表镜像 + 当前实例 + 健康态
lib/state/slices/remote-notifications/         # 条目、未读计数（按实例）、已读
lib/ws/handlers/remote-notification.ts         # action=remote_notification → 写 slice
components/app-sidebar/app-sidebar-instance-picker.tsx  # 侧边栏头部选择器（workspace picker 模式）
components/app-sidebar/sections/notifications-section.tsx  # 通知模块 section + 角标
components/settings/…instances…                # Settings 实例管理页（SettingsGroup/Row 约定）
lib/instances/switch-instance.ts               # POST select + location.assign
修改：app-sidebar-header.tsx、app-sidebar-constants.ts（section id）、ws handler 注册表、settings 路由
```

## 依赖准出门禁结论

| 依赖 | 终态 |
|---|---|
| 远端 WS 通知帧形状 | **已试通**（代码证据：`local.go:60` BroadcastToUser + `message.go:30` action 常量；帧字段清单在 Send payload） |
| 远端历史 occurrence API | **不存在 → 本 change 自建**（D5）；远端需 ≥ 本版本，版本下限经 `/api/v1/system/info` 探测 |
| SPA 端口分离 boot 机制 | **已试通**（`config.ts:22-45`、`boot-payload.ts:274-290` app-state 回退、`ws-connector.tsx:15-16`） |
| shell 按请求注入 | **已试通**（`webapp/handler.go:20` PayloadBuilder 带 `*http.Request`；`shell.go` ComposeTitle） |
| e2e 双实例 fixture | **已试通**（`fixtures/backend.ts` spawn + `helpers/backend-ports` + `E2E_PORT_OFFSET` 端口错开机制现成） |
| 第三方依赖 | **无新增**（全为仓库内机制） |

## Research Log

### 未知 1：Go shell 能否按请求注入实例相关配置
- **验证方式**：读 `internal/webapp` shell 渲染链。
- **结论（试通证据）**：`PayloadBuilder func(*http.Request, RouteClassification) BootPayload`（`handler.go:20`）按请求构建；`RenderShell`→`BootPayloadScript`（`shell.go:41,166`）注入 `window.__KANDEV_BOOT_PAYLOAD__`；`ComposeTitle/rewriteTitle`（`shell.go:66,77`）支持 TitlePrefix。实例 cookie/query 在此层可读；`__KANDEV_API_PORT` 为 `global.d.ts` 声明的现有全局，`config.ts:36` 读取。
- **证据摘要**：`apps/backend/internal/webapp/handler.go:20,47,67`；`apps/backend/internal/webapp/shell.go:14,41,66,77,166`。

### 未知 2：真实 WS client 能否吃代理端口 base
- **验证方式**：读 WS 连接建立链。
- **结论（试通证据）**：`ws-connector.tsx:15-16` 用 `httpToWebSocketUrl(getBackendConfig().apiBaseUrl)` → ws://<host>:<port>/ws；`getBackendConfig` 的端口分支（`config.ts:36-40`）产出 `${protocol}//${hostname}:${port}`。代理端口同主机名即成立。boot 的 `webSocketPath` 字段真实 client 未用（仅 demo），本设计不需要它（代理端口服务标准 /ws 路径）。
- **证据摘要**：`apps/web/components/ws-connector.tsx:15-16`；`apps/web/lib/config.ts:22-45`；`apps/web/lib/ws/utils.ts`。

### 未知 3：远端事件源与对账面
- **验证方式**：读通知 provider 推送路径与 REST 面。
- **结论（试通证据）**：通知帧用户级广播 `p.hub.BroadcastToUser(message.UserID, msg)`（`local.go:60`），帧由 `ws.NewNotification(message.EventType, payload)` 构造（`local.go:58`）——**帧的 `action` 即语义事件名**（`pkg/websocket/message.go:30` 的 `"notification"` 是 MessageType 而非 action），`msg.ID` = occurrence ID，payload 含 task_id/session_id/title/body；`task.subscribe`/`session.subscribe`/`run.subscribe` 订阅模型存在（网关）。REST 仅有 provider CRUD（`notifications/handlers/handlers.go:27-31`），**无历史 occurrence 端点** → 按 D5 自建。
- **证据摘要**：`apps/backend/internal/notifications/providers/local.go:35-60`；`apps/backend/pkg/websocket/message.go:9-37`；`apps/backend/internal/notifications/handlers/handlers.go:27-31`。

### 未知 4：payload 绝对 URL 重写范围
- **验证方式**：检查 URL 承载面 + 评估重写风险。
- **结论（拒绝理由）**：v1 **不做任何 body 重写**（D4）。面板内导航全部由 ID 构建客户端路由（`lib/links.ts::linkToTask`）；远端绝对 URL 按跨实例深链处理；宿主绑定面按 spec 降级。消除逐端点重写风险。
- **证据摘要**：`apps/web/AGENTS.md`（linkToTask 唯一构建器）；proposal Impact 已列降级清单。

### 未知 5：e2e 双实例 fixture 可行性
- **验证方式**：读 e2e backend fixture。
- **结论（试通证据）**：`fixtures/backend.ts` 以 `spawn` 拉起后端二进制，端口经 `helpers/backend-ports` + `E2E_PORT_OFFSET` 错开（"prevents port clashes between concurrent test runs"，`backend.ts:20-22`）。第二个实例 = 再 spawn 一个（独立 home + 独立 port offset），现有机制直接支持。
- **证据摘要**：`apps/web/e2e/fixtures/backend.ts:2,12,20-22`；`apps/web/e2e/helpers/backend-ports.ts`。

## 风险与失败模式

对照两个 spec delta 的 Known Failure Modes，设计层补充：

| 风险 | 缓解 |
|---|---|
| 代理端口泄漏到非本机访问 | 监听同主机名所有接口是刻意的（远程浏览器访问本机面板的场景）；桥接守卫（D7）是安全边界而非 origin 放宽；auth off 时与现状（本机实例本身无认证暴露）风险等价，文档明示 |
| 远端版本过旧 | 健康探测版本下限 + 显式版本不匹配错误（D5） |
| relay 断线期间事件丢失 | 对账端点 + since 游标；对账分页上限 200，超限记"更早事件未同步"（spec 失败模式已列） |
| 双写竞态（WS 帧与对账同到） | 唯一键 + 单入口 `Record()`，INSERT 冲突即丢弃（幂等） |
| shell 实例参数与本地路由冲突 | `kandev_instance` 只影响 shell 渲染与 boot base；API 路由不经 shell 分类逻辑（RouteClassification 只分页面类） |
| 通知模块无界增长 | occurrences 表留存窗口（默认 7 天）+ 启动/周期清扫（沿用 SQLite 维护任务 admission 纪律） |
| 本机全量测试本就有红（环境基线） | 验证清单用基线复跑 + 逐名 diff 判零新增（见 tasks.md 验证清单） |

## 非目标（重申 + 修正）

- 不做合并看板/跨实例聚合任务视图（单活动实例 + 切换）。
- 不做跨实例任意 TCP 端口代理、payload URL 重写（D4）。
- 不做浏览器直连、不改 origin/CORS 策略。
- 远端事件不进 toast/浏览器通知/声音；本地通知链路不动。
- 不做 stats-sync 式数据镜像；远端数据不进本地任务表。
- 不做实例间任务迁移/交接；不做多 owner 共享一个实例注册（v1 per-owner）。
- Desktop 特权动作跨实例不启用（降级说明）。
