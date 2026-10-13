## Why

用户在两台机器上各跑一个 Kandev 实例（LOCAL：本机 dev 机，新代码；REMOTE：第二实例，旧 commit、构建冻结）。用户在 LOCAL 的统计页（/stats）看数据，但统计只覆盖本机活动，看不到 REMOTE 上的工作。需要让 LOCAL 的 /stats 展示两台机器合并后的数据。方向已定（2026-10-12 拍板，不再重议）：LOCAL 是聚合方，新增 ingest API；REMOTE 用既有版本自带的 scheduled automation 定期把原始统计行推送过来——新代码全部集中在已经是新版的 LOCAL 上，REMOTE 保持零改动。

## What Changes

- **REMOTE 侧（零代码改动）**：配置一个 scheduled automation（如每日夜间，Asia/Shanghai），由 agent 运行一个同步脚本：只读抽取自身 DB 的原始行 → 按 payload 契约做 ID 命名空间化与身份映射 → HTTPS POST 到 LOCAL ingest API（带共享密钥）→ 成功后推进 watermark（状态文件存 REMOTE 本地）。推送失败不丢数据：watermark 不动，下次运行重推累积增量；LOCAL 侧按 (source, 原始 ID) 幂等 upsert。脚本随仓库发布（`scripts/` 下），人工拷贝到 REMOTE 使用。
- **LOCAL 侧（全部新代码）**：新增 `apps/backend/internal/statsync/` 模块，提供版本化 ingest 端点（建议 `POST /api/v1/statsync/ingest`）：校验版本化 payload 契约 → 经专用 repository 写路径落镜像表（不走 `Service.CreateTask`，不发任务事件、不占 generated-title、不受 workflow admission 影响）。
- **写库形态：专用镜像表 + analytics UNION**（方案 (b)，Explore 阶段已论证）：为 remote 原始行建独立镜像表集，`internal/analytics` 的统计查询 UNION 镜像行。From: 统计页只反映本机 DB 原始行。To: 统计页按 workspace 映射合并渲染本机行 + 镜像行，纳入规则一致（非 ephemeral、非 automation origin）。理由：外键强制开启（`internal/db/sqlite.go:40` `_foreign_keys=on`），镜像直写核心表必须连带伪造 `repositories`/`task_repositories`/`task_environments` 行；且任务行变更必须发事件的仓库约定不允许静默直写核心表。镜像表方案无 FK 耦合、看板/任务列表保持干净、是可 upstream 的产品形态。备选方案 (a)（直写核心表 + 命名空间 ID）因此被否决。
- **认证：webhook-secret 自认证路由**：新端点带共享密钥校验（常量时间比较），沿用 `internal/automation/webhook.go` 先例；启用 auth 的部署需在 `internal/auth/httpmw/middleware.go` 的 `isPublicPath` 加白名单项并更新 pinning test。备选 PAT 被否决：需要在本机开启 `features.auth`，行为面变化更大。
- **workspace 映射**：payload 携带 remote 侧 workspace 身份；LOCAL 侧用配置（config.yaml/env，v1 无 UI）把 source 映射到本地 workspace，默认映射到用户主 workspace（满足「完整合并统计」目标）。
- **一致性**：v1 用周期性全量对账（每 N 次 push 附带一次全量 ID inventory，LOCAL 清理 inventory 之外的镜像行）；tombstone 机制推迟。

## Capabilities

### New Capabilities

- `stats-sync`: 跨实例统计同步。REMOTE 实例经 scheduled automation 把版本化 raw-row payload 推送到 LOCAL ingest API；LOCAL 经专用镜像表存储并在 /stats 按 workspace 映射渲染合并视图；watermark + 幂等 upsert 保证重推安全；周期性全量对账处理远端删除。

### Modified Capabilities

（无：`internal/analytics` 在 OpenSpec 层没有既有 capability spec，镜像行 UNION 属于 `stats-sync` 合并视图需求的实现面，在 Impact 中列出。）

## Research Log

### 未知 1：统计页数据来源是否可合并（转发汇总数是否可行）
- **验证方式**：读代码（Explore 步骤，本会话上一步）。
- **结论（试通证据）**：不可行。7 个统计区块全部在查询时从原始表实时计算（`apps/backend/internal/analytics/handlers/stats_handlers.go:66-72` 路由；`stats.go` 各 CTE），必须把原始行落进聚合方 DB，故 ingest 载荷必须是 raw rows。
- **证据摘要**：`stats_handlers.go:66-72`；`analytics/repository/sqlite/stats.go`（is_ephemeral=0 且 origin != 'automation_run' 才计入，行 22-23；author_type='user'、type LIKE 'tool_%'；agent_profile_snapshot JSON 抽取，行 669-671）。

### 未知 2：镜像直写核心表是否可行（方案 (a)）
- **验证方式**：读 DDL 与 DB 连接参数。
- **结论（拒绝理由）**：否决。FK 强制开启（`internal/db/sqlite.go:33-40`），`tasks→workspaces`、`task_repositories→tasks+repositories`、`task_session_git_snapshots→task_environments` 等外键要求连带伪造多张表行；且仓库约定「任务行变更必须发事件」不允许静默直写（镜像任务还会泄漏进本机看板/列表）。
- **证据摘要**：`base_schema.go:720-800,1314-1540`（DDL 与 FK）；backend CLAUDE.md「Task lifecycle events」约定。

### 未知 3：REMOTE 数据库引擎假设（SQLite）是否可靠
- **验证方式**：读配置与持久层代码；REMOTE 机器事实不可从本机验证。
- **结论（blocker，不阻塞开题）**：Postgres 是一等引擎（`database.driver: sqlite|postgres`，`internal/common/config/config.go:946-958`；`internal/persistence/provider.go:37-48`），「REMOTE 是 SQLite」仅是假设。REMOTE 的引擎、home/DB 路径、commit、到 LOCAL 的网络可达性尚未提供。v1 抽取脚本按 SQLite 实现 + 引擎探测显式报错；REMOTE 为 Postgres 时作为已知限制记录。
- **证据摘要**：`config.go:946-958`；`persistence/provider.go:37-48`；`db/sqlite.go:40`（WAL 语义仅 SQLite 成立）。

### 未知 4：是否已有可复用的历史行写入入口
- **验证方式**：枚举 task/session REST 面。
- **结论（试通证据）**：无。现有 session 路由只读 + `POST /tasks/:id/sessions/ensure`（启动真实执行），不存在接受「指定 ID/历史时间戳」的写入端点，新 ingest 端点是必需的。
- **证据摘要**：`task_handlers.go:231-239`；`message_handlers.go:328-330`。

### 未知 5：认证与调度先例是否成立
- **验证方式**：读 webhook 与 auth 中间件、automation scheduler。
- **结论（试通证据）**：成立。webhook-secret 自认证先例（`X-Webhook-Secret` + `subtle.ConstantTimeCompare`，`automation/webhook.go`）；PAT 中间件在 `auth/httpmw/middleware.go`（`svc.ResolveBearer`，`features.auth` 开关）；scheduled automation（`TriggerTypeScheduled`、robfig/cron、30s tick、时区感知，`automation/scheduler.go`）在旧版同样存在，REMOTE 零改动可行。
- **证据摘要**：`automation/webhook.go:52-58`；`auth/httpmw/middleware.go:34-58,303`；`automation/scheduler.go:17,87,105-112`。

## Impact

- **代码（LOCAL）**：新增 `apps/backend/internal/statsync/`（handler/service/repository/models/config）；路由注册；`internal/analytics/repository/sqlite/stats.go` 各统计查询 UNION 镜像行（dialect 参数化，SQLite/Postgres 双引擎）；新增镜像表 schema（`runMigrations` 幂等迁移 + fresh/replay 测试 + env-gated Postgres 覆盖）；`internal/persistence/requiredstores` + `storeconformance` 登记；`internal/auth/httpmw/middleware.go` `isPublicPath` 白名单项 + pinning test。
- **代码（REMOTE，脚本而非产品代码）**：`scripts/` 下新增同步脚本（只读抽取 + 变换 + POST + watermark 状态文件），人工部署到 REMOTE 并配置 scheduled automation；executor 需 local/worktree 且 agent profile 预批脚本命令（automation 运行不能等待权限提示）。
- **测试**：payload 契约测试；ingest 幂等/watermark 重推测试；认证测试（缺密钥/错密钥/常量时间比较）；analytics 合并口径 parity 测试（镜像行纳入后各区块数字符合统一纳入规则，参照 `stats_reference_fixture_test` 模式）；迁移 replay 测试。
- **配置**：LOCAL ingest 共享密钥 + source→workspace 映射（config.yaml/env，按 `internal/common/config/catalog.go` 稳定配置契约登记；v1 无 UI、无 i18n）。
- **文档**：ingest 端点属公开 API 面，apply 阶段走 `/docs-maintainer` 评估；REMOTE 侧配置说明引用 `docs/public/automation-and-mcp.md`。
- **明确不做（v1 non-goals）**：设置 UI 与 i18n；实时同步；REMOTE 产品代码改动；任何一侧直接写 DB；预聚合数字转发；LOCAL→REMOTE 反向同步；通用多远端 fan-in（但 schema 保留 `source_instance` 字段为将来留口）；REMOTE 为 Postgres 的抽取路径（探测报错 + 已知限制记录，待 REMOTE 事实确认后补）。
- **开放环境事实（不阻塞，实现前需确认）**：REMOTE→LOCAL 的可达 URL:port 与推送时间窗；REMOTE kandev home/DB 路径与 commit；REMOTE 引擎（SQLite 假设）；LOCAL 在推送时段在线（watermark 重试容忍错过的推送）。
