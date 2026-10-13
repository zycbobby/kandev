# Design: add-cross-instance-stats-sync

Proposal: `openspec/changes/add-cross-instance-stats-sync/proposal.md` · Spec: `specs/stats-sync/spec.md`

## 概览

REMOTE（旧版本实例，零代码改动）上的 scheduled automation 运行 `scripts/statsync/push.py`：只读抽取自身 SQLite DB 的统计相关原始行 → 变换（UID 命名空间化、快照字段裁剪）→ HTTPS POST 到 LOCAL 的 `POST /api/v1/statsync/ingest`（共享密钥）→ 成功后推进本地 watermark 状态文件。LOCAL 新增 `apps/backend/internal/statsync/` 模块：校验版本化 payload → 单事务内按 `(source, orig_id)` 幂等 upsert 进专用镜像表 → analytics 查询经兼容视图（`v_stats_*`，核心行 UNION ALL 镜像行）读取，使 /stats 七个区块按 workspace 映射渲染合并视图。

## 组件

```
REMOTE (stock scheduled automation, cron+TZ, agent 预批命令, executor=local/worktree)
  scripts/statsync/push.py  (python3 stdlib-only: sqlite3 + urllib + json)
    1. preflight: DB 存在性 + 引擎探测（非 SQLite 显式报错退出）
    2. 列内省（pragma_table_info），老版本缺列时选交集列
    3. 单只读连接抽取 delta（watermark - overlap 起的增量）
    4. 变换：uid = source + ":" + orig_id；快照裁剪为 {model, model_name, llm}；消息不带 content
    5. POST /api/v1/statsync/ingest（X-Statsync-Secret）
    6. 仅 2xx 时推进状态文件 <home>/statsync-state.json
LOCAL (HEAD, 新代码)
  internal/statsync/handlers.go   路由注册 + 密钥认证（常量时间比较）
  internal/statsync/service.go    payload 校验、source→workspace 映射解析、mode 分发
  internal/statsync/repository.go 镜像表 schema/migration、单事务 upsert/reconcile
  internal/statsync/models.go     payload 契约类型（schema_version 常量）
  internal/analytics/.../stats.go 七区块查询换表名为 v_stats_*；repositories/git 增 remote-only UNION 分支
```

## 关键设计决策

- **D1 兼容视图作为 UNION 缝**：建 `v_stats_tasks`、`v_stats_sessions`、`v_stats_turns`、`v_stats_messages`、`v_stats_commits`、`v_stats_git_snapshots`、`v_stats_task_repositories`，每张 = 核心表 UNION ALL 镜像行，镜像分支输出常量 `is_ephemeral=0`、`origin='manual'`。stats.go 查询仅换表名，纳入规则由视图统一保证，diff 最小。列集 = stats.go 实际引用的列超集（parity 测试为准出）。
- **D2 repo 归并双通道**：remote 仓库按 name 在映射 workspace 内匹配本地 `repositories` 行——匹配上则镜像 sessions 的 `repository_id` 落本地 repo id（git/repo 区块经视图自然合并），并写 `statsync_task_repositories` 链接行；未匹配（remote-only）则落 `statsync_repositories` 镜像行，repositories/git 区块各加一个 UNION 分支按 name 聚合（合成 id 取镜像 uid）。不在 LOCAL 伪造核心 `repositories` 行（避免污染 workspace 仓库列表 UI）。
- **D3 payload 只载统计相关列**：消息不带 `content`，会话快照裁剪为 `{model, model_name, llm}`（stats.go:669-671 实际只用这些键）——减小体积且避免对话内容与凭据跨机流转。
- **D4 时间戳契约**：payload 用 RFC3339 字符串；ingest 解析为 time.Time 经驱动落库，与核心行同构（实测存储为 TEXT `YYYY-MM-DD HH:MM:SS.ffffff+00:00`，UTC、字典序=时间序）。
- **D5 watermark**：REMOTE 状态文件按表记录最后时间戳（有 `updated_at` 用之，否则 `created_at`，如 commits/snapshots），抽取窗口 `>= watermark - overlap`（overlap 60s，容忍同秒乱序与时钟微差）；幂等 upsert 使重叠无害。仅 ingest 返回 2xx 才推进。
- **D6 ingest 事务语义**：整个 payload 在单事务内校验后写入（夜间增量规模小，短事务不饿共享 writer，遵守 `_txlock=immediate` 写者准入）；`mode=delta` 全量 upsert，`mode=reconcile` 附带全量 ID inventory，删除该 source 在 inventory 之外的镜像行；未知 `schema_version` → 400 原子拒绝；映射无法解析（显式映射指向不存在的 workspace，或无默认 workspace 可用）→ 403；未映射 source 默认归入主 workspace（见 D8 与 proposal 的完整合并统计目标）；密钥错/缺 → 401。
- **D7 认证**：头 `X-Statsync-Secret`，`subtle.ConstantTimeCompare`（先例 `automation/webhook.go:52-58`）；`internal/auth/httpmw/middleware.go` `isPublicPath` 增加精确分支 `POST /api/v1/statsync/ingest` 并更新 pinning test（auth 开启时仍仅凭共享密钥可达）。
- **D8 配置**：`statsync.ingest_secret`（env `KANDEV_STATSYNC_INGEST_SECRET`）与 `statsync.workspace_mappings`（map：source_instance → 本地 workspace id）按 `internal/common/config/catalog.go` + `source.go` 稳定配置契约登记；v1 无 UI。secret 未配置 = 路由不注册（不启用功能）。
- **D9 REMOTE 脚本健壮性**：python3 stdlib-only（REMOTE 无第三方依赖前置）；preflight 探测引擎（sqlite3 模块可开库即 SQLite，Postgres 路径显式报错为已知限制）；`pragma_table_info` 列内省选交集——旧版本实例可能缺后加的列（如 `tasks.origin`）；任务可选携带 best-effort `completed` 布尔（archived 或当前步为末步），缺省按 `archived_at IS NOT NULL`。
- **D10 路由接线**：照 `automation/handlers.go:45-46` 模式，`internal/statsync` 暴露 `Register(router, deps)`，在 backendapp 装配 automation handlers 的同一处注册。

## 镜像表（键与关键列）

| 表 | PK / upsert 键 | 统计消费所需关键列 |
|---|---|---|
| `statsync_repositories` | `uid`（=source:orig） | source, name, remote_url |
| `statsync_tasks` | `uid` | source, orig_workspace_id, workspace_id(映射后), state, archived_at, completed(可空), created_at |
| `statsync_task_repositories` | `uid` | task_uid → statsync_tasks, repository_id(本地或镜像 repo), created_at |
| `statsync_sessions` | `uid` | task_uid, started_at, completed_at, agent_profile_snapshot(裁剪 JSON), repository_id(映射后，可空) |
| `statsync_turns` | `uid` | session_uid, started_at, completed_at |
| `statsync_messages` | `uid` | session_uid, author_type, type, created_at |
| `statsync_commits` | `uid` | session_uid, commit_sha, committed_at, files_changed, insertions, deletions |
| `statsync_git_snapshots`（可选进 payload） | `uid` | session_uid, snapshot_type, files(JSON), created_at |

镜像子表用 `uid` 外键级联（FK 限定在镜像集内部）；`workspace_id`/`repository_id` 的值解析发生在 ingest（映射逻辑），存储的是解析后的本地 id。

## 区块 → 集成方式

| 区块 | 方式 |
|---|---|
| global / tasks / daily-activity | 查询中 `tasks`、`task_sessions`、`task_session_turns`、`task_session_messages` 换为对应 `v_stats_*` |
| completed-activity | 同上 + 镜像分支用 `COALESCE(completed, archived_at IS NOT NULL)`（视图暴露同名列） |
| model-usage | `task_sessions` → `v_stats_sessions`（快照裁剪字段已含 model 键） |
| repositories | 视图换表 + name-matched 自然合并；追加 remote-only UNION 分支（按 `statsync_repositories` 聚合） |
| git | 同上；commits/snapshots 走 `v_stats_commits` / `v_stats_git_snapshots`，remote-only 分支同 D2 |

## 失败路径

- 推送失败（网络/非 2xx）：watermark 不动，下个周期重推累积增量（spec R2）。
- payload 非法 / 版本不识别：400，事务不落任何行（spec R1）。
- 未映射 source：默认归入主 workspace 计入统计（proposal 拍板的完整合并目标）；仅当映射无法解析——显式映射指向不存在的 workspace，或无默认 workspace 可用——才 403 拒绝、统计不变。
- REMOTE 引擎非 SQLite：preflight 显式报错退出（已知限制，proposal non-goals）。
- 老版本缺列：列内省取交集，缺列字段置空（不猜测 schema）。

## 已知限制（记录进 verify 阶段检查单）

- 镜像任务的 completed-activity 在 remote 未提供 `completed` 标志时按 archived 口径，可能低计。
- git snapshots 为 payload 可选数组，remote 老版本若无法产出则峰值 LOC 区块对镜像部分缺省。
- 多远端 fan-in 不在 v1，但 `source_instance` 贯穿契约与镜像表，后续可扩展。

## Research Log

### 未知 1：SQLite 时间戳存储格式（决定 watermark 与 payload 契约）
- **验证方式**：对本地实例 DB 只读探测（`sqlite3 -readonly ~/kandev-home/data/kandev.db`）。
- **结论（试通证据）**：TEXT 存储，`YYYY-MM-DD HH:MM:SS.ffffff+00:00`（UTC）；偶见无小数部分（`2026-10-13 02:50:04+00:00`），但同 offset 下字典序仍=时间序（`+`(0x2B) < `.`(0x2E) 且小数逐位比较成立）。watermark 可用字符串比较；payload 用 RFC3339，ingest 经驱动落库与核心行同构。
- **证据摘要**：tasks `2026-10-13 03:12:00.048191+00:00`；task_sessions `2026-10-12 07:44:36.447135+00:00`；task_session_commits `2026-10-13 02:50:04+00:00`（退出码 0）。

### 未知 2：ingest 路由的接线先例
- **验证方式**：grep 路由注册。
- **结论（试通证据）**：`automation/handlers.go:45-46` 直接 `router.POST("/api/v1/automations/webhook/:id", wh.Handle)`；auth 白名单分支模式见 `middleware.go:340-342`。statsync 照搬。
- **证据摘要**：`internal/automation/handlers.go:45-46`；`internal/auth/httpmw/middleware.go:340-342`。

### 未知 3：REMOTE 脚本运行时依赖
- **验证方式**：本机探测 python3 stdlib（REMOTE 机器本身不可从本机验证）。
- **结论（拒绝/前置条件）**：脚本设计为 python3 stdlib-only（sqlite3/urllib/json），本机 `python3 -I` 验证可用（3.15，sqlite 3.54.0）。REMOTE 存在 python3 列为部署前置条件，脚本 preflight 校验并给出可读错误；不引入第三方依赖。
- **证据摘要**：`python3 -I -c "import sqlite3, urllib.request, json"` → `py-ok (3, 15) sqlite 3.54.0`。

### 依赖准出表

| 依赖 | 终态 |
|---|---|
| SQLite 时间戳格式/字典序 | ✅ 实测（未知 1） |
| 路由/白名单接线模式 | ✅ 实测（未知 2） |
| python3（本机/CI） | ✅ 实测（未知 3；repo 脚本测试走 `uv run --no-project --python 3.12`） |
| python3（REMOTE） | 前置条件 + preflight 显式报错（不可从本机验证，拒绝假设） |
| REMOTE→LOCAL 网络可达 / REMOTE 引擎 / DB 路径 / commit | 环境开放事实，watermark 重推容忍不可达；不阻塞定稿 |
| Postgres（LOCAL 侧） | 仓库一等引擎：镜像 schema 与 upsert 需双引擎覆盖（env-gated 测试），`ON CONFLICT (uid) DO UPDATE` 双引擎可用 |
