# Ledger: add-cross-instance-stats-sync

Executor: inline baseline(用户指令:9 个实现 task 为线性依赖链,不开 subagent;不逐 task 双审)。

## Per-Task Ledger

| task | executor | 验证方法 | 结果 | 状态 | 时间戳 |
|---|---|---|---|---|---|
| T1 契约与模型 | inline | `go test ./internal/statsync/`(models_test.go:非法版本/缺 source/非法 mode/reconcile 缺 inventory/5 类缺必填列/外来源 uid/非 RFC3339/非法 JSON 快照/合法载荷,三向全覆盖) | ok | done | 2026-10-13 |
| T2 镜像 schema + 视图 + persistence 登记 | inline | `go run ./cmd/sqlguard ./internal` 零违规;`go test -race ./internal/persistence/storeconformance -count=1` ok(含 TestPreviousStableUpgrade sqlite3;pgx 无本地 DSN SKIP,env-gated 用例 `repository_schema_test.go:TestSchemaPostgresFreshAndReplay` 已备);`go test ./internal/statsync/` fresh+replay+视图常量断言全绿 | ok | done | 2026-10-13 |

| T3 镜像写路径 | inline | `go test ./internal/statsync/ -count=1`:重复推送同 payload 行数不变 + upsert 原地更新列值;reconcile 后 inventory 外行清空且 FK 级联删除孤儿 session;空 inventory 清空该 source 全部镜像行 | ok | done | 2026-10-13 |
| T4 service + 配置 + 装配 | inline | `go test ./internal/statsync/`:Authorize 常量时间比较(对/错/缺);未映射 source→ErrUnmappedSource(→403);显式映射命中;无映射回退主 workspace;映射到不存在 workspace→403;repo 按 name 命中→改写 sessions/links 引用且不落镜像行,未命中→落镜像行(workspace=映射值);非法 payload 在触 DB 前拒绝。`go test ./internal/common/config/` 全绿(含 audited inventory 增补 KANDEV_STATSYNC_INGEST_SECRET)。backendapp 装配:storage.go 恒建镜像 schema + recordRequiredStore("stats-sync");services 装配 initStatsyncWiring(secret 未配置→nil 不注册路由);`go test ./internal/backendapp/` 仅剩 4 个经基线 worktree 验证的既有 macOS 环境失败(与本次无关) | ok | done | 2026-10-13 |
| T5 HTTP handler + auth 白名单 | inline | `go test ./internal/statsync/`:401 错/缺密钥、400 未知版本/非法体、403 未映射、200 返回 upserts/deleted 计数、reconcile 200。`go test ./internal/auth/httpmw/`:isPublicPath 精确分支 POST /api/v1/statsync/ingest + AllowlistMatrix 双 pin(POST 放行/GET 拦截)全绿 | ok | done | 2026-10-13 |
| T6 analytics 合并接入 + parity 测试 | inline | stats.go 七区块查询换 `v_stats_*` 视图(completed-activity/repo/global 的 completed 判定统一走视图 `completed` 列,删除各查询内 workflow_steps 关联);ListSessionCodeStats 保持核心表(plugin Host data API,非 /stats 区块)。新增 `stats_mirror_merge_test.go`:真 Service ingest(name-matched repo + remote-only repo + 两种 completed 口径)→ 七区块合并数字断言。既有回归全绿:`go test ./internal/analytics/...`(含 stats_aggregate_parity、handlers 可用性、reference fixture);`go run ./cmd/sqlguard ./internal` 零违规 | ok | done | 2026-10-13 |
| T7 REMOTE 同步脚本 | inline | `uv run --no-project --python 3.12 python scripts/statsync/push_test.py` 9 用例全绿:E2E 图路径 1(成功 delta:路径/X-Statsync-Secret 头/uid 命名空间化/快照裁剪 3 键/消息无 content/水位推进)、路径 2(500 失败→不建 state→恢复后重推同 delta)、路径 3(reconcile 附全量 inventory + 周期 N)、增量抽取(重叠窗口内重推、第三次只推尾巴行)、legacy 缺列交集、automation/ephemeral 排除、preflight(缺 DB/Postgres 显式拒绝) | ok | done | 2026-10-13 |
| T8 文档 | inline | docs-maintainer 结论:公开 API 面 + 配置键 + 脚本均属用户可见行为,需要公开文档。新增 how-to 指南 `docs/public/stats-sync.md`(Diátaxis: how-to;含 LOCAL 配置、REMOTE scheduled automation、前置条件 python3/网络可达/executor=local+worktree+预批命令、limits/recovery、端点状态码表);`meta.json` 注册(Configure Kandev 组,automation-and-mcp 之后);`authentication.md` 自认证路由清单补 X-Statsync-Secret;`configuration.md` 增 statsync 两键表格;`coverage.json` backend-configuration area 登记(source internal/statsync/service.go)。`node --test scripts/validate-public-docs.test.mjs` + `node scripts/validate-public-docs.mjs`(48 pages)通过 | ok | done | 2026-10-13 |
| T9 整体验证 | inline | 见下方 Exit Gate(全部通过) | ok | done | 2026-10-13 |

## Exit Gate

1. **Verification Strategy 入口通过**:✅ 主验证类型 integration-test 两条入口均执行并通过——
   - Go 侧全链路 parity:`go test ./internal/analytics/repository/sqlite/ -run TestStatsSectionsMergeMirroredRows`(真 Service ingest 镜像载荷 → 镜像表 → 七区块合并数字断言,含 name-matched/remote-only 仓库、completed 双口径)ok;既有 `go test ./internal/analytics/...`(stats_aggregate_parity 双引擎、handlers 可用性、reference fixture)全绿。
   - 脚本侧 stub 链路:`uv run --no-project --python 3.12 python scripts/statsync/push_test.py` 9 用例 OK,覆盖 E2E 图三条路径(成功 delta+契约形状、失败保留水位后重推同 delta、reconcile 附 inventory)。
   - 伴随:`go test ./internal/statsync/... ./internal/auth/httpmw/... ./internal/common/config/... ./internal/persistence/... -count=1` 全绿(含 storeconformance 双引擎 fresh/replay、TestPreviousStableUpgrade sqlite3;pgx 引擎无本地 DSN SKIP,env-gated Postgres 用例已备,CI 跑)。
2. **`openspec validate add-cross-instance-stats-sync --strict --no-interactive`**:✅ valid。
3. **验收方法与证据**:验收场景 = 「REMOTE 推送 → LOCAL 合并统计」。方法与结果:
   - 场景 1(合并渲染):fixture DB 上核心行(1 任务/1 会话/2 轮/3 消息/1 提交)+ 经真 Service 推入镜像行(2 任务/2 会话/2 轮/3 消息/1 提交,1 个 name-matched 仓库 + 1 个 remote-only 仓库)后,`GetGlobalStats` 计 3 任务/3 完成/3 会话/4 轮/6 消息、`GetRepositoryStats` 恰 2 行(local id 合并行 + uid 合成行)、`GetGitStats` 2 提交 +17/-7、model-usage 双模型、daily/completed-activity 当日数字符合统一纳入规则——全部断言通过。
   - 场景 2(重推安全):同 delta 推两遍行数不变、列值原地更新;HTTP 500 后无 state 文件,恢复后重推同 payload。
   - 场景 3(远端删除收敛):reconcile 后 inventory 外行清空且镜像内 FK 级联;空 inventory 清空该 source;脚本周期性附带全量 inventory。
   - 场景 4(认证):缺/错密钥 401,未映射 source 403,未知版本/非法体 400,auth 中间件 allowlist 双向 pin。
   - 已知限制(记入 verify 检查单):remote 未提供 completed 时按 archived 口径可能低计;git snapshots 为可选 payload;pgx 引擎路径本机未跑(CI 环境变量触发);活实例冒烟属 verify 阶段可选项,apply 阶段未执行。

T2 备注:共 8 张镜像表 + 8 张视图(D1 的 7 张 + `v_stats_repositories`,见「实现备注」第 1 条);storeconformance 行为探针 statsyncAction 覆盖 CRUD/时间戳回读/幂等重推(conflict 探针)/FK 违例载荷整体回滚(transaction 探针);升级 manifest `known_missing_required_stores` 增补 stats-sync(v0.93.0 fixture 不含新表)。

## 实现备注

- (T6 前置说明) remote-only 仓库合并采用第 8 张视图 `v_stats_repositories`(core repositories UNION ALL remote-only `statsync_repositories`,镜像分支带 ingest 时解析好的 `workspace_id`),使 D2 的「repositories/git 区块 remote-only 呈现(合成 id=镜像 uid、按 name 聚合)」经同一套聚合 CTE 达成——与 design.md D1「视图作为 UNION 缝、diff 最小」和 D2 的结果语义一致,避免在查询里复制整条聚合管线。
- (T4 前置说明) YAML 键名按仓库 catalog 既有驼峰约定取 `statsync.ingestSecret` / `statsync.workspaceMappings`(design.md 行文的 `statsync.ingest_secret` 为散文写法;env 别名 `KANDEV_STATSYNC_INGEST_SECRET` 与 design 一致)。
- (T5 实现要点) 视图不能随镜像表在 boot 早期创建:后续 store 的表重建迁移(如 office 的 tasks.priority_text_rebuild)会因视图引用被 DROP 的核心表而失败(`error in view v_stats_tasks: no such table: main.tasks`,backendapp 集成测试抓到)。因此 `statsync.NewWithDB` 只建表,`statsync.EnsureViews` 在 boot 末尾(所有 store schema 迁移完成后、recordSchemaVersion 之前)创建视图,失败则启动中止。视图为每次 boot 幂等重建,天然吸收未来列变化。
- (verify 裁决) 未映射 source 的行为在变更文档内曾自相矛盾(spec 场景与 D6 写「未映射即 403」,proposal.md「workspace 映射」一节写「默认映射到用户主 workspace」);实现遵循 proposal(`service.go` resolveWorkspace 回退 defaultWorkspace,`backendapp/statsync.go` 注入主 workspace 回退)。verify 阶段 drift 检测发现后经用户裁决(2026-10-13)以实现/proposal 为准:spec 场景改写为「未映射的 source 默认归入主 workspace」+「映射无法解析被拒绝」两条,D6 与失败路径同步改写;代码、测试、公开文档零改动。

## Exit Gate

(待所有 task 完成后填写)
