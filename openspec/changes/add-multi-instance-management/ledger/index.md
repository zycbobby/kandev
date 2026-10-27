# add-multi-instance-management — apply ledger

- **executor**: `subagent-driven`（用户指令预选，非 auto）。每 task 一个 implementer 子代理，只拿 work packet（任务原文 + 允许修改文件 + 绑定性 Global Constraints）；按用户指令不开默认 per-task spec/quality 双审子代理，验收门 = 各 task 的验证命令由 controller 复跑核验 + 本文件记录。
- **编排**: superpowers:subagent-driven-development；briefs/reports/review packages 在 `.superpowers/sdd/tasks/`（git-ignored）。
- **计划基线 BASE**: `b120d90fa`（branch `feature/multi-instance-kande-47e`）。
- **环境**: `pnpm -C apps install --frozen-lockfile` 已于 apply 开始前完成；`go`/`gofmt` 不在 PATH，在 `/usr/local/go/bin/`；shell 禁 `cd`（gvm 报错），一律 `-C`/绝对路径。

## Pre-flight scan（任务间共享面冲突检查，SDD setup）

| # | 共享面 | 涉及 task | 发现 | 裁决 |
|---|---|---|---|---|
| 1 | `/api/v1/instances` CRUD+health REST 路由组 | T6 提到"select 端点在 instances 路由组"、T7 前端消费 `/api/v1/instances` CRUD+health，但 T1–T5 无一显式创建该路由组 | 计划缝隙：instances 路由组归属未显式指派 | **Ruling**：路由组（CRUD + health + select，settings 鉴权、flag 门控）在 **Task 6** 随 select 端点一并落地——design.md 文件结构把"路由"归 backendapp 装配，T6 是首个动 instances HTTP 面的 task；T7 只做前端。若错：路由形状与 `instances-api.ts` 不匹配，由 T7 vitest 与 T10 e2e 兜住。 |
| 2 | `store/sqlite/store.go` | T1 创建（两表 schema）、T5 扩展 occurrence 方法 | 一致（T1 明示 occurrence 方法归 T5 扩展同文件） | 无需裁决 |
| 3 | `internal/backendapp/` 装配 | T4（ProxyManager）、T5（RelayManager + 本机 remote-notifications REST）、T6（PayloadBuilder + select/instances 路由）先后修改 | 顺序串行执行，无并行冲突 | 无需裁决 |
| 4 | 桥接能力 cookie `kandev_bridge_<instanceID>` | T4 校验（requireBridgeAuth）、T6 写入（select 端点） | 命名一致（同一实例作用域 HMAC 能力模式） | 无需裁决 |
| 5 | WS action `remote_notification` | T5 后端常量（pkg/websocket/message.go）、T9 前端 handler | 一致 | 无需裁决 |
| 6 | app-sidebar 挂点 | T8（header/picker/instances slice）、T9（notifications section/角标） | 不同文件，均含手机导航挂点 | 无需裁决 |
| 7 | notifications 新表 vs schema owner 纪律 | T1 纪律"新 SQL schema owner 注册"仅适用新 owner（instances）；T3 表进 notifications 既有 owner 体系 | T3 文本自洽（跑 sqlguard 验证即可） | 无需裁决 |
| 8 | T1 flag 注册 vs T6/T8/T10 flag-off 断言 | 均消费 `features.multiInstance`，默认 false | 顺序覆盖 | 无需裁决 |

## Per-Task Ledger

| task id | executor | 验证方法 | 结果 | 状态 | 时间戳 |
|---|---|---|---|---|---|
| Task 1 | subagent-driven (sonnet implementer + controller 复核) | brief 步骤 4 三命令由 controller 独立复跑（instances ok / sqlguard clean / storeconformance -race ok 38.7s）+ controller 通读全量 diff（review-b120d90fa..5f2fdd081.diff） | 全绿；commit `5f2fdd081`（19 文件 +660/-18）；TDD RED/GREEN 证据齐全；三处偏差均为仓库现实强制（e2e_reset 实际在 `internal/backendapp/`、requiredstores 联动 boot wiring + v0.93.0 manifest、service 层 fail-closed flag 门） | done | 2026-10-27 |
| Task 2 | subagent-driven (sonnet implementer + controller 复核) | `go -C apps/backend test -race ./internal/instances/...`（controller 复跑 ok 1.3s）+ sqlguard exit 0 + controller 通读 diff（review-5f2fdd081..cb3bc9efc.diff） | 全绿；commit `cb3bc9efc`（4 文件 +460/-5）；TDD RED/GREEN 齐全；`MinRemoteVersion="0.98.0"`（v0.97.0 之后下一 minor，本 change 发布版）；seam：Repository 增 `ListAllInstances`（internal-only，文档化） | done | 2026-10-27 |
| Task 3 | subagent-driven (sonnet implementer + controller 复核，含 1 轮修复) | `go -C apps/backend test ./internal/notifications/...`（controller 复跑：仅 2 条已知 macOS 基线失败，零新增）+ sqlguard exit 0 + storeconformance -race ok + requiredstores ok + controller 通读 diff | 全绿；commits `7a2b4d11b`（13 文件 +932/-6）+ 修复轮 `001cfacbf`（conformance 覆盖：RequiredTables + v0.93.0 manifest + PG-gated 测试）；TDD 齐全；见 Task 3 备注（office.inbox_item 裁决 + 两处已接受偏差） | done | 2026-10-27 |
| Task 4 | subagent-driven (opus implementer + controller 复核，含 1 轮 NEEDS_CONTEXT 裁决) | `go -C apps/backend test -race ./internal/instances/...`（controller 复跑 ok）+ backendapp 恰为已知 10 条环境基线失败（两名 implementer 分别在 pristine HEAD / 原始提交验证同名，零新增）+ sqlguard exit 0 + controller 通读全量 diff | 全绿；commit `eb6756552`（14 文件 +988/-4）；五测试 + cookie 往返 + 安全附加测试；cookie 契约：`kandev_bridge_<id>` host-only/HttpOnly/Lax/900s，JSON `{"i","e"}`+HMAC-SHA256（进程随机 32B 密钥），滑动 15min，重启失效；已接受偏差：WS 桥上游 Authorization 为 PAT 注入而非浏览器凭据透传（spec 对齐，防本地凭据泄露远端）；report 文件因 child harness 规则未写（契约与证据在 diff + 最终消息） | done | 2026-10-27 |
| Task 5 | subagent-driven (opus implementer + controller 复核，含 1 轮 NEEDS_CONTEXT 裁决×2) | `go -C apps/backend test -race ./internal/instances/...`（controller 复跑：instances/proxy/relay/store/sqlite 四包全 ok）+ backendapp 恰 10 条已知基线 + sqlguard exit 0 + storeconformance -race ok + controller 通读全量 diff + 契约核对（订阅路由/payload 字段/SQL 方言 ON CONFLICT DO NOTHING 双兼容） | 全绿；commit `048938685`（12 文件 +938/-0）；五测试 + 边界测试（卡秒边界/漏收窗口/重连退避/parked）；两项裁决落实（专用对账游标 + 有界同秒恢复）；本地通知 ID = (instance,event,occurrence) 的 SHA1 UUID，行与广播共用 → read/dismiss 寻址一致；report 文件因 child harness 规则未写 | done | 2026-10-27 |
| Task 6 | subagent-driven (sonnet implementer + controller 复核，含 1 轮布局修复) | `go -C apps/backend test ./internal/webapp/... ./internal/instances/...`（controller 复跑五包 ok）+ backendapp 恰 10 条已知基线（implementer 双次串行 + throwaway HEAD worktree 逐名 diff）+ 焦点测试复跑 ok + controller 通读合并 diff（含修复轮纯度证据：helpers.go -234/+0 字节级一致移动） | 全绿；commits `1a4dd1317`（8 文件 +867/-5）+ `038ae35be`（拆分 instance_routes.go）；三 brief 测试 + 路由/解析矩阵/端到端 HTML/poller 装配/AST 组合测试；REST 形状按裁决钉死（GET/POST /api/v1/instances、PATCH/DELETE /:id、GET /:id/health、POST /:id/select；flag off 全 404）；已接受解释：无 live 代理 → 整体回落本地 boot；匿名 shell 访客不泄实例名（本地 boot）；kandev_instance 为 session cookie（spec 仅钉 F5） | done | 2026-10-27 |
| Task 7 | subagent-driven (sonnet implementer + controller 复核) | controller 独立复跑：scoped vitest 21/21（隔离串行重跑；先前失败系与 tsc 并发抢资源）+ i18n:check 四项 ✓ + typecheck exit 0 + em-dash grep 0 + 通读 api client/组件/注册 diff | 全绿；commit `d9621f2dc`（26 文件 +1768/-2，含七语言 catalog）；页面挂 /settings/system/instances（System 区 discovery + feature 门控）；api client 与后端契约逐字对齐；native-catalog.test.ts 1 条本机预存环境失败（与 HEAD 逐字节一致） | done | 2026-10-27 |
| Task 8 | subagent-driven (sonnet implementer + controller 复核，含 1 轮修复) | controller 复跑：Go 焦点测试 ok（webapp+backendapp -run 'Shell\|Instance\|Select'）+ 前端核心套件 4 文件 23/23 + config/switch-instance diff 逐字对裁决 + implementer 全量触面控制（18+6 套件 184 测试、tsc、eslint、i18n:check 全绿） | 全绿；commits `084aa3a87`（59 文件 +1904/-106 含修复轮）+ `a413646dd`（清选 + 预览面降级）；三项裁决全落地（getLocalBackendConfig 双 base、shell 注入 `__KANDEV_INSTANCE_ID`、单 WS）；宿主绑定降级覆盖 folder/editor/file-tree/port-forward/预览 iframe/隧道开关；store 四注册 seam 机械性扩展（已接受）；切回本地 = select 路由特判 `local` 清 cookie（无 query pin，F5 保持本地） | done | 2026-10-28 |
| Task 9 | subagent-driven (sonnet implementer + controller 复核) | controller 复跑：6 文件 75 测试全绿（含现有 notifications handler 回归套件）+ tsc exit 0 + i18n:check 6✓ + D9 红线 diff-stat 验证（notifications.ts/sonner/sound 零触碰）+ handler 门控 diff 审查（activeId==='local' 才写 slice） | 全绿；commit `dd09a4d3f`（33 文件 +1712/-13）；四处结构性必要扩展已接受（app-sidebar 桌面挂点、BackendMessageMap 类型项、use-remote-notifications hook、default-state 注释式惰性项——该文件 599/600 行预算）；点击条目 → switchInstance(instance_id, /t/task_id) + 标记已读；角标接线 picker 预留槽 | done | 2026-10-28 |
| Task 10 | subagent-driven (sonnet implementer + controller 复核，含 2 轮修复) | controller 独立复跑 e2e 门禁：`pnpm -C apps/web run e2e:run -- --grep "multi-instance"` **2 passed**（B flag-off 诚实默认态，完整流 12.9s + flag-off 惰性 969ms）；两个产品 bug 修复 diff 由 controller 逐行审查通过；门控 hook 与预检头修复审查通过 | 全绿；commits `f6d2fed54`（spec+fixture+README+两个产品修复）+ `6703cadd5`（本地 flag 门控）；E2E 揪出并修复两个真实 bug（selector 快照缓存、代理 CORS 权威归属）——见 Task 10 备注；`use-local-feature-flag` hook（本地 /api/v1/features 会话缓存）解 flag-off 远端被困 | done | 2026-10-28 |
| Task 11 | subagent-driven (sonnet implementer + controller 复核，含 1 轮互链修复) | 灰度清单 a/b/c 对照代码逐项核验全 PASS（元数据完整且被 completeness 测试强制、四层 disabled 测试、RestartRequired=true）；docs 验证器绿（56 页 63 检查 0 失败 0 em-dash） | done；commits `3ee84ac68`（multi-instance.md 89 行 + feature-status + meta + coverage）+ `2617af64f`（stats-sync/use-kandev 互链）；coverage.json 修改为验证器硬性要求（HEAD 在本 task 前即验证失败，属既有欠账被一并补齐）；Parked 项（60s 对账默认投递、office.inbox_item 休眠路径）均已写入文档限制节 | done | 2026-10-28 |

## Exit Gate（Step 4 准出门禁，controller 于 2026-10-28 执行）

**前置检查**：无 `reviews/state.json`（无 collecting review session，无 applyBlocked）。

1. **Verification Strategy 入口（首选 e2e-script）——通过**：`pnpm -C apps/web run e2e:run -- --grep "multi-instance"` 由 controller 独立复跑，**2 passed**（`multi-instance register, switch, chat, notify, read, and kill flows` 12.9s 覆盖测试图 1–7；`flag off keeps the federation surface inert` 969ms 覆盖图 8；fixture B 为 flag-off 诚实默认态 + 版本 stamp ≥ MinRemoteVersion）。次选集成层：后端 instances/proxy/relay/storeconformance 全包 -race 绿 + 前端各域 vitest 套件绿（各 task 步骤记录）。
2. **`openspec validate add-multi-instance-management --strict --no-interactive`——通过**（"Change is valid"）。
3. **验收方法与证据**：
   - **后端全量基线 diff（串行两轮，b120d90fa 基线 worktree vs 当前树）**：基线 256 个失败用例（本机既知必红基线），当前 257；逐名 diff 出 3 个「新增」全部为全量负载 flake（`TestGitOperatorPush…`/`TestRestoreManagedBranch…`/`TestStopTunnel…` 隔离复跑全 ok，与改动无代码交集；基线侧另有 2 个反向 flake）→ **零新增失败**。
   - **web 全量基线 diff（串行两轮）**：基线 24 个失败文件，当前 13；唯一「新增」`task-chat.comment-send.test.tsx`（browser-mode）隔离复跑 21/21 ok → **零新增失败**。
   - **typecheck**：`tsc --noEmit` exit 0；**全量 lint**：`eslint --max-warnings 0` exit 0；**i18n:check**：全部子检查 ✓（多次复跑）。
   - 手动冒烟（可选项）未执行（optional，留待用户 3090Ti 实机验证）。

**结论**：三项门禁全部满足，apply 阶段完成。下一步 `openspec-verify-change`（漂移检测）→ `openspec-archive-change`。

### Task 1 备注（供后续 task 承接）

- 前瞻（非缺陷）：`remote_notification_occurrences` DDL 目前只有 `id/owner_user_id/instance_id/event/occurrence_id/created_at` + 唯一键；Task 5 的 `Record/ListOccurrences/MarkOccurrenceRead` 需要 `task_id/session_id/title/body` 与已读/忽略列 → Task 5 在同文件扩展 CREATE TABLE（该表首发于本 change，pre-release 扩展安全，仍守幂等纪律）。
- `Repositories.Instances`（backendapp/types.go）与 `instances.NewService(repo, enabled)` 是后续装配入口；flag 断言可用 `profiles.FeatureFlagDefaults()["multi_instance"]`。

### Task 2 备注（供后续 task 承接）

- **Parked (deferred minor)**：健康探测无凭据——auth 开启的远端对 `GET /api/v1/system/info` 回 401，报告为 Reachable=false + "system info returned status 401"。Ruling：保持现状（v1 目标为无认证远端，用户拍板 #2；spec 未覆盖 auth-on 健康场景）。Task 4 拿到 credential resolver 后如顺手可让探测带 PAT；否则留给 verify-change 裁决。
- **Task 10 承接**：e2e 远端二进制是 mid-development git-describe 版本（核心三元组低于 floor）→ fixture 用 Makefile `VERSION ?=` 覆盖 stamp 版本（≥ `MinRemoteVersion`）。
- 健康态为进程内存（不持久化，设计如此）；`MinRemoteVersion="0.98.0"` 若发布编号变化需一行 bump（测试从常量派生，无需改测试）。

### Task 3 备注（供后续 task 承接）

- **Ruling（office.inbox_item 无 occurrence 身份）**：调查结论——`events.OfficeInboxItem`（"office.inbox.item"）bus topic 在全仓库**无生产发布方**（对照组：`OfficeTaskUpdated`/`OfficeRunProcessed` 等均有真实 publisher），即 `office.inbox_item` 通知在远端是休眠路径；`HandleInboxItem` 经 `dispatchGenericNotification` 分发，`Message.OccurrenceID` 恒为空。给它 occurrence 身份 = 修改既有通知平台行为，被 proposal 显式排除（Modified Capabilities: 无）。**裁决**：(1) relay 封闭集保留 `office.inbox_item`（spec 字面，前向兼容）；(2) **Task 5 relay 只投递携带 occurrence ID 的帧**——无 ID 的帧忽略（fail closed）并记日志，绝不用空 occurrence 键入库（空键会让同实例全部 inbox 帧坍缩成一条）；(3) 对账端点对 inbox_item 无行（Task 3 已按语义边界实现，正确）；(4) Task 11 文档写明该限制。若错：用户若期望 v1 收到远端 inbox 通知，可在 review 推翻，补齐上游 occurrence 身份是干净的后续 change。
- 已接受偏差：`controller/controller.go` +17 行（REST 三层惯例所需）；留存清扫为 store 构造启动清扫 + dispatch/list 触发的 1h 节流清扫（无 goroutine，`maintenance.ForPool` 按 `*db.Pool` 键控而 store 只见 `*sqlx.DB`，避免改 Provide 签名）。
- 修复轮 1（已完成，`001cfacbf`）：`notification_occurrences` 已进 notification owner 的 RequiredTables + v0.93.0 fixture `known_missing_required_tables` + PG-gated 行为测试（`occurrence_postgres_test.go`，本机无 DSN 时 skip）。append-only 表不做 CRUD-shaped owner action（沿用 instances owner 的 occurrences 同款处理，schema 校验 + 方言测试覆盖）。

### Task 7 备注

- 本机 pnpm 陷阱：workspace 根在 `apps/`，从仓库根跑 `pnpm --filter @kandev/web ...` 会 NO_PKG_MANIFEST 失败——一律 `pnpm -C apps/web ...` 或 `pnpm -C apps --filter ...`。另：vitest 与 tsc 并发跑会资源争抢导致假失败，串行验证。
- 页面在 `/settings/system/instances`（System 区，feature 门控行 + discovery + breadcrumb）；health 挂载/变更后拉取（后端 poller 负责周期刷新）；`selectInstance` 已导出未接 UI（归 Task 8）。

### Task 8 裁决（design 缝隙：联邦数据 base，controller 决策）

Design D1/D2 只定义了面板数据走代理端口；实例注册表 / remote-notifications / select 是**本地后端**端点，查看远端时经代理打不到。三项裁决：

1. **双 base**：`lib/config.ts` 新增本地 base 导出（与 `getBackendConfig()` 同一计算但**忽略** `window.__KANDEV_API_PORT`：VITE dev 端口或同源）。一切联邦调用（instances CRUD/health/select、Task 9 的 remote-notifications REST、Task 8 的 switch）走本地 base；面板数据照旧走 `apiBaseUrl`。若错：查看远端时选择器/通知模块拿不到本地数据 → e2e #2/#4 暴露。
2. **活动实例 ID 注入**：shell 的 OmitBootData 分支同时注入 `window.__KANDEV_INSTANCE_ID="<id>"`（`BootPayload.InstanceID string json:"-"`；backendapp resolver 填充；`apps/web/global.d.ts` 声明；SPA 缺省 `'local'`）。cookie 是 HttpOnly 读不到、DTO 刻意不含 proxy_port，注入是唯一干净来源。picker 高亮/当前实例名/`useIsRemoteInstance()`（activeId !== 'local'）由此派生。若错：picker 无法显示当前实例 → e2e #2 暴露。
3. **WS 单连接维持**（Task 9 承接）：remote_notification handler 仅在活动实例为 local 时写 slice；查看远端时通知模块数据来自 REST 加载（pinned e2e 场景 #4/#6 均为本机视图）。不做第二条 WS 连接。

### Task 10 备注（E2E 揪出的产品 bug 与裁决）

- **两个真实产品 bug，E2E 发现并已修复（controller 审查通过）**：(1) Task 9 的 `selectRemoteNotificationUnreadByInstance` 每次返回新对象 → useSyncExternalStore 订阅者无限重渲染（React #185，全 app 崩溃）——修复为键于 entries 身份的快照缓存；(2) Task 4 的代理透传远端 CORS 头（远端见不到 Origin 故答 `ACAO:*`）与桥自身 CORS 头并存 → 浏览器拒绝所有代理响应——修复为 `modifyUpstreamResponse` 删除远端 CORS 头（桥是浏览器侧 CORS 唯一权威）。两者各带回归测试。教训：vitest 组件层未覆盖真实渲染循环与跨域头组合。
- **Ruling（联邦 UI 门控）**：picker/通知模块的可见性此前读 hydrate 的 feature flag（查看远端时来自**远端** app-state）→ 查看默认态（flag-off）远端时被困。裁决：门控从**本地** flag 派生（本地 `/api/v1/features` 或注册表可达性），与所查看实例的 app-state 无关；fixture B 改回 flag-off（诚实默认态）作回归钉。修复轮 2 进行中。
- **Ruling（预检头）**：代理 CORS 允许头补 `X-Kandev-Interim-Settings-Interlock`（及前端实际发送的自定义头，非通配）。
- **Parked（→ Task 11 文档）**：远端无订阅语义事件的 provider 时，条目仅经 60s 对账窗口到达；文档写明「实时投递需远端配置订阅相应事件的通知 provider」。spec 对账时窗本身接受该延迟。
- 已接受偏差：spec 文件位于 `e2e/tests/multi-instance/`（testDir 要求，brief 路径不可行）。

### Task 8 其他承接

- Task 6 行为备忘：select 一个 disabled 实例只写 selection cookie 不铸 bridge cookie、shell 回落本地 boot → picker 应将未启用实例渲染为禁用（i18n 说明）。
- Task 4/6 契约：`switchInstance` POST select（本地 base）→ `window.location.assign`；带 targetPath 时按 D3 用 `/t/:id?kandev_instance=<id>`。

### Task 4 裁决（implementer NEEDS_CONTEXT → controller 决策）

- **Enabled 变更联动 ProxyManager**：采用 Service 级变更通知钩子（`Set<Notifier>` idiom，参照 `SetTaskEventPublisher` 先例）——Create/Update/Delete 成功写入后触发 backendapp 侧 `ProxyManager.Reconcile()`；不做注册表轮询（boot 启动 + 写触发是全部机制）。允许修改 `internal/instances/service.go`（Tasks.md 接口记录后续回写）。若错：轮询延迟场景（注册后立即切换）不可用 → e2e #1/#2 会暴露。
- **桥接能力 cookie 密钥**：沿用 PortProxyHandler 模式（构造时进程内随机 32 字节）。代价：后端重启后 cookie 失效（15 分钟滑动窗一致；重启后浏览器经 select 端点重铸）。不新增密钥存储。

### Task 5 裁决（implementer NEEDS_CONTEXT → controller 决策）

- **同秒 >200 条的分页不可推进**：Ruling——有界恢复，spec 自身 Known Failure Modes（对账风暴行）明示对策即「分页与上限 + 超限只取最近窗口 + 『更早事件未同步』提示」。实现：页末 created_at 游标（严格大于）；满页且边界不前进时停止本周期分页、结构化日志记该条件、游标推过卡死边界保前进；唯一键去重吸收重叠。不改 Task 3 的远端 DTO/REST。若错：极端场景（同秒 >200 语义事件，实际负载不可能）丢失部分事件，日志可见。
- **实时事件掩盖漏收窗口**：Ruling——专用对账游标（per-instance），仅由对账成功页推进；relay 启动时从本地 MAX(created_at) 初始化（此后一切未知，正确起点），进程内存持有；live 路径 Record() 永不触碰。修复「漏 A、收到更新的 B、MAX 游标越过 A」的洞，且 relay 重启自愈。若错：无——该方案严格优于 MAX(all rows)。
- 修正本 controller 此前的临时建议（「since 从本地 MAX 派生」）：仅适用于游标**初始化**，不适用于每轮派生。



