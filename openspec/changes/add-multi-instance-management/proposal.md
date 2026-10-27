## Why

用户同时运行多个 Kandev 实例（本机 10.224.50.152:38429 与 3090Ti 的 10.224.51.44:38429），被迫开两个浏览器面板来回切换；且远端实例的会话完成/等待输入通知只在远端自己的面板里可见，不看那边就漏。浏览器直连第二个实例被 WebSocket 同源校验（防跨站劫持）与跨站 Cookie 策略阻止，唯一可行架构是本机后端充当联邦客户端：持有实例注册表、按实例反向代理远端 API/WS、中继远端通知事件。本 change 让单一面板可切换并直接操作远端实例的任务，并通过侧边栏通知模块 + 未读角标接收全部实例的会话事件通知（用户已拍板：远端事件不做 toast/浏览器原生通知/声音）。

## What Changes

**总体形态：本机后端联邦 + 透明反向代理 + 跨实例通知中继。**

- **实例注册表**（新增，SQLite + Settings 页面）：登记远端实例（名称、base URL、可选凭据），带健康探测（可达性 + 版本，healthpoll 模式）。用户两实例当前均未开认证，v1 必须兼容无凭据直连；凭据字段预留 PAT（`kandev_pat_*` bearer，`internal/auth/tokens.go`）演进。
- **每实例本机代理端口**（新增）：为每个启用的远端实例在本机开一个专用反向代理端口（同主机名），转发其全部 HTTP API 与双向 WebSocket。SPA 复用既有端口分离机制（`getBackendConfig()` 的 `__KANDEV_API_PORT` 路径，dev 模式浏览器与 API 本就分端口）把 API/WS base 指向该端口——现有聊天/看板/任务详情 UI 无需改写即可直接操作远端任务。
- **实例选择器与切换**（新增，侧边栏头部 workspace picker 旁）：切换 = 整页重载携带实例标识，SPA 以该实例代理端口为 API/WS base，boot 走 `/api/v1/app-state` API 启动路径。单活动实例模型（不做合并看板）。标签页标题携带实例名（复用 `KANDEV_WEB_TITLE_PREFIX`/titlePrefix 机制）。
- **跨实例通知中继**（新增）：本机后端为每个已注册实例维持一条独立于面板选择的持久事件连接（WS 订阅 + 周期 REST 对账），把远端语义通知事件（`session.turn_finished`、`session.clarification_requested`、`office.inbox_item`）以 (instance, event, occurrence) 去重后在本地再发布，仅进入侧边栏通知模块 + 未读角标；点击条目切换到对应实例并导航到任务。
- **运行时开关**：作为高风险能力走 runtime feature toggle，默认在所有 shipped profile 关闭，按 `/runtime-feature-flags` 流程灰度。

**切换体验**
- From: 每个实例一个浏览器面板，手动来回切换；远端通知只进远端面板（漏看）。
- To: 单一面板内选择实例，切换后看板/聊天/收件箱/任务详情全部来自所选实例且可直接操作；全部实例的会话完成/等待输入事件聚合在侧边栏通知模块。
- Reason: 消除面板切换与通知漏看两个痛点。
- Impact: 新增能力，默认关闭；不改本地实例任何既有行为（通知、看板、auth 均保持原样）。

**用户已拍板的三个决策（记录为约束）**
1. v1 直接操作远端任务（不是只读+跳转）。
2. 两实例均未开认证：兼容无凭据直连现状，同时写明安全前提与 PAT 演进路径。
3. 远端通知仅侧边栏模块 + 未读角标：不弹 toast、不弹浏览器原生通知、不响声音；本地实例通知行为不变。

## Capabilities

### New Capabilities

- `multi-instance-management`: 实例注册表（含健康与凭据）、每实例本机代理端口（HTTP + WS 桥）、实例选择器与整页切换重水合、feature toggle 与桥接安全边界、宿主绑定能力的降级语义。
- `cross-instance-notifications`: 远端语义通知事件的中继（持久订阅 + 对账）、带实例身份的幂等去重、侧边栏通知模块与未读角标、读取状态与留存、提醒方式限定（不 toast/不原生通知/不声音）。

### Modified Capabilities

（无：本 change 不修改既有 capability 的 spec 语义。本地实例的通知链路、看板、Needs You 收件箱行为全部保持不变；通知平台仅新增一个中继消费方。）

## Research Log

### 未知 1：浏览器能否直连第二个实例
- **验证方式**：读 WS 升级校验与 CORS/cookie 代码。
- **结论（拒绝理由）**：不可行且是设计性禁止。`checkWebSocketOrigin`（`internal/gateway/websocket/origin.go:16`）+ `httpmw.AllowedOrigin`（`internal/common/httpmw/origin.go:30`）对浏览器 Origin 与请求 Host 严格校验；跨站 cookie（端口作用域名 + SameSite）也发不过去。唯一架构 = 本机后端联邦/代理。
- **证据摘要**：`apps/backend/internal/gateway/websocket/origin.go`；`apps/backend/internal/common/httpmw/origin.go:39-45`（host 归一化比较）。

### 未知 2：代理形态选哪种
- **验证方式**：读 SPA API/WS base 解析与 origin 策略细节。
- **结论（试通证据）**：选**每实例本机监听端口**。(a) `AllowedOrigin` 只比较主机名（`normalizeHost(parsed.Hostname())` 去端口），同主机名不同端口天然通过现有策略，零安全策略改动；(b) SPA 已有端口分离机制（`apps/web/lib/config.ts:31-37` `__KANDEV_API_PORT`，apiBaseUrl = `${protocol}//${hostname}:${port}`），所有 API 调用经单一 `resolveUrl(path, baseUrl)`（`apps/web/lib/api/client.ts`），零 API client 调用点改动；(c) WS URL 由 apiBaseUrl 推导（`apps/web/lib/ws/utils.ts`）。路径前缀方案需改全部 API 路径构造 + 逐端点 payload 绝对 URL 重写，SPA 侧改动面更大，弃。
- **证据摘要**：`apps/web/lib/config.ts:22-45`；`apps/web/lib/api/client.ts`（`resolveUrl`）；`apps/web/lib/ws/utils.ts`；`internal/common/httpmw/origin.go`。

### 未知 3：远端 boot/水合是否有现成入口
- **验证方式**：读 boot 契约与路由。
- **结论（试通证据）**：`GET /api/v1/app-state`（`internal/backendapp/helpers.go:1374`）是"不经 Go shell 渲染"的 API 启动路径（`boot-payload.ts:39` 注释确认）；boot 契约已有 `webSocketPath` 字段（`boot-payload.ts:25`）与 `titlePrefix`（区分相邻标签页的多实例痛点已有先例）。切换走整页重载即可复用。
- **证据摘要**：`apps/backend/internal/backendapp/helpers.go:1374`；`apps/web/src/boot-payload.ts:25,39`。

### 未知 4：通知事件从远端怎么来、语义是否已存在
- **验证方式**：读通知平台与既有 spec。
- **结论（试通证据）**：后端 `internal/notifications/` 已有语义事件（`session.turn_finished` / `session.clarification_requested` / `system.update_available` / `office.inbox_item`）、按 provider+event+occurrence 的幂等去重、WS 广播链路；等待语义与 parked 延迟投递已有 spec（`docs/specs/platform/requirements/notifications.md`、`docs/specs/disambiguate-waiting/`、`docs/specs/parked-notification-deferral/spec.md`）。中继挂在语义 occurrence 边界即可天然继承延迟语义，不再发明第二套延迟规则。
- **证据摘要**：`apps/backend/internal/notifications/`（service/providers/handlers）；`apps/web/lib/ws/handlers/notifications.ts`；`apps/web/lib/notifications/events.ts`。

### 未知 5：是否有跨实例先例与数据隔离纪律
- **验证方式**：读 stats-sync 文档与 spec。
- **结论（试通证据）**：stats-sync（`docs/public/stats-sync.md`）是唯一先例：远端推原始行到本机 ingest（共享密钥、自认证 webhook 白名单），本机存镜像表且**绝不进任务列表**。本 change 沿用其隔离纪律：远端数据不并入本地任务表，实例身份进一切 ID/去重键。
- **证据摘要**：`docs/public/stats-sync.md`；`openspec/specs/stats-sync/spec.md`。

### 未知 6：范围深度、认证现状、通知形态
- **验证方式**：向用户澄清（3 问）。
- **结论（试通证据）**：① v1 直接操作远端任务（全量代理）；② 两实例均未开认证（兼容无凭据直连 + 写明安全建议与 PAT 演进）；③ 远端通知仅侧边栏模块 + 未读角标（不 toast/浏览器通知/声音）。
- **证据摘要**：本会话澄清问答 `scope_depth = q1_opt2`、`auth_status = q2_opt1`、`notify_style = q3_opt3`。

## Impact

- **代码（后端）**：新增实例联邦相关包（注册表 store/service + 健康探测、每实例代理 server（HTTP + WS 桥）、远端事件 relay 客户端），`internal/backendapp` 装配；Settings 增加实例管理路由与页面数据；SQLite 迁移（注册表 + 通知读取状态表）；`profiles.yaml` + `internal/runtimeflags/registry.go` 注册默认关闭的 feature toggle；远端凭据入 `internal/secrets`。
- **代码（前端）**：侧边栏头部实例选择器（`app-sidebar-header` 区域）、侧边栏通知模块新 section（含未读角标、按实例分组、点击切换+跳任务）、Go shell/SPA boot 的实例参数与代理端口注入、app-state 启动路径接通；i18n 七语言目录；mobile parity（手机导航可达）。
- **安全**：不改 `AllowedOrigin`/CORS 信任策略；代理桥是第一方代码，自己校验本地会话（auth 开启时）并按注册凭据访问远端；远端无认证模式仅在可信网络下使用，文档明示风险。
- **测试**：Go 单测（注册表 CRUD、代理转发与 WS 桥、relay 去重与对账、goleak/Start-Stop 生命周期）；web vitest（选择器、通知模块、切换重载）；e2e 以第二个后端实例模拟远端（auth 关闭 + 开启两档）。
- **文档**：`docs/public/` 新增多实例管理用户文档（走 `/docs-maintainer`）；toggle 灰度走 `/runtime-feature-flags`。
- **已知限制（明示进用户文档）**：会话端口预览/port-proxy、桌面特权动作（本机打开文件/编辑器、桌面原生通知）在远端实例上下文降级为不可用说明；跨实例任意 TCP 端口代理不做。
