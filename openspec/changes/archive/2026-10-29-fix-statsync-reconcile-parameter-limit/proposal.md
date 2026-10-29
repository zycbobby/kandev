## Triage

- **现象**：预期——每次对账推送（`mode=reconcile`）删除远端已删除的镜像行，让本地合并统计与远端收敛；实际——本地 ingest 在 inventory 超过 32765 个 uid 时对整条 DELETE 语句返回 500，后端日志为 `ERROR statsync ingest failed {"error": "reconcile statsync_messages: too many SQL variables"}`，删除从未执行。生产实例（3090Ti）自 2026-10-17 起连续 12 晚失败，watermark 冻结在 2026-10-16。
- **受影响 spec**：`openspec/specs/stats-sync/spec.md` → Requirement「周期性全量对账处理远端删除」及其 Scenario「远端删除在下一个对账周期收敛」。
- **性质判定**：A 实现 bug（spec 描述的语义正确，实现只在 inventory 规模较小时成立）；附带一处 spec 补充——spec 未写明删除不得受数据库单条语句绑定参数上限的约束。
- **复现**：在包内用临时探针直接调用 `deleteOutsideInventory`（探针文件已删除，worktree 干净）：inventory=32765（绑定参数 32766）成功；inventory=32766（绑定参数 32767）返回 `reconcile statsync_messages: too many SQL variables`，与生产日志逐字一致；inventory=40000 同样失败。生产 inventory 约 18.6 万条 message uid，超限约 5.7 倍。
- **根因初判**：`apps/backend/internal/statsync/repository.go:336-375` 的 `deleteOutsideInventory` 按 500 个 uid 一段生成 `AND uid NOT IN (...)` 子句，却把所有子句拼进**同一条** DELETE 语句，绑定参数总量 = `1 + len(inventory)`，没有上限。上方注释（326-328 行）声称 "chaining clauses keeps under bound-parameter ceilings"，这只对单个子句成立，对整条语句不成立。

## Why

statsync 的夜间全量对账在生产上连续 12 天失败：接收端把整份 uid inventory 绑定进一条 DELETE，超过 SQLite 的 32766 参数上限而返回 500。事务整体回滚，所以远端的删除永远不会在本地生效，镜像表只增不减、`/stats` 合并统计持续失真；而推送脚本只在全部 chunk 成功后才推进 watermark 与 push_count，于是每晚永久重试同一个失败请求。现在必须修复：越晚修，积压的待删除行越多，统计偏差越大。

## What Changes

**对账删除语句形态（核心）**

- From: 每个 category 只生成一条 DELETE，把全部 500-uid 分段的 `AND uid NOT IN (...)` 子句拼进同一语句，绑定参数 = `1 + len(inventory)`，随远端数据增长无上限。
- To: 按 uid 游标分页读回该 source 在该 category 的现有 uid（`WHERE source_instance = ? AND uid > ? ORDER BY uid LIMIT ?`），在 Go 内与 inventory 集合做差，再用 `sqlx.In` 按固定批大小分批执行 `DELETE ... WHERE uid IN (...)`；单条语句的绑定参数恒为常数级（页大小/批大小 + 1），与 inventory 长度无关。
- Reason: SQLite 的 `SQLITE_MAX_VARIABLE_NUMBER`（本仓库内置驱动为 32766）与 PostgreSQL 的同类限制都按**语句**计，不按子句计。只有拆成多条语句，上界才与 inventory 规模无关。
- Impact: 非破坏性，行为语义不变——仍按 `source_instance` 删除 uid 不在 inventory 中的行；空 inventory 仍删该 source 的整个 category；`deleted` 计数口径不变（各条语句 `RowsAffected` 之和，不含级联删除的子行）；全部语句仍在同一个事务内，失败仍整体回滚。

**注释订正**

- From: `inventoryChunkSize` 的注释暗示"把子句串起来就能保持低于绑定参数上限"。
- To: 陈述真实约束——单条语句的绑定参数总量受限，必须按有界集合**分语句**执行；分块只限制单条 `IN` 列表的长度，不限制整条语句的参数总量。

**回归测试**

- 新增 `apps/backend/internal/statsync/repository_reconcile_test.go`，覆盖：跨 chunk 边界的保留/删除、inventory 外行删除、空 inventory 整类删除、多 category 混合、以及一条真实 32767+ uid 规模的回归（实测约 0.05s）。
- 页/批大小常量改为测试可注值的 `var`（先例：`apps/backend/internal/statsync/handlers.go` 的 `maxIngestBodyBytes`），使少量数据即可覆盖多页/多批路径。

**spec 补充**

- `stats-sync` capability 的对账 Requirement 增加"删除必须对任意被接收的 inventory 规模完成，不得因超过数据库单条语句绑定参数上限而失败"的语义与 Scenario；`Known Failure Modes` 增加一行。

**不在本 change 范围**

- 不改 `scripts/statsync/push.py` 与 watermark / `push_count` 语义（失败不推进是 spec 要求的正确行为）；reconcile 失败后重试同一请求是预期。
- 不改镜像表 schema、`v_stats_*` 视图或索引（含不新增 `source_instance` 索引）。
- 不加"跳过 reconcile"、"降级为只增不减"之类开关；不改错误响应契约。
- 不改其它包中既有 `NOT IN` 小集合用法（已确认它们不会触发同一上限）。
- 不调整 ingest 的请求体上限，也不改变 inventory 不可拆分的推送契约。

## Capabilities

### New Capabilities

（无：本 change 不引入新能力。）

### Modified Capabilities

- `stats-sync`: 「周期性全量对账处理远端删除」补齐规模约束——对账删除必须对接收端接受的任意 inventory 规模完成，不得因超过数据库单条语句绑定参数上限而失败。

## Research Log

### 未知 1：绑定参数上限到底是多少，生产日志能否复现
- **验证方式**：读驱动源码 + 包内临时探针直接调用 `deleteOutsideInventory`。
- **结论（试通证据）**：上限为 32766；inventory=32765 成功、32766 起报错，错误文本与生产日志逐字一致。
- **证据摘要**：`apps/backend/go.mod` 使用 `github.com/mattn/go-sqlite3 v1.14.33`；其内置 `sqlite3-binding.c:14458` 为 `# define SQLITE_MAX_VARIABLE_NUMBER 32766`（SQLite 3.51.1）。探针输出：`inventory=32766 params=32767 err=reconcile statsync_messages: too many SQL variables`。

### 未知 2：失败是否留下半删状态，是否需要数据修复
- **验证方式**：读 `applyEnvelope` 的事务边界 + 探针确认是事务内报错。
- **结论（试通证据）**：整个 ingest（upsert + 删除）在同一事务，`messages` 是 category 顺序第 5 个失败，前 4 条 DELETE 随回滚撤销；镜像表无脏状态，不需要数据修复。
- **证据摘要**：`apps/backend/internal/statsync/repository.go:288-324`（`defer tx.Rollback()`，删除在 commit 之前）。

### 未知 3：为什么失败会永久重试，次生效应如何形成
- **验证方式**：读推送脚本。
- **结论（试通证据）**：inventory 固定放在最后一条独立 POST；`push_count`/`watermarks` 只在全部 chunk 2xx 后才写盘，因此失败那晚不计数，下一晚仍判定为对账轮次；该 POST 只带 inventory 不含行，失败事务里 upsert 全为空。
- **证据摘要**：`scripts/statsync/push.py` 的 `chunk_envelope`（inventory 单独最后一条）、`main()`（成功后 `state["push_count"] = push_count`）。

### 未知 4：literal「每个 chunk 一条 NOT IN DELETE」是否等价
- **验证方式**：探针实测两种写法。
- **结论（拒绝理由）**：不等价。对每个 chunk 单独执行 `DELETE ... uid NOT IN (chunk_k)` 会删掉存在于其它 chunk 的合法行（inventory 有 a、b 两条时，两条语句执行后三条消息全被删除）。必须让删除条件对**全量** inventory 成立，因此需要临时表或 Go 侧差集，而不是简单分块。
- **证据摘要**：同一探针内 `TestExploreNaivePerChunkNotInIsWrong` 输出 `rows left after naive per-chunk deletes: []`；对照的临时表写法 `deleted=1 remaining=[remote-1:msg-a remote-1:msg-b]`。

### 未知 5：两种可行实现方案的取舍
- **验证方式**：读仓库既有形态 + 探针验证方言行为。
- **结论（试通证据）**：采用"读回差集 + `sqlx.In` 分批删除"。临时表方案同样可行（探针确认 SQLite 中事务内 temp DDL 随回滚消失、multi-row VALUES 1000 行可执行），但它引入 DDL、重复 uid 主键冲突、连接池残留与 PG 的 DROP 语义，且每晚要向 staging 写入全部 inventory 行，拉长 SQLite 写者锁。读回差集方案无 DDL、双方言同形，并与仓库既有 `internal/coordinator/ledger/settle.go` 的 `deleteTurnBatch`（`SELECT ... LIMIT` + `sqlx.In` 批量 DELETE）一致。
- **证据摘要**：`apps/backend/internal/coordinator/ledger/settle.go:146-181`；探针 `TestExploreTempTableRollback`（回滚后 temp 表不存在，可重新创建）。

## Impact

- **代码**：`apps/backend/internal/statsync/repository.go`（重写 `deleteOutsideInventory` 及其注释/常量）；新增 `apps/backend/internal/statsync/repository_reconcile_test.go`。无其它包的改动。
- **接口**：`POST /api/v1/statsync/ingest` 的请求/响应契约与状态码不变（成功仍返回 200 与 `upserts`/`deleted`）。推送脚本无需改动。
- **数据**：无 schema、视图、索引或迁移变更。修复上线后第一次成功的 reconcile 会一次性删除积压 12 天的远端删除行——这是既定语义下的预期结果，不是数据迁移；删除在一个事务内完成，写者锁新增时长量级为一次按 `source_instance` 的全表扫描加少量删除。
- **方言**：实现只使用 `?` 占位符 + `Rebind` + `sqlx.In`，SQLite 与 PostgreSQL 同形；本机无 PostgreSQL（`KANDEV_TEST_POSTGRES_DSN` 未设置、Docker 不可用），PG 行为由 CI 的 env-gated 测试覆盖，属已知验证缺口。
- **依赖**：无新增依赖、无新增配置项、无 feature flag。
- **文档**：`docs/public/stats-sync.md` 未描述内部删除实现，无需更新。
- **验证**：`go test ./internal/statsync/ -count=1`；`bash scripts/lint-go-changed <changed-go-files>`；`go run ./cmd/sqlguard ./internal`。本机全量后端测试存在既有失败，按"基线复跑 + 逐名 diff 判零新增"判定。
