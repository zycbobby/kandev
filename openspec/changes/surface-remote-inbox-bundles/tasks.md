# surface-remote-inbox-bundles — Implementation Tasks

> **For agentic workers:** REQUIRED SUB-SKILL: Use openspec-apply-change to implement
> this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Apply 按
> task 顺序推进；每 task 完成即勾选并 commit；全部完成后进入 verify → archive。

**Goal:** 本机 Inbox 组合已注册启用远端实例的 answerable clarification bundles（每实例一页，就地回答/转发 sidecar、降级态、计数、跳转），远端行不落任何本地表。

**Architecture:** 见 `design.md`——本机后端 fan-out（`internal/instances/remoteinbox` 直连远端 + backendapp `/api/v1/instances/:id/…` 转发路由），SPA 仅本机视图渲染远端 section，共享答案组件 additive `respondTarget` 覆盖，收敛 = 远端页 re-read。

**Spec:** `openspec/changes/surface-remote-inbox-bundles/proposal.md` + `specs/cross-instance-inbox/spec.md`（spec 的 6 组 Requirement 逐一映射到下方任务）。

## Global Constraints

- 提交：Conventional Commits（header ≤100 字符），结尾 `Co-Authored-By: Claude Code <noreply@anthropic.com>`；pre-commit 可能重排 TS/Go 文件，重加后新 commit（不 amend）。
- Go：改动文件过 `bash scripts/lint-go-changed <files>`（函数 ≤80 行等 golangci 约束）；提交前 `gofmt -w`。
- 前端：新 copy 一律 `t()`；7 语言（en/pt-pt/zh-cn/zh-hk/zh-tw/ja/ko）+ pseudo，zh-hant 对用 `pnpm run i18n:zh-hant`；禁 Unicode em dash；`pnpm run i18n:check` 必须过。
- e2e：因果等待只用 `waitForHttp`/`watchWs`/`expect.poll`/`dwell`（e2e/helpers/causal-waits），禁裸 sleep；本地套件不叠加全量运行。
- pnpm 命令从 `apps/`（或 `apps/web`）运行；Go 命令从 `apps/backend` 运行。
- 门控语义：远端面仅在 `features.needsYouInbox && features.multiInstance` 同时开时存在；任一关 = 与现状逐字节一致。

## Review Focus

实现外但最可能咬人的五类输入，测试逐一钉住：

1. **远端被抢先回答**：本地展开行提交得 409/claimed:false → 行经 re-read 移除、无失败误报（T8 图2 断言 + T3 envelope 解析用例）。
2. **跨实例 pending_id 相同**：两个实例各自持有同 ID bundle，转发必须只命中所属实例（T2 双 fake 用例）。
3. **降级瞬间提交**：远端刚死时提交 → 502 → 行保留、已输入答案保留、可重试（T3 error path + T8 图5）。
4. **角标与列表不一致**：远端页晚于本机页落地时角标不得计入未加载实例（T5 count-known 用例）。
5. **看远端实例时误发 fan-out**：面板切到远端后不得向 proxy base 发实例页请求（T6 gating 用例）。

## Verification Strategy

- **主验收 = e2e-script**：`apps/web/e2e/tests/multi-instance/remote-inbox.spec.ts`（双后端真实闭环，T8）。
- **次验收 = integration-test**：backend `remoteinbox` + backendapp 路由测试（httptest 假远端，T1/T2）；前端 vitest（slice/controller/组件，T3/T5/T6/T7）。
- unit-test 仅作实现过程支撑，不作为唯一验收。TDD：每 task 先写失败测试再实现。

## E2E 测试图

| 图 | 场景 | 断言要点 |
|---|---|---|
| 1 | 注册远端（远端 seed `/e2e:clarification` 至 WAITING_FOR_INPUT）后打开本机 Inbox | 远端 section 列出该 bundle（实例名可见），本机 section 不受影响 |
| 2 | 就地展开并提交答案 | 远端会话恢复（远端 client `waitForSessionDone`）、行移除、角标回落 |
| 3 | 未打开 Inbox 的普通路由 fresh load | 侧边栏角标含远端行数（count 在路由挂载前正确） |
| 4 | 远端行 open-task 控件 | URL 变为 `/t/{remoteTaskId}?kandev_instance={id}`，远端会话页加载 |
| 5 | kill 远端 → section 降级；本机行照常；重启远端 + Retry 恢复 | 降级态命名实例、可重试、无答案组件；恢复后行回来 |
| 6 | 本机 snooze 远端行 → 远端 hidden 列表出现；本机 restore → 行回归 | sidecar 落远端（远端 client 直查 hidden 端点），本地无表 |
| 7 | 远端 backend 关 `features.needsYouInbox`（fixture env 覆盖） | section 降级为 `inbox_disabled` 理由文案 |

## 实现清单

### Task 1: `remoteinbox` 客户端与聚合器

**Files:**
- Create: `apps/backend/internal/instances/remoteinbox/types.go`, `client.go`, `aggregate.go`
- Test: `apps/backend/internal/instances/remoteinbox/client_test.go`, `aggregate_test.go`

**Interfaces:**
- Consumes: `instances.Instance`（registry 行）、credential 闭包（`instance_proxies.go:97-105` 模式：`CredentialSecretID == ""` → 返回 `("", nil)`，否则 `secretadapter.Reveal`）。
- Produces: `remoteinbox.New(inst, credential, *http.Client) *Client`；`Client.ReadInbox/ReadHidden(ctx, limit) ReadResult`；`Client.Respond(ctx, pendingID, body) (int, []byte, error)`；`Client.UpsertSidecar(ctx, pendingID, state, duration) (int, error)`；`Client.DeleteSidecar(ctx, pendingID) (int, error)`；`ReadResult{Page *Page; Degraded Reason}`；`Reason` 闭集 `unreachable|unauthorized|inbox_disabled|error`（design.md Contracts 节的准确形状）。

- [x] **Step 1: 写失败测试**（httptest 假远端：workspaces 列表 + 每 ws inbox/hidden 页 + respond/sidecar 端点）
  用例：`TestReadInboxAggregatesAcrossWorkspaces`（跨 ws 合并、`created_at` 升序 + `pending_id` 决胜、cap=limit、任一 ws `has_more` 或溢出 → `HasMore`、行带 `InstanceID/WorkspaceID`）；`TestReadInboxDegradedReasons`（拨号失败→unreachable；401→unauthorized；inbox 路由 404→inbox_disabled；500→error；零 ws → ok 空页）；`TestRespondPassthrough`（200 win/loss envelope 字节透传、409 `not_active` 状态码透传）；`TestSidecarForwarding`（PUT/DELETE 透传 204/400/404）；`TestCredentialInjection`（有 PAT → `Authorization: Bearer`；无 PAT → 仍拨号无头）；`TestReadDeadline`（挂起远端按 ctx deadline 归 unreachable）。
- [x] **Step 2: 跑测确认失败**：`cd apps/backend && go test ./internal/instances/remoteinbox/...`（期望：包不存在/未定义）
- [x] **Step 3: 实现** `types.go`（wire 形状 + Reason）、`client.go`（单实例拨号：workspaces → 逐 ws 串行读、10s 总 deadline、PAT 注入、日志只带实例名）、`aggregate.go`（合并/cap/has_more、hidden 聚合 total=min、`next_snooze_expiry` 取 MIN）。函数守 golangci 限制，聚合拆小函数。
- [x] **Step 4: 跑测通过**：同 Step 2（期望 PASS）
- [x] **Step 5: Lint + commit**：`bash scripts/lint-go-changed apps/backend/internal/instances/remoteinbox/*.go`；`git commit -m "feat(instances): add remote clarification-inbox fan-out client"`

### Task 2: backendapp 实例 inbox 路由

**Files:**
- Create: `apps/backend/internal/backendapp/instance_inbox_routes.go`
- Modify: `apps/backend/internal/backendapp/helpers.go`（注册调用一处，紧邻 `registerInstanceRoutes(p)`）
- Test: `apps/backend/internal/backendapp/instance_inbox_routes_test.go`

**Interfaces:**
- Consumes: T1 的 `*remoteinbox.Client` 方法集（以窄接口 `remoteInboxForwarder` 注入，测试用 fake，不碰网）；`instanceCallerUserID`/`visibleInstance`；`p.features.NeedsYouInbox`。
- Produces（HTTP，供 T4 消费）: `GET/PUT/DELETE /api/v1/instances/:id/clarification-inbox…`、`POST /api/v1/instances/:id/clarification/:pendingID/respond`，envelope 与状态映射见 design.md Contracts 表。

- [x] **Step 1: 写失败测试**：`TestRoutesGatedByNeedsYouInboxFlag`（flag off → 全部 404）；`TestForeignInstanceIs404`（他人实例与不存在同形 404）；`TestInstancePageEnvelope`（ok 页含 `instance_id/instance_name/bundles[].workspace_id/count==len(bundles)`；degraded 页 200 + `status:"degraded"` + 理由，bundles 空）；`TestRespondForwardingStatusMapping`（远端 200/409 透传、拨号失败 502 `{"error":"remote_unreachable","instance":…}`）；`TestSidecarForwarding`（204/404 透传）；`TestTwoInstancesSamePendingIDIsolated`（两个 fake，同 pendingID 各自命中）。
- [x] **Step 2: 跑测确认失败**：`cd apps/backend && go test ./internal/backendapp/ -run TestInstanceInbox -v`
- [x] **Step 3: 实现** 路由 + envelope 组装 + 状态映射；helpers.go 装配（门控 `p.services.Instances != nil && p.features.NeedsYouInbox`；client 构造复用 secretadapter credential 闭包）。
- [x] **Step 4: 跑测通过** + `go test ./internal/backendapp/ -count=1`（回归）
- [x] **Step 5: Lint + commit**：`feat(instances): mount remote clarification-inbox routes`

### Task 3: 共享答案组件 `respondTarget` 覆盖

**Files:**
- Modify: `apps/web/hooks/domains/session/use-clarification-group.ts`（options 参数 + `postClarification` 目标选择）
- Modify: `apps/web/components/task/chat/clarification-input-overlay.tsx`、`clarification-panel-section.tsx`（可选 prop 透传）
- Test: 既有测试文件追加用例（hooks 与组件各自 test 文件）

**Interfaces:**
- Produces: `useClarificationGroup(messages, onOutcome?, onLateAnswer?, options?: { respondTarget?: (pendingId: string) => string })`；`ClarificationPanelSection`/`ClarificationInputOverlay` 增可选 prop `respondTarget?: (pendingId: string) => string`。本机 host 不传 → URL 与现状逐字节一致。

- [x] **Step 1: 写失败测试**：fetch spy 断言——不传 options → 请求 `${apiBaseUrl}/api/v1/clarification/:id/respond`；传 `respondTarget` → 请求覆盖 URL、envelope（win/loss/409）解析与现状一致；组件测试：prop 从 PanelSection 透传到 hook；本机 host（任务会话/Quick Chat 现有测试）不传时渲染与回调不变（AC .39 断言）。
- [x] **Step 2: 跑测确认失败**：`cd apps/web && pnpm vitest run hooks/domains/session/use-clarification-group components/task/chat`
- [x] **Step 3: 实现**（纯 additive：默认值 = 现状 URL；不触碰 onLateAnswer/onResolved 语义）
- [x] **Step 4: 跑测通过** + `pnpm run typecheck`
- [x] **Step 5: Commit**：`feat(web): add respond-target override to clarification answer chain`

### Task 4: 前端 API client 与类型

**Files:**
- Create: `apps/web/lib/types/remote-inbox.ts`、`apps/web/lib/api/domains/remote-inbox-api.ts`
- Test: `apps/web/lib/api/domains/remote-inbox-api.test.ts`

**Interfaces:**
- Consumes: T2 的 HTTP 契约；`getLocalBackendConfig().apiBaseUrl`（instances-api 同款 local-base 护栏）。
- Produces: `RemoteInboxPage`/`RemoteInboxBundle`（= `ClarificationInboxBundle` + `instance_id` + `workspace_id`）/`RemoteInboxDegradedReason` 类型；`listRemoteClarificationInbox(instanceId)`、`listRemoteHiddenClarificationInbox(instanceId)`、`respondRemoteClarification(instanceId, pendingId, body)`、`dismissRemote/snoozeRemote/restoreRemoteClarificationBundle(instanceId, pendingId[, duration])`。

- [x] **Step 1: 写失败测试**：URL 构建落 local base（含显式 baseUrl 覆盖仍优先 caller）、路径与 query 形状、respond body 透传。
- [x] **Step 2: 跑测确认失败** → **Step 3: 实现**（fetchJson 包装，仿 clarification-inbox-api）→ **Step 4: 跑测通过 + typecheck**
- [x] **Step 5: Commit**：`feat(web): add remote clarification-inbox api client`

### Task 5: slice 与组合角标 selector

**Files:**
- Modify: `apps/web/lib/state/slices/needs-you-inbox/{types.ts,needs-you-inbox-slice.ts,selectors.ts,index.ts}`
- Test: `apps/web/lib/state/slices/needs-you-inbox/needs-you-inbox-slice.test.ts`（追加）

**Interfaces:**
- Produces: `RemoteInboxInstanceState`（design.md 形状）与 `remoteByInstanceId`；actions `beginRemoteInboxRead(instanceId)→generation`、`setRemoteInboxPage(instanceId, generation, page)`、`setRemoteInboxDegraded(instanceId, generation, reason)`、`bumpRemoteInboxRefreshTick(instanceId?)`、`bumpRemoteInboxRetryTick(instanceId)`；selectors `selectRemoteInboxInstances`、`selectComposedNeedsYouCount`（本机已加载页 + Σ 已落地远端页；未加载/降级贡献 0）、`selectComposedNeedsYouHasMore`。

- [x] **Step 1: 写失败测试**：远端页 set/generation 陈旧丢弃/degraded 落态；composed count 用例——仅本机 → 本机值；本机+1 远端已落地 → 和；远端 loading/degraded → 不计入；hasMore 任一真即真；boot seeding 路径不受影响（现状用例保持绿）。
- [x] **Step 2: 跑测确认失败** → **Step 3: 实现**（stale-response 纪律复制到实例键）→ **Step 4: 跑测通过**
- [x] **Step 5: Commit**：`feat(web): add remote instance pages to needs-you inbox slice`

### Task 6: 远端 inbox controller

**Files:**
- Create: `apps/web/hooks/domains/needs-you-inbox/use-remote-inbox-controller.ts`
- Test: `apps/web/hooks/domains/needs-you-inbox/use-remote-inbox-controller.test.tsx`

**Interfaces:**
- Consumes: T4 client、T5 actions/selectors、`NeedsYouInboxRefreshCoordinator`（scopeKey 含 instanceId）、`useForegroundRefresh`、`getWebSocketClient().on("remote_notification", …)`。
- Produces: `useRemoteInboxController()`（App shell 挂载一次）；gating = `needsYouInbox && multiInstance && activeInstanceId === LOCAL_INSTANCE_ID`；触发集 = 挂载/实例清单变化、`remote_notification`（`event === "session.clarification_requested"` → bump 该 instance）、前台恢复、60s 周期（仅可见）、每实例 retry tick、每实例 snooze 到期定时器（5s 下限 + 1s buffer，沿用本机常量）。

- [x] **Step 1: 写失败测试**：gating 三假（flag 关×2、看远端实例）→ 不发请求；挂载 → 每启用实例一读；`remote_notification` bump 只刷新 payload.instance_id；retry tick → 单实例手动重试；陈旧响应（跨实例 generation）丢弃。
- [x] **Step 2: 跑测确认失败** → **Step 3: 实现**（每实例一个 coordinator；controller 于 app shell 挂载点接线属 T7）→ **Step 4: 跑测通过**
- [x] **Step 5: Commit**：`feat(web): add remote inbox refresh controller`

### Task 7: UI——section/行/角标 + i18n

**Files:**
- Create: `apps/web/components/needs-you-inbox/remote-inbox-section.tsx`（+test）
- Modify: `apps/web/components/needs-you-inbox/needs-you-inbox-row.tsx`（可选 `source` prop：实例 chip、open-task 走 `switchInstance(instanceId, linkToTask(taskId))`、展开传 `respondTarget` 且不传 onLateAnswer）；`apps/web/components/needs-you-inbox/needs-you-inbox-hidden-panel.tsx`（可选 source，restore 走 T4 转发）；`apps/web/app/needs-you-inbox/needs-you-inbox-page-client.tsx`（needs-you tab 本机列表后挂 `RemoteInboxSection`×启用实例；tab/页头改 composed selector）；`apps/web/components/app-sidebar/app-sidebar-primary-nav.tsx`、`apps/web/components/navigation/mobile-sidebar-layout-navigation.tsx`（角标改 composed selector）；App shell 挂载 `useRemoteInboxController()`。
- Locale: `apps/web/src/locales/{en,pt-pt,zh-cn,zh-hk,zh-tw,ja,ko,pseudo}/`（needs-you-inbox 命名空间新增 key：远端 section 标题/空态/四条降级文案+重试/隐藏计数/实例 chip 等）

- [x] **Step 1: 写失败测试**（组件级）：section 三态（行列表含实例名、空态、降级态——命名实例 + Retry 按钮且**不渲染**答案组件）；远端行 chip 与 open-task 调 `switchInstance`（mock）；hidden panel source 路由到转发 API；侧边栏角标渲染 composed 值。
- [x] **Step 2: 跑测确认失败**：`cd apps/web && pnpm vitest run components/needs-you-inbox app/needs-you-inbox`
- [x] **Step 3: 实现** + 7 语言 + pseudo（`pnpm run i18n:zh-hant` 生成 zh-hk/zh-tw；无 em dash）
- [x] **Step 4: 跑测通过 + typecheck + `pnpm run i18n:check`**
- [x] **Step 5: Commit**：`feat(web): render remote instance sections in needs-you inbox`

### Task 8: E2E spec

**Files:**
- Create: `apps/web/e2e/tests/multi-instance/remote-inbox.spec.ts`
- Modify（如需）: `apps/web/e2e/fixtures/remote-backend.ts`（澄清 seed helper：远端 client `createTaskWithAgent` + `/e2e:clarification` + `waitForSessionState WAITING_FOR_INPUT`；env 覆盖支持关远端 needsYouInbox）

- [x] **Step 1: 实现 E2E 测试图 1-7**（结构仿 `multi-instance.spec.ts`：`bootRemoteBackend` + 注册 + `backend.restart()` 拉起 poller/proxy/relay + `backend.useEnv` 作用域 flag；等待全部走 causal-waits）。
- [x] **Step 2: 跑通**：`cd apps/web && pnpm e2e:run -- tests/multi-instance/remote-inbox.spec.ts`（期望全绿；shard 纪律：不与其他全量套件叠跑）
- [x] **Step 3: Commit**：`test: add remote inbox e2e flows`（补充覆盖见 `a9e001a396`）

### Task 9: 决策与规格文档

**Files:**
- Modify: `docs/decisions/2026-10-28-multi-instance-federation.md`（Status 行 `amended 2026-10-28` + Decision 3 修订段：允许 Inbox 组合读远端澄清状态，通知再发布规则不变）；`docs/specs/ui/requirements/needs-you-inbox.md`（新增 REQ-UI-NEEDS-YOU-INBOX-002 远端实例行 + .5/.12/.20/.40 的远端豁免措辞）；`docs/specs/ui/system-design/needs-you-inbox-0{1,2,3}.md`（实例页契约/远端触发集与 count-known/copy 契约）；`apps/backend/internal/instances/AGENTS.md`（layout 增 remoteinbox，Decision-3 相邻不变量措辞修订）；`docs/public/multi-instance.md`（用户指南一段）。

- [x] **Step 1: 写入修订**（英文工程文本；产品 locale 不动）
- [x] **Step 2: 校验**：`uv run --no-project --python 3.12 python scripts/list-docs.py validate`、`… scripts/lint-spec-files.py --all`、`cd apps/web && pnpm run i18n:check`、`openspec validate surface-remote-inbox-bundles --strict --no-interactive`
- [x] **Step 3: Commit**：`docs: amend federation decision and needs-you inbox specs for remote rows`

## 验证清单

- [x] `cd apps/backend && go test ./internal/instances/... ./internal/backendapp/ -count=1` 全绿；`bash scripts/lint-go-changed <改动 go 文件>` 无告警。
- [x] `cd apps/web && pnpm vitest run`（受影响域）+ `pnpm run typecheck` + `pnpm run lint` 通过。
- [x] `pnpm run i18n:check` 通过（含 pseudo、无 em dash、7 语言完整）。
- [x] `pnpm e2e:run -- tests/multi-instance/remote-inbox.spec.ts` 全绿（图 1-7）。
- [x] 文档校验四连（list-docs validate / lint-spec-files / i18n:check / openspec validate --strict）通过。
- [x] spec delta 六组 Requirement 逐条映射：组合读→T1/T2/T7、就地回答→T3/T8 图2、sidecar 转发→T1/T2/T8 图6、计数→T5/T7/T8 图3、降级→T1/T2/T7/T8 图5/7、跳转→T7/T8 图4；双 flag 关闭惰性→T2/T6 + 既有 flag-off e2e。
- [x] 数据隔离终检：`git grep` 确认无远端行写入本地 task/board/sidecar 表的代码路径。

## Apply 阶段状态恢复要求

- 按 Task 1→9 顺序推进；每 task 完成即勾选 + commit（Conventional Commits），中断后以 tasks.md 勾选与 git log 恢复现场。
- 设计冲突（契约与实现相抵触）停下提问，不静默改契约；全部 task 完成后进入 verify（openspec-verify-change）→ archive。
