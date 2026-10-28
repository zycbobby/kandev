# surface-remote-inbox-bundles — System Design

实现 `openspec/changes/surface-remote-inbox-bundles/proposal.md` 与
`specs/cross-instance-inbox/spec.md` 裁定的行为。决策依据与替代方案见 proposal
「方案与决策」；本文只定 HOW：契约、文件结构、状态机与验收映射。

## Architecture

本机后端是联邦客户端（ADR 2026-10-28）：新增 `internal/instances/remoteinbox`
包按实例直连远端（PAT 注入，`relay.fetch`/`ProxyManager` credential 先例，不经
浏览器 proxy port），把远端自身的 clarification-inbox 面聚合为每实例一页；
backendapp 在 `/api/v1/instances/:id/` 下挂组合读与转发写路由（owner 域、
双 flag 门控）。SPA 仅在看本机实例时，为本机页之外每个启用实例渲染一个
section：行复用本机 Inbox 行解剖（实例 chip + 跳转改走 `switchInstance`），
展开挂共享 `ClarificationPanelSection` 并传入 additive 的 `respondTarget` 覆盖
（提交打到本机转发路由）。远端行不落任何本地表；行移除与计数只由远端页
响应落地（re-read 即契约）。

```
SPA(本机实例视图)
  ├─ 本机页: GET /api/v1/clarification-inbox?workspace_id=…            (现状不动)
  ├─ 实例页: GET /api/v1/instances/:id/clarification-inbox             (新, 本机后端)
  │            └─ 本机后端 --PAT--> 远端 GET /api/v1/workspaces
  │                              └─ 远端 GET /api/v1/clarification-inbox?workspace_id=… (逐 ws)
  ├─ 回答:   POST /api/v1/instances/:id/clarification/:pendingID/respond (新, 转发)
  └─ sidecar: PUT/DELETE /api/v1/instances/:id/clarification-inbox/sidecar/:pendingID (新, 转发)
```

## Contracts

### 后端路由（backendapp `instance_inbox_routes.go`，挂于既有实例路由装配处）

门控：`p.services.Instances != nil && p.features.NeedsYouInbox`（`routeParams`
已携带 features，helpers.go:1940 先例）；实例可见性沿用
`instanceCallerUserID` + `visibleInstance`（foreign/missing 一律 404）。

| 路由 | 语义 | 成功 | 降级/失败 |
|---|---|---|---|
| `GET /instances/:id/clarification-inbox?limit=` | 组合实例页 | `200` envelope | 降级也 `200`（status=degraded） |
| `GET /instances/:id/clarification-inbox/hidden` | 组合隐藏页（行带 state/snooze_until，页带 total） | `200` | 同上 |
| `POST /instances/:id/clarification/:pendingID/respond` | 转发回答/拒绝，body 与响应原样透传 | 透传远端 `200`/`409`（win-loss envelope、not_active） | 拨号失败 `502 {"error":"remote_unreachable","instance":name}` |
| `PUT /instances/:id/clarification-inbox/sidecar/:pendingID` | 转发 dismiss/snooze | 透传 `204/400/404` | `502` |
| `DELETE /instances/:id/clarification-inbox/sidecar/:pendingID` | 转发 restore | 透传 `204/404` | `502` |

实例页 envelope（`status: "ok"|"degraded"`；degraded 时 `bundles:[]`、`count:0`，
`degraded_reason` 取闭集）：

```json
{
  "instance_id": "…", "instance_name": "…", "status": "ok",
  "degraded_reason": "unreachable | unauthorized | inbox_disabled | error",
  "bundles": [{ "…本机 bundle 同形状…": "", "instance_id": "…", "workspace_id": "…" }],
  "count": 1, "hidden_count": 0, "next_snooze_expiry": null, "has_more": false
}
```

降级理由映射（closed set）：拨号/超时 → `unreachable`；远端 `401/403` →
`unauthorized`（未登记 PAT 或 PAT 失效；降级文案指向登记 PAT）；远端 inbox
组 `404`（远端未开 `features.needsYouInbox`）→ `inbox_disabled`；其余非 2xx →
`error`。零 workspace 的远端是 `ok` 的空页而非降级。

### remoteinbox 包（`internal/instances/remoteinbox/`）

```go
func New(inst instances.Instance, credential func() (string, error), client *http.Client) *Client

type ReadResult struct {
    Page     *Page   // status == ok
    Degraded Reason  // status == degraded
}
type Page struct {
    Bundles          []Bundle // 按 created_at asc, pending_id asc 全序；cap=limit
    Count, HiddenCount int    // Count == len(Bundles)（AC .12 同构）
    NextSnoozeExpiry *time.Time
    HasMore          bool     // 任一 ws 页 has_more 或合并溢出
}
// Bundle = 本机 inbox bundle 的 wire 形状 + InstanceID + WorkspaceID（来源 ws）
func (c *Client) ReadInbox(ctx, limit int) ReadResult
func (c *Client) ReadHidden(ctx, limit int) ReadResult // 行额外带 State/SnoozeUntil；页带 Total
func (c *Client) Respond(ctx, pendingID string, body []byte) (int, []byte, error) // 原样透传
func (c *Client) UpsertSidecar(ctx, pendingID, state, duration string) (int, error)
func (c *Client) DeleteSidecar(ctx, pendingID string) (int, error)
```

约束：每实例一次读的总 deadline（10s，context.WithTimeout）；逐 workspace 串行
（workspace 数小，串行即有界）；无 PAT 也发起拨号（auth-off 远端可用）；
Authorization 头只由 credential 注入（relay.authorize 先例）；日志只带实例名
与状态，绝不带凭据。聚合分解为小函数守住 golangci 函数行数限制。

### 前端状态与触发（slice 扩展 + 新 controller）

`needs-you-inbox` slice 增加（本机 `byWorkspaceId` 形状不动）：

```ts
type RemoteInboxInstanceState = {
  instanceId: string;
  status: "idle" | "loading" | "ready" | "error";
  degradedReason?: "unreachable" | "unauthorized" | "inbox_disabled" | "error";
  bundles: RemoteInboxBundle[];   // = ClarificationInboxBundle + instance_id + workspace_id
  count: number; hiddenCount: number;
  nextSnoozeExpiry: string | null; hasMore: boolean;
  appliedGeneration: number; lastAppliedOk: boolean;   // 沿用本机 stale-response 纪律
};
// actions: beginRemoteInboxRead / setRemoteInboxPage / setRemoteInboxDegraded /
//          bumpRemoteInboxRefreshTick(instanceId?) / bumpRemoteInboxRetryTick(instanceId)
```

角标（AC .40 producer 集的显式扩展）：新 selector
`selectComposedNeedsYouCount` = 本机页 count（已加载）+ Σ 已落地远端页 count；
`selectComposedNeedsYouHasMore` = 本机 hasMore ∨ 任一远端 hasMore。未加载/降级
实例贡献 0（count-known 语义，failed-inbox 先例）；boot 仍只 seed 本机页。侧边栏
入口（`app-sidebar-primary-nav`、手机导航、页内 tab strip）改读 composed selector。

远端页触发集（`use-remote-inbox-controller`，仅当
`needsYouInbox && multiInstance && activeInstanceId === LOCAL_INSTANCE_ID`）：
挂载/实例清单变化、`remote_notification` WS 事件（`event ===
"session.clarification_requested"` → bump 该 `instance_id`）、前台/可见性恢复
（复用 `useForegroundRefresh`）、60s 有界周期（仅 tab 可见）、每实例手动重试
tick、每实例 snooze 到期定时器（读 `nextSnoozeExpiry`，5s 下限沿用本机参数）。
每实例一个 `NeedsYouInboxRefreshCoordinator`（scopeKey 含 instanceId）。

### 共享答案组件 additive 覆盖

`useClarificationGroup(messages, onOutcome?, onLateAnswer?, options?)` 增加可选
`options.respondTarget?: (pendingId: string) => string`，贯穿
`ClarificationInputOverlay` → `ClarificationPanelSection`（均为可选 prop）。
`postClarification` 优先用 `respondTarget(pendingId)`，否则现状
`${apiBaseUrl}/api/v1/clarification/${pendingId}/respond`。远端行 host 传
`(pid) => \`${localBase}/api/v1/instances/${instanceId}/clarification/${pid}/respond\``
且**不传** `onLateAnswer`（抑制 late-answer 回退）。本机 host（任务会话、
Quick Chat、本机 Inbox 行）不传 options，渲染与回调逐字节不变（AC .39 测试断言）。

### 行与 section UI

`NeedsYouInboxRow` 增加可选 `source?: { instanceId: string; instanceName: string }`：
行首实例 chip；open-task 控件（按钮 + 菜单项）改走
`switchInstance(instanceId, linkToTask(taskId))`；展开面板传 `respondTarget`、
不传 late-answer。新 `RemoteInboxSection`（每实例）：header（实例名 + 行数/截断
角标）→ 行列表 / 空态一句 / 降级态（命名实例 + 理由文案 + Retry，无答案组件）；
hiddenCount > 0 时复用 `NeedsYouInboxHiddenPanel`（加 source 参数，restore 走转发
DELETE）。挂载于 needs-you tab 本机列表之后。

## File Structure

```
后端
+ internal/instances/remoteinbox/{client.go, aggregate.go, types.go, client_test.go, aggregate_test.go}
+ internal/backendapp/instance_inbox_routes.go            (路由 + 装配 + envelope)
~ internal/backendapp/helpers.go                           (注册调用一处)
~ internal/instances/AGENTS.md                             (layout 增 remoteinbox；不变量措辞修订)
前端
+ lib/api/domains/remote-inbox-api.ts                      (local-base 解析，仿 instances-api)
+ lib/types/remote-inbox.ts                                (RemoteInboxPage/Bundle/DegradedReason)
~ lib/state/slices/needs-you-inbox/{types,slice,selectors}.ts  (+index)
~ hooks/domains/needs-you-inbox/use-remote-inbox-controller.ts (+test)
~ hooks/domains/session/use-clarification-group.ts         (respondTarget)
~ components/task/chat/clarification-{panel-section,input-overlay}.tsx (透传 prop)
~ components/needs-you-inbox/needs-you-inbox-row.tsx       (source prop)
+ components/needs-you-inbox/remote-inbox-section.tsx (+test)
~ app/needs-you-inbox/needs-you-inbox-page-client.tsx      (挂 sections)
~ components/app-sidebar/app-sidebar-primary-nav.tsx + navigation/mobile-sidebar-layout-navigation.tsx (composed selector)
+ src/locales/{en,pt-pt,zh-cn,zh-hk,zh-tw,ja,ko,pseudo}/needs-you-inbox.json (或既有命名空间文件)
e2e
+ e2e/tests/multi-instance/remote-inbox.spec.ts
~ e2e/fixtures/remote-backend.ts                           (如需澄清 seed helper)
文档
~ docs/decisions/2026-10-28-multi-instance-federation.md   (Decision 3 修订)
~ docs/specs/ui/requirements/needs-you-inbox.md            (REQ-002 + 措辞豁免)
~ docs/specs/ui/system-design/needs-you-inbox-0{1,2,3}.md  (契约/控制流/copy)
~ docs/public/multi-instance.md                            (用户指南一段)
```

## Research Log

### 探测 1：远端 e2e 能否 seed 出 answerable bundle
- **验证方式**：读 `e2e/helpers/clarification.ts` 与 `cmd/mock-agent` scenario 表。
- **结论（试通证据）**：`seedClarificationSession` 的 seed→wait(WAITING_FOR_INPUT) 流程可直接以远端 ApiClient 复刻（`/e2e:clarification` scenario 存在）；远端 fixture 走 e2e profile（`KANDEV_E2E_MOCK=true`）→ 远端 `features.needsYouInbox` 默认开，inbox 路由存在。
- **证据摘要**：`clarification.ts:23-60`；`mock-agent/scenarios.go:40-42`；`remote-backend.ts:202`。

### 探测 2：新路由的装配点与门控谓词可达性
- **验证方式**：读 `backendapp/helpers.go`、`instance_routes.go`、`instance_proxies.go`。
- **结论（试通证据）**：`registerInstanceRoutes(p)` 在 helpers.go:1914 挂载，`routeParams` 已携带 `features.NeedsYouInbox`（helpers.go:1940 传给 clarification.RegisterRoutes 的先例）；credential 闭包模式在 `instance_proxies.go:97-105`（secretadapter.Reveal），remoteinbox 直接复用。
- **证据摘要**：`handlers.go:214-236`（inbox 组按 needsYouInbox 门控、respond 无条件）；`instance_proxies.go:96-106`。

### 探测 3：共享组件链与 slice 的精确扩展点
- **验证方式**：读 `clarification-panel-section.tsx`、`clarification-input-overlay.tsx`、`use-clarification-group.ts`、`needs-you-inbox/types.ts`、`needs-you-inbox-row.tsx`。
- **结论（试通证据）**：组件链 props 为 `{pending, messages, onResolved, shortcutScopeRef, maxHeightVh, onOutcome?, onLateAnswer?}`，`postClarification` 在 `use-clarification-group.ts:249` 硬编码 `apiBaseUrl`——additive `respondTarget` 插入点唯一且不触碰本机路径；slice 的 `appliedGeneration/lastAppliedOk` stale-response 纪律可原样复制到实例键；行 open-task 为 Link（`needs-you-inbox-row.tsx:142,259`），远端改 `switchInstance`。
- **证据摘要**：`clarification-panel-section.tsx:19-39`；`use-clarification-group.ts:241-278`；`types.ts:16-50`。

### 探测 4：ADR 修订与规格文档的仓库惯例
- **验证方式**：查 `docs/decisions/` 既有修订示例。
- **结论（试通证据）**：修订惯例为 Status 行追加 `(amended YYYY-MM-DD)` + 文内修订段（0001/0010/0015/0018 先例）；docs/specs 改动后须跑 `list-docs.py validate` 与 `lint-spec-files.py --all`。
- **证据摘要**：`docs/decisions/0015-*.md:3`、`0018-*.md:3`；根 CLAUDE.md「Knowledge」节。

## Risks / 边界

- **远端旧版本**：健康探测的 `MinRemoteVersion` 门槛只影响健康展示；组合读不阻塞在健康态，直接以读结果降级（避免 stale 健康误伤）。
- **多实例 × 多 workspace 读放大**：有界（每实例 1+W 次串行读、10s deadline、60s 周期、仅 tab 可见），无长驻 goroutine——remoteinbox 是请求级 HTTP 拨号，不引入 Start/Stop 生命周期。
- **看远端实例时的行为**：远端 section 只在看本机实例时渲染（单活动实例模型）；看远端时该实例自身 Inbox 即其本机页，不感知 A 的注册表（`remote-inbox-api` 走 local base 解析，`instances-api` 同款护栏）。
- **远端 404 respond**（bundle 在远端未知）：客户端视作 error（行保留可重试），随后 re-read 收敛移除——不把它误报为 not_active。
