# add-multi-instance-management — tasks.md

> **For agentic workers:** 按 task 顺序推进（openspec-apply-change），每个 task 独立可测、独立提交。步骤用 `- [ ]` 跟踪。
>
> **Goal:** 单一面板切换并直接操作远端 Kandev 实例 + 跨实例通知侧边栏模块
> **Architecture:** 本机后端联邦（实例注册表 + 每实例代理端口 + 事件中继），SPA 复用端口分离机制切换实例；见 `design.md`
> **Spec:** `proposal.md`、`specs/multi-instance-management/spec.md`、`specs/cross-instance-notifications/spec.md`、`design.md`
>
> **工作区提醒**：本 worktree 首次跑 pnpm 命令前先 `pnpm install --frozen-lockfile`（在 `apps/`）；shell 一律不 `cd`，用 `-C`。

## Global Constraints

- Go lint：函数 ≤80 行/≤50 语句、复杂度限额见 `apps/backend/.golangci.yml`；长 goroutine 必须 Start/Stop + WaitGroup + 包级 `goleak.VerifyTestMain`；重试/退避用 `time.NewTimer`+`select`，禁 `time.Sleep`。
- SQLite：新表走 `init*Schema`（`CREATE TABLE IF NOT EXISTS`）+ 幂等迁移；新 SQL schema owner 必须在 `internal/persistence/requiredstores` 注册描述符 + `internal/persistence/storeconformance` 适配器；跑 `go run ./cmd/sqlguard ./internal` 与 `go test -race ./internal/persistence/storeconformance`。
- Feature toggle：`features.multiInstance`（env `KANDEV_FEATURES_AUTH` 风格 → `KANDEV_FEATURES_MULTI_INSTANCE`），所有 shipped profile 默认 `false`；关闭路径零行为差异并有测试；上线灰度走 `/runtime-feature-flags`。
- i18n：所有新 copy 走 `t()`；键进 `src/locales/en/` 七语言（zh-hant 对走 `pnpm run i18n:zh-hant`）；禁 em dash；`pnpm run i18n:check` 必须绿。
- Mobile parity：手机导航可达、44px 触达、角标状态一致；手机端 picker 位于 workspace picker 同层（菜单滚动区外）。
- WS：不用全局 `hub.Broadcast`；中继用 `BroadcastToUser`（owner）。
- Auth：实例注册表 per-owner（`notification providers` 模式）；新路由按现有 settings 鉴权；auth off 时合成 admin。
- E2E：causal-waits（`waitForHttp`/`watchWs`）禁裸 sleep；新 spec 文件遵守 one-worker-per-shard 与内存预算。
- 提交：Conventional Commits ≤100 字符（`feat:`/`test:`/`docs:`），走 `/commit` 技能。

## Review Focus

实现任务覆盖不到、最可能咬到真用户的五类输入/失效（每条已有归属任务的测试钉住）：

1. 远端中途不可达 → 代理返回结构化错误、面板不挂起不伪造数据（Task 4/10 测试）
2. 本地 auth 开 + 远端 auth 开 → 桥接必须有凭据守卫，无裸桥（Task 4 测试）
3. 远端版本低于下限 → 显式版本不匹配错误而非静默怪异行为（Task 2 测试）
4. 断线重连后同一事件重复出现 → (instance,event,occurrence) 唯一键 + 双路写入只投一次（Task 5 测试）
5. flag 关闭时零行为差异（无端口/无 relay/无 UI，含 shell 参数不生效）（Task 1/6 测试）

---

## Verification Strategy

- **首选 `e2e-script`**：`apps/web/e2e/multi-instance.spec.ts` 双后端实例跑通核心路径（见 E2E 测试图）。
- **次选 `integration-test`**：Go 侧用 `httptest` 假远端覆盖代理转发/WS 桥/中继对账；`pnpm --filter @kandev/web test` 覆盖前端 slice/handler/组件。
- unit-test 仅用于纯函数（归一化、去重键、token 签名），不作为唯一验收。
- 命令：`make -C apps/backend test`、`cd apps/web && pnpm test -- <pattern>`（实际用 `pnpm --filter @kandev/web test`）、`pnpm --filter @kandev/web run typecheck`、`pnpm --filter @kandev/web run lint`、`pnpm run i18n:check`（在 apps/web）、`pnpm e2e:run --grep "multi-instance"`。
- **本机基线纪律**（记忆：本机全量测试必红）：全量套件只做基线对比——先跑基线记录失败名，再复跑逐名 diff 判「零新增」；串行执行，不并行全量。

## E2E 测试图

fixture：`bootRemoteBackend()`（spawn 第二个后端二进制：独立 home、独立 `E2E_PORT_OFFSET` 档、`KANDEV_E2E_MOCK=true`、seed 一条 B 独有标题任务）。

| # | 用户路径 | 断言（causal wait 后 UI 默认超时） |
|---|---|---|
| 1 | 在 A 注册 B | 设置页健康绿 + 版本显示；选择器出现 |
| 2 | 切到 B | 整页重载；看板出现 B 独有任务（非 A 任务） |
| 3 | 在 B 的任务聊天发消息 | B 的任务状态流转（api-client 直连 B 断言）；面板经代理可见 |
| 4 | 切回 A；对 B 触发一轮完成（api-client 对 B 发 prompt 等 mock turn 终态） | A 面板通知模块出现条目；模块与实例角标 +1；无 toast |
| 5 | 点击该条目 | 切到 B 并打开对应任务路由 |
| 6 | 标记已读 → reload | 角标清零且 reload 后保持 |
| 7 | kill B | 实例健康降级；A 自身看板/任务操作不受影响；代理面给结构化错误 |
| 8 | flag off 启动新后端 | 无选择器、无 `instances` 路由、shell 带实例参数也走本地 boot |

---

## 实现清单

### Task 1: feature toggle + 实例注册表 domain/store

**Files:**
- Create: `apps/backend/internal/instances/models.go`、`apps/backend/internal/instances/service.go`
- Create: `apps/backend/internal/instances/store/sqlite/store.go`（含 `instances` 与 `remote_notification_occurrences` 两表 schema）
- Modify: `apps/backend/internal/runtimeflags/registry.go`、根 `profiles.yaml`
- Modify: `apps/backend/internal/persistence/requiredstores/`、`apps/backend/internal/persistence/storeconformance/`、`apps/backend/cmd/kandev/e2e_reset.go`

**Interfaces:**
- Produces: `instances.Instance{ID, OwnerUserID, Name, BaseURL, CredentialSecretID string; Enabled bool; ProxyPort int; CreatedAt, UpdatedAt time.Time}`；`instances.Service` 提供 `CreateInstance/ListInstances/UpdateInstance/DeleteInstance(ctx, callerUserID, …)`（owner 鉴权，`*NotFound` 哨兵模式）；store 方法 `CreateInstance/GetInstance/ListInstancesByOwner/UpdateInstance/DeleteInstance/UpdateProxyPort`（occurrence 方法归 Task 5 扩展同文件）；`Provide` 装配点供 backendapp 用。

- [x] 步骤 1：注册 runtime flag（`features.multiInstance`，默认全 profile false）+ 更新 registry/profile 完整性测试（现有 completeness 测试会强制前后端契约对齐）
- [x] 步骤 2：写失败测试 `internal/instances/service_test.go`：`TestCreateListUpdateDeleteInstanceOwnedByCaller`、`TestInstanceInvisibleToOtherOwner`（404 哨兵）、`TestDefaultFlagKeepsRegistryInert`；跑 `go test ./internal/instances/` 确认 FAIL
- [x] 步骤 3：实现 models/service/store（schema：`CREATE TABLE IF NOT EXISTS` + 幂等迁移；两表唯一键 `UNIQUE(instance_id,event,occurrence_id)`；e2e_reset 加两表清理）+ requiredstores/storeconformance 注册
- [x] 步骤 4：`go test ./internal/instances/... && go run ./cmd/sqlguard ./internal && go test -race ./internal/persistence/storeconformance` 全绿；`make -C apps/backend test` 相关包无新增失败
- [x] 步骤 5：提交（`feat: add multi-instance registry and feature flag`）

### Task 2: 实例健康探测 + 版本下限

**Files:**
- Create: `apps/backend/internal/instances/health.go`（+ `health_test.go`，httptest 假远端）

**Interfaces:**
- Consumes: Task 1 的 Service/store。
- Produces: `instances.HealthReport{Reachable bool; Version, Error string; Supported bool; LastCheckedAt time.Time}`；`(*Service) GetHealth(ctx, callerUserID, instanceID) (HealthReport, error)`；常量 `MinRemoteVersion`（= 本 change 版本）；探测走远端 `GET /api/v1/system/info`。

- [x] 步骤 1：失败测试：`TestHealthPollerMarksReachableAndVersion`、`TestVersionBelowFloorMarksUnsupported`、`TestUnreachableInstanceDegradedAndIsolated`（httptest 假远端；版本字符串来自真实 system/info 响应形状）
- [x] 步骤 2：实现（复用 `internal/integrations/healthpoll` 90s 轮询模式，参照 `internal/jira` 用法；健康态为进程内存 map + Start/Stop + goleak `TestMain`）
- [x] 步骤 3：测试全绿（含 goleak）→ 提交（`feat: add remote instance health polling with version floor`）

### Task 3: 远端侧 notification occurrences 落库 + REST

**Files:**
- Modify: `apps/backend/internal/notifications/models/`（Occurrence 模型）、`store/`（表 `notification_occurrences` + 写入/查询/留存清扫）、`service/service.go`（dispatch 时写 occurrence）、`handlers/handlers.go`、`dto/dto.go`

**Interfaces:**
- Produces: `GET /api/v1/notifications/occurrences?since=<RFC3339>&limit=`（≤200，默认 100，callerUserID 鉴权）返回 `[{event, occurrence_id, task_id, session_id, title, body, created_at}]`；`service` 在语义 occurrence 分发路径写一行（有界留存默认 7 天，启动+周期清扫挂现有维护 admission）。

- [x] 步骤 1：失败测试：`TestOccurrenceRecordedAtDispatch`（现有 dispatch 测试旁扩展）、`TestListOccurrencesSinceAndLimit`、`TestListOccurrencesScopedToUser`、`TestOccurrenceRetentionSweep`
- [x] 步骤 2：实现模型/表/写入钩子/REST/DTO（表进本包既有 schema owner 体系，跑 sqlguard + storeconformance）
- [x] 步骤 3：`go test ./internal/notifications/...` 全绿 → 提交（`feat: record and expose notification occurrences`）

### Task 4: 每实例代理端口（HTTP + WS 桥）

**Files:**
- Create: `apps/backend/internal/instances/proxy/server.go`（+ `server_test.go`、`bridgeauth_test.go`；包级 `TestMain` goleak）
- Modify: `apps/backend/internal/backendapp/`（装配：flag 门控起停 ProxyManager）

**Interfaces:**
- Consumes: Task 1 Service/凭证（`CredentialSecretID` 经 `internal/secrets`+`integrations/secretadapter` 读取）。
- Produces: `proxy.NewServer(inst instances.Instance, credential func() (string, error), authOn bool, log) *Server`；`(*Server) Start(ctx) error / Stop() / Port() int`；HTTP `/api/v1/**` 反向代理（Director 注入 `Authorization: Bearer <PAT>`）；`/ws` 每连接双向桥（upgrade 后 dial 远端 `ws(s)://host/ws`，Authorization 透传）；`/healthz`；桥接守卫 `requireBridgeAuth`：`features.auth` 开启时校验 HMAC 能力 cookie `kandev_bridge_<instanceID>`（仿 `PortProxyHandler` 能力模式：15 分钟滑动、实例作用域）；auth 关闭时放行（合成 admin 语义）。远端不可达 → 结构化 JSON 错误（含 instance 标识）。

- [x] 步骤 1：失败测试：`TestProxyForwardsApiAndInjectsCredential`（httptest 假远端断言收到的 Authorization）、`TestWsBridgeBidirectionalFrames`（假远端 echo WS）、`TestRemoteUnreachableReturnsStructuredError`、`TestBridgeRequiresCapabilityWhenAuthOn`、`TestProxyStartStopNoGoroutineLeak`
- [x] 步骤 2：实现（端口 `:0` 动态分配 + `UpdateProxyPort` 持久化重绑；gin mux 不挂全局 auth 中间件，仅自校验）
- [x] 步骤 3：backendapp 装配（Enabled 变更联动起停）+ 测试全绿 → 提交（`feat: add per-instance reverse proxy with ws bridge`）

### Task 5: 远端事件中继 + 本地再发布

**Files:**
- Create: `apps/backend/internal/instances/relay/relay.go`（+ `relay_test.go`、`reconcile_test.go`；包级 goleak）
- Modify: `apps/backend/internal/instances/store/sqlite/store.go`（occurrence 方法）、`apps/backend/internal/backendapp/`（RelayManager 装配）、`apps/backend/pkg/websocket/message.go`（action 常量 `remote_notification`）

**Interfaces:**
- Consumes: Task 1 store、Task 3 远端 occurrences REST、Task 4 凭证解析。
- Produces: `relay.NewRelay(inst, credential, store, hub Broadcaster, log)`；`Start(ctx)/Stop()`；WS 客户端按帧 `action`（=语义事件名，`ws.NewNotification(message.EventType, …)`，`local.go:58`）过滤封闭集 `session.turn_finished`/`session.clarification_requested`/`office.inbox_item` → `store.Record(...)` → 新插入才 `hub.BroadcastToUser(owner, {action: "remote_notification", payload})`（本地再发布的 payload 内含 `event` 字段）；对账循环 60s：`GET <remote>/api/v1/notifications/occurrences?since=<last>` 分页 ≤200 → Record → 新者广播；退避重连 1s→30s cap（timer+select）；store 新方法 `Record(ctx, owner, instanceID, event, occurrenceID, taskID, sessionID, title, body, at) (inserted bool, err)`（`INSERT OR IGNORE` 语义）、`ListOccurrences(ctx, owner, since, limit)`、`MarkOccurrenceRead/Dismissed(ctx, owner, id)`、`SweepOccurrences(ctx, before)`；REST（本机）：`GET /api/v1/remote-notifications`、`POST /api/v1/remote-notifications/:id/read|dismiss|read-all`。

- [x] 步骤 1：失败测试：`TestRelayRecordsAndBroadcastsNewOccurrenceOnly`、`TestRelayDedupAcrossLiveAndReconcile`（同 occurrence 双路只广播一次）、`TestReconcileRecoversMissedWindow`（假远端 occurrences 端点 + 断连窗口）、`TestRelayReconnectBackoffNoSleep`、`TestParkedDeferralInherited`（远端未 dispatch 则本地无条目——帧不到即不到）
- [x] 步骤 2：实现 relay + store 方法 + 本机 REST + 装配
- [x] 步骤 3：`go test ./internal/instances/...` 全绿（含 goleak）→ 提交（`feat: relay remote notification occurrences locally`）

### Task 6: shell 远端实例 boot

**Files:**
- Modify: `apps/backend/internal/webapp/payload.go`（`BootPayload` 加 `APIPort int json:"apiPort,omitempty"` + `OmitBootData bool json:"-"`）、`shell.go`（OmitBootData 时脚本只注入 `window.__KANDEV_API_PORT=<APIPort>`，TitlePrefix 走现有重写）、`handler.go`（实例解析 seam）
- Modify: `apps/backend/internal/backendapp/helpers.go`（PayloadBuilder 包装：flag 开 + cookie/query `kandev_instance` → 查实例 → 端口 resolver；`select` 端点 `POST /api/v1/instances/:id/select` 写 `kandev_instance` cookie + 桥接能力 cookie + 返回 `{href}`）
- Test: `apps/backend/internal/webapp/shell_test.go` 扩展 + `helpers_test.go`

**Interfaces:**
- Consumes: Task 1 Service、Task 4 端口（resolver 闭包注入 webapp，避免包依赖）。
- Produces: 远端实例 shell HTML：标题=实例名、含 `window.__KANDEV_API_PORT`、不含 `__KANDEV_BOOT_PAYLOAD__`（SPA 走 app-state 回退，`boot-payload.ts:274-290` 现有路径）。

- [x] 步骤 1：失败测试：`TestShellRemoteInstanceInjectsPortAndOmitsPayload`、`TestShellInstanceParamIgnoredWhenFlagOff`、`TestSelectSetsInstanceAndBridgeCookies`
- [x] 步骤 2：实现三处修改（select 端点在 instances 路由组，settings 鉴权）
- [x] 步骤 3：`go test ./internal/webapp/... ./internal/backendapp/...` 相关全绿 → 提交（`feat: boot spa shell for remote instances`）

### Task 7: Settings 实例管理页（前端）

**Files:**
- Create: `apps/web/lib/api/domains/instances-api.ts`（+ test）、`apps/web/components/settings/instances/`（页面组件 + test）
- Modify: settings 路由与 tabs 注册、`src/locales/en/`（新键，7 语言）

**Interfaces:**
- Consumes: Task 1/2 REST（`/api/v1/instances` CRUD+health、Task 6 select）。
- Produces: `instances-api.ts` 导出 `listInstances/createInstance/updateInstance/deleteInstance/selectInstance/getInstanceHealth`；页面组件遵循 `SettingsGroup/SettingsRow/SettingsInfo` 与 `useSettingsSaveContributor` 约定。

- [x] 步骤 1：失败测试（vitest）：api client 请求形状与错误映射；组件渲染/保存/丢弃/健康横幅（含 390px 视口断言）
- [x] 步骤 2：实现 + i18n 键（`settings`/`instances` 命名空间；zh-hant 走脚本）
- [x] 步骤 3：`pnpm --filter @kandev/web test -- instances`、`pnpm run i18n:check`（apps/web）绿 → 提交（`feat: add instance management settings page`）

### Task 8: 侧边栏实例选择器 + 切换

**Files:**
- Create: `apps/web/components/app-sidebar/app-sidebar-instance-picker.tsx`（+ test）、`apps/web/lib/instances/switch-instance.ts`（+ test）
- Modify: `apps/web/components/app-sidebar/app-sidebar-header.tsx`、`lib/state/slices/instances/`（新 slice：注册表+当前实例+健康）、手机导航挂点（workspace picker 同层）

**Interfaces:**
- Consumes: Task 7 api、Task 6 select。
- Produces: `switchInstance(instanceId: string, targetPath?: string): Promise<void>`（POST select → `window.location.assign(href)`；targetPath 形如 `/t/:id`）；slice `instances.items/activeId/health`；选择器组件含当前实例名 + 健康点 + 远端通知角标位（Task 9 接线）。本地实例恒为列表首项（id=`local`）。**宿主绑定降级**：`useIsRemoteInstance()`（由当前实例 id ≠ `local` 派生）+ 对宿主绑定动作位点的降级渲染（见步骤 1b）。

- [x] 步骤 1：失败测试：switch 调 select 且 location.assign 目标 href；picker 渲染/禁用态/无实例时隐藏；手机导航可达
- [x] 步骤 1b：宿主绑定降级（spec `multi-instance-management` 的「宿主绑定能力降级」Requirement）：`rg` 定位宿主绑定动作位点（桌面特权命令：open folder/editor，`lib/desktop/` 调用；会话端口预览/port-proxy 打开链接的构建点）→ 逐位点包 `useIsRemoteInstance()` 降级为不可用说明（含 i18n 键）→ 失败测试：远端实例上下文中动作渲染为禁用 + tooltip/说明文案，点击不发命令
- [x] 步骤 2：实现 + i18n；mobile parity（44px、菜单滚动区外）
- [x] 步骤 3：vitest + `pnpm run i18n:check` 绿 → 提交（`feat: add sidebar instance picker and switching`）

### Task 9: 侧边栏通知模块

**Files:**
- Create: `apps/web/lib/api/domains/remote-notifications-api.ts`（+ test）、`lib/state/slices/remote-notifications/`（+ test）、`lib/ws/handlers/remote-notification.ts`（+ test）、`apps/web/components/app-sidebar/sections/notifications-section.tsx`（+ test）
- Modify: `app-sidebar-constants.ts`（section id `notifications`）、ws handler 注册表、手机导航

**Interfaces:**
- Consumes: Task 5 本机 REST + WS action `remote_notification`、Task 8 `switchInstance`。
- Produces: slice `remoteNotifications.entries/unreadByInstance/read/dismiss/readAll`；handler 仅写本 slice（**不触碰** `notifications.ts` 现有 handler/sonner/声音）；section 组件按实例分组 + 未读角标，点击条目 `switchInstance(instanceId, \`/t/${taskId}\`)` 并标记已读。

- [x] 步骤 1：失败测试：handler 收帧写 slice 且不触发 toast/Notification/sound（mock 断言零调用）；去重（同 id 帧两次只一条）；点击条目调用 switchInstance 且标记已读；角标清零与 reload 保持（mock REST）；手机端 section 可达
- [x] 步骤 2：实现 + i18n + mobile parity
- [x] 步骤 3：vitest（含现有 `notifications` handler 测试不回归）+ i18n:check 绿 → 提交（`feat: add cross-instance notifications sidebar module`）

### Task 10: E2E 双实例流

**Files:**
- Create: `apps/web/e2e/multi-instance.spec.ts`、`apps/web/e2e/fixtures/remote-backend.ts`（复用 `backend.ts` spawn + `helpers/backend-ports` + 独立 home）
- Modify: e2e README（新 spec 说明）

- [x] 步骤 1：实现 fixture `bootRemoteBackend()`（独立 `E2E_PORT_OFFSET` 档 + seed B 独有任务 + `KANDEV_E2E_MOCK=true`）
- [x] 步骤 2：按 E2E 测试图 1–8 写 spec（causal-waits：`waitForHttp`/`watchWs`；对 B 的断言用 api-client 直连 B）
- [x] 步骤 3：`pnpm e2e:run -- grep multi-instance`（apps/web，遵守 shard/worker 纪律）全绿 → 提交（`test: add multi-instance e2e flows`）

### Task 11: 公开文档 + 灰度清单

**Files:**
- Create: `docs/public/multi-instance.md`（注册/切换/通知/安全前提：远端需 ≥ 本版本、无认证仅限可信网络、PAT 建议；宿主绑定降级清单）
- Modify: `docs/public/feature-status.md`、`docs/public/` 相关导航（走 `/docs-maintainer`）

- [x] 步骤 1：撰写文档（结构遵循 `docs/public/README.md` 长页可读性规范）
- [x] 步骤 2：`/runtime-feature-flags` 清单核对：registry 元数据完整、disabled 路径已测、重启要求标注；`pnpm run i18n:check` 不涉及
- [x] 步骤 3：提交（`docs: add multi-instance management guide`）

---

## 验证清单

- [x] Task 1–9 各自测试命令绿（见各 task 步骤）
- [x] `make -C apps/backend test`：与基线逐名 diff 零新增失败（串行）
- [x] `pnpm --filter @kandev/web test` + `run typecheck` + `run lint`：零新增
- [x] `pnpm run i18n:check`（apps/web）绿
- [x] `pnpm e2e:run --grep multi-instance` 绿
- [ ] 手动冒烟（可选）：本机 + 3090Ti 真实双实例注册/切换/操作/通知
- [x] `openspec validate add-multi-instance-management --strict --no-interactive` 绿

## apply 阶段状态恢复要求

- 按 Task 1→11 顺序推进；每 task 结束必须：测试绿 → 提交（conventional commit）→ 勾选本文件对应项 → ledger 记录（`openspec-apply-change` 流程）。
- 中断恢复：先 `git status` + 本文件勾选状态定位断点；只重跑当前 task 的验证命令，不重跑全量。
- 每个 task 的 Interfaces 块是跨 task 契约：改动签名必须回写本文件与相邻 task 引用。
- 全部 task 完成后进入 `openspec-verify-change`（漂移检测）→ `openspec-archive-change`。
