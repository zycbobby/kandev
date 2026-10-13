# Tasks: add-cross-instance-stats-sync

按序推进；每条 task 准出标准通过后才勾选并进入下一条。apply 由 `openspec-apply-change` 驱动，任务顺序即依赖顺序（契约 → schema → 写路径 → service/配置 → HTTP → analytics → 脚本 → 文档 → 整体验证）。

## Verification Strategy

- **主验证类型：integration-test**（无 UI 改动，Playwright e2e 不适用）：
  - Go 侧：sqlite fixture 上「ingest payload → 镜像表 → analytics 七区块查询」全链路 parity 断言（本地行 + 镜像行合并数字），复用 `internal/analytics/repository/sqlite/stats_reference_fixture_test.go` 模式。
  - 脚本侧：`push.py` 对 fixture SQLite 抽取 → 进程内 `http.server` stub 断言路径/密钥头/payload 形状/watermark 行为。
- unit-test（契约校验、映射解析、密钥常量时间比较）是实现伴随手段，不作为唯一验收依据。
- verify 阶段可选活实例冒烟（debug-kandev）：对运行中的 LOCAL 实例手工推一条 fixture delta，/stats 页面肉眼确认合并区块。

## E2E 测试图

| # | 用户路径 | 关键状态节点 | 观测点 |
|---|---|---|---|
| 1 | 夜间推送 → /stats 合并视图 | REMOTE 抽取 → POST 200 → watermark 推进 → parity 查询 | stub 收到的请求体与 `X-Statsync-Secret`；镜像行数；parity 测试中七区块数字 = 本地 + 镜像 |
| 2 | 推送失败 → 下周期重推收敛 | POST 500 → watermark 不动 → 恢复后同 delta 重推 → 幂等 upsert | 状态文件内容不变；第二次请求体与首次等价；镜像行无重复 |
| 3 | 远端删除 → 对账收敛 | reconcile 推送（全量 inventory）→ 镜像行删除 → 数字回落 | 200 响应 deleted 计数；被删 uid 查无；parity 数字回落 |

## 实现清单

- [x] **T1 契约与模型**：`apps/backend/internal/statsync/models.go` + `models_test.go`。定义 payload 信封（`schema_version` 常量、`source_instance`、`mode: delta|reconcile`、八类行数组、reconcile 的 inventory），校验：未知版本拒绝、缺 source 拒绝、行内必填列缺失拒绝、时间戳 RFC3339 解析。边界：不含 DB 与 HTTP。准出：`go test ./internal/statsync` 契约用例全绿（非法版本/缺字段/合法载荷三向）。

- [x] **T2 镜像 schema + 视图 + persistence 登记**：`apps/backend/internal/statsync/repository.go`（schema init + 幂等迁移，建 design.md 镜像表 8 张与 `v_stats_*` 7 张兼容视图，镜像分支常量 `is_ephemeral=0`/`origin='manual'`）；登记 `apps/backend/internal/persistence/requiredstores/catalog.go` + `apps/backend/internal/persistence/storeconformance/adapters.go`。边界：只建 schema/视图，不写业务查询。准出：`go run ./cmd/sqlguard ./internal` 通过；`go test -race ./internal/persistence/storeconformance -count=1` 通过；schema fresh-DB + 同库 replay 测试通过（`repository_schema_test.go`），存在 `KANDEV_TEST_POSTGRES_DSN` 时跑 env-gated 用例。

- [x] **T3 镜像写路径**：`apps/backend/internal/statsync/repository.go` 追加 `ApplyDelta` / `ApplyReconcile`：单事务内 `ON CONFLICT(uid) DO UPDATE` 幂等 upsert；reconcile 删除 inventory 外该 source 行（镜像内 FK 级联）。边界：不含 HTTP 与映射解析（workspace_id/repository_id 解析后的值由 service 传入）。准出：重复推送同 payload 两遍行数不变；reconcile 后 inventory 外行清空且级联生效；测试覆盖两条路径。

- [x] **T4 service + 配置 + 装配**：`apps/backend/internal/statsync/service.go` + `service_test.go`；配置 `statsync.ingest_secret`、`statsync.workspace_mappings` 登记 `apps/backend/internal/common/config/catalog.go` + `source.go`；backendapp 装配（Provide writer/reader pools + 注册路由，接线模式同 `automation/handlers.go:45-46`）。service 负责：常量时间密钥比较、source→本地 workspace 解析、repo 按 name 在映射 workspace 内解析到本地 `repositories.id`（未匹配落 `statsync_repositories`）。边界：不含 analytics。准出：未映射 source → 403；映射解析用例（命中/未命中）通过；secret 未配置时路由不注册。

- [x] **T5 HTTP handler + auth 白名单**：`apps/backend/internal/statsync/handlers.go` + `handlers_test.go`（`POST /api/v1/statsync/ingest`：401 错/缺密钥、400 未知版本/非法体、403 未映射、200 返回 upsert/deleted 计数）；`apps/backend/internal/auth/httpmw/middleware.go` `isPublicPath` 增精确分支 `POST /api/v1/statsync/ingest` 并更新对应 pinning test。边界：handler 不含业务逻辑，全部委托 service。准出：handler 四向用例绿；`go test ./internal/auth/httpmw` 绿。

- [x] **T6 analytics 合并接入 + parity 测试**：`apps/backend/internal/analytics/repository/sqlite/stats.go` 七区块查询换表名为 `v_stats_*`；repositories/git 区块追加 remote-only UNION 分支（按 `statsync_repositories` 聚合，合成 id 取镜像 uid）；completed-activity 镜像口径 `COALESCE(completed, archived_at IS NOT NULL)`。新增 parity 测试（fixture：核心行 + 镜像行各若干，断言七区块合并数字与纳入规则）。边界：不改 DTO/handler/前端。准出：parity 测试断言全绿；既有 `go test ./internal/analytics/...` 全绿（含 `stats_reference_fixture_test.go` 回归）。

- [x] **T7 REMOTE 同步脚本**：`scripts/statsync/push.py`（shebang + python3 stdlib-only；preflight 引擎探测与 python 版本检查；`pragma_table_info` 列内省选交集；watermark 状态文件按表记最后时间戳、overlap 60s；变换：uid 命名空间化、快照裁剪 `{model, model_name, llm}`、消息无 content、可选 `completed` best-effort；仅 2xx 推进 watermark）+ `scripts/statsync/push_test.py`（unittest：最小 DDL fixture DB + 进程内 `http.server` stub）。边界：脚本不改 Kandev 产品代码；Postgres 路径显式报错。准出：`uv run --no-project --python 3.12 python scripts/statsync/push_test.py` 覆盖 E2E 图三条路径全绿。

- [x] **T8 文档**：走 `/docs-maintainer` 评估 ingest 公开 API 面（`docs/public/`）与脚本使用说明（REMOTE 侧 scheduled automation 配置引用 `docs/public/automation-and-mcp.md`；列明前置条件：python3、网络可达、agent 预批命令、executor=local/worktree）。边界：不写 docs/specs（OpenSpec change 已承载需求）。准出：docs-maintainer 结论记录；若新增文档则按 `docs/public/README.md` 结构并通过相应 lint。

- [x] **T9 整体验证**：`make -C apps/backend test`（或最小集合：`go test ./internal/statsync/... ./internal/analytics/... ./internal/auth/httpmw/... ./internal/persistence/...`）+ `go run ./cmd/sqlguard ./internal` + 脚本测试 + `openspec validate add-cross-instance-stats-sync --strict --no-interactive`；新改 Go 文件过 `gofmt -l` 与 `golangci-lint run --new-from-rev`。准出：全部命令零失败、校验 valid。

## 验证清单

1. `go test ./internal/statsync/...`（T1/T3/T4/T5 用例）→ 全绿
2. `go run ./cmd/sqlguard ./internal` && `go test -race ./internal/persistence/storeconformance -count=1` → 通过
3. `go test ./internal/analytics/...` → parity 新用例 + 既有回归全绿
4. `go test ./internal/auth/httpmw/...` → 白名单 pinning 全绿
5. `uv run --no-project --python 3.12 python scripts/statsync/push_test.py` → 三条 E2E 路径全绿
6. `openspec validate add-cross-instance-stats-sync --strict --no-interactive` → valid
7. （verify 阶段可选）活实例冒烟：fixture delta 推送到运行中 LOCAL，/stats 肉眼确认合并区块出现远端活动
