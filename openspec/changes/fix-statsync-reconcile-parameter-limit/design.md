# 设计：statsync reconcile 删除的有界语句化

## Context

`statsync` 的 reconcile 语义是：远端每 N 次成功推送携带一份全量 uid inventory，本地接收端按 category 删除该 `source_instance` 在 inventory 之外的镜像行，让远端删除在一个对账周期内收敛。

现状实现（`apps/backend/internal/statsync/repository.go:336-375`）把 inventory 按 500 个 uid 分段，为每段生成一个 `AND uid NOT IN (...)` 子句，然后把**所有子句拼进同一条 DELETE**。整条语句的绑定参数总量 = `1 + len(inventory)`，与 inventory 规模线性相关。生产环境约 18.6 万条 message uid，远超内置驱动（`mattn/go-sqlite3 v1.14.33`，SQLite 3.51.1）的 `SQLITE_MAX_VARIABLE_NUMBER = 32766`，ingest 返回 500：

```
ERROR statsync ingest failed {"error": "reconcile statsync_messages: too many SQL variables"}
```

约束条件：

- 整个 ingest（upsert + 删除）在同一个事务里，失败必须整体回滚，不能出现半删状态。
- 同一实现要在 SQLite 与 PostgreSQL 上同形（LOCAL 可以是 PG）。LOCAL 的数据库引擎由部署决定，不能按引擎分叉。
- 语义硬约束：按 `source_instance` 删除；空 category inventory 仍删除该 source 的整个 category；`deleted` 计数口径保持"直接删除行数之和"。
- 本机无 PostgreSQL（`KANDEV_TEST_POSTGRES_DSN` 未设置、Docker 不可用），PG 行为只能由 CI 的 env-gated 测试覆盖。

## Goals / Non-Goals

**Goals:**

- 让每一条 SQL 语句的绑定参数数量与 inventory 长度无关，上界为常数级（页大小 + 1）。
- 保持 reconcile 的删除语义、原子性、`deleted` 计数口径与 HTTP 契约不变。
- 用回归测试锁住原故障：inventory 超过 32766 个 uid 时仍能完成删除。
- 覆盖分页/分批边界、空 category、多 category 混合与重复 uid。

**Non-Goals:**

- 不改 `scripts/statsync/push.py`，也不改 watermark / `push_count` 语义（失败不推进是既定契约）。
- 不改镜像表 schema、`v_stats_*` 视图或索引（含不新增 `source_instance` 索引）。
- 不改 ingest 的请求体上限，不改变 "inventory 不可拆分" 的推送契约。
- 不加 feature flag、配置项或降级开关。
- 不重构 statsync 的其它函数（upsert 路径本来就是逐行执行，不存在同类问题）。

## Research Log

### 未知 1：绑定参数上限的具体数值，以及生产错误能否在本地复现
- **未知描述**：决定 Requirement 的规模语义与回归测试的取值；影响任务拆分中的测试数据规模。
- **验证方式**：读驱动内置 SQLite 源码 + 包内临时探针直接调用 `deleteOutsideInventory`（探针已删除）。
- **结论（试通证据）**：上限 32766；`1 + len(inventory) = 32767` 起失败，错误文本与生产日志逐字一致。
- **证据摘要**：`sqlite3-binding.c:14458` → `# define SQLITE_MAX_VARIABLE_NUMBER 32766`；探针输出 `inventory=32765 params=32766 err=<nil>`、`inventory=32766 params=32767 err=reconcile statsync_messages: too many SQL variables`。

### 未知 2：`sqlx.In` + `tx.Rebind` 能否在 `*sqlx.Tx` 上组合使用（两种方言）
- **未知描述**：新的批量删除依赖 `sqlx.In` 展开 uid 列表后再按方言重绑；若组合不成立，方案需要改为手写占位符。
- **验证方式**：读仓库既有用法与 `sqlx` 版本。
- **结论（试通证据）**：仓库已在同一模式下使用（`SELECT ... LIMIT ?` 取批、`sqlx.In` 构造 `IN (?)`、`tx.Rebind` 后 `Exec`）。
- **证据摘要**：`apps/backend/internal/coordinator/ledger/settle.go:151-170`；`apps/backend/go.mod` 固定 `github.com/jmoiron/sqlx`。

### 未知 3：PostgreSQL 上 `LIMIT ?` 绑定与 `IN (?)` 展开的行为
- **未知描述**：若 PG 行为与 SQLite 不一致，`deleteCategoryOutsideInventory` 的分页键或占位符写法需要分方言。
- **验证方式**：无法本地验证——`KANDEV_TEST_POSTGRES_DSN` 未设置，Docker 不可用。
- **结论（blocker）**：作为硬 blocker 登记，不作为已接受假设；验证入口是新增的 env-gated 测试 `TestReconcileDeletesOutsideInventoryPostgres`，在设置 DSN 的 CI 中执行。设计不依赖 PG 特有语法（仅标准 `SELECT ... WHERE ... ORDER BY uid LIMIT $n` 与 `DELETE ... WHERE ... IN (...)`）。
- **证据摘要**：本机 `echo $KANDEV_TEST_POSTGRES_DSN` 为空；`docker info` 不可用；仓库既有 PG 测试同样以该 env-gated 方式运行（`apps/backend/internal/statsync/repository_schema_test.go:202-216`）。

## Decisions

### D1：删除改为"分页读回现有 uid → Go 侧差集 → 分批 DELETE"
- **选择**：对每个 category，用 uid 游标分页读取该 source 的现有 uid，在 Go 内与 inventory 集合求差，再按批执行 `DELETE ... WHERE source_instance = ? AND uid IN (...)`。
- **理由**：inventory 完全不出现在 SQL 里，单条语句参数恒为 `1 + 批大小`；无 DDL；SQLite/PG 同形；稳态下删除集合很小，写者锁持有时间最短。
- **已考虑 alternative**：（a）临时暂存表 + `NOT IN (SELECT ...)`——可行但引入 DDL、重复 uid 主键冲突、连接池残留与 PG 的 DROP 语义，且每晚要把整份 inventory 写入 staging；（b）"每个 chunk 一条 `uid NOT IN (chunk)`"——实测会删掉 inventory 内的合法行，语义错误。

### D2：分页键用 uid 游标，不用 OFFSET
- **选择**：`SELECT uid FROM <table> WHERE source_instance = ? AND uid > ? ORDER BY uid LIMIT ?`，游标取本页最后一个 uid。
- **理由**：分页过程中会删除行，OFFSET 会因行数变化而跳页/重页；uid 是主键且唯一，游标不受删除影响，两种方言都能用主键索引满足排序。
- **已考虑 alternative**：一次性 `SELECT uid` 全量载入——更短，但内存随镜像表线性增长；游标分页是有界内存。

### D3：页大小与删除批大小共用 `inventoryChunkSize`，由 const 改为 var
- **选择**：把 `const inventoryChunkSize = 500` 改为 `var inventoryChunkSize = 500`，同时作为分页 `LIMIT` 与删除批大小；注释改述真实约束。
- **理由**：单条语句最多 `1 + 500 = 501` 个参数，远低于 32766，也与推送脚本的 `CLOSURE_ID_CHUNK = 500` 一致；可注值后测试能用两三条数据覆盖多页/多批路径（先例：`handlers.go:19` 的 `maxIngestBodyBytes`）。
- **已考虑 alternative**：保留 const 并从环境变量取值——增加配置面，本 change 明确不加配置项。

### D4：空 inventory 短路为整类删除
- **选择**：`len(keep) == 0` 时直接执行 `DELETE FROM <table> WHERE source_instance = ?`，不进入分页。
- **理由**：保持既有语义（空 category 删除该 source 的整个 category），同时避免为"全删"做无谓的读回。
- **已考虑 alternative**：让分页路径自然处理空集合——会先读回全部 uid 再逐批删除，纯属浪费。

### D5：删除语句同时带 `source_instance` 谓词
- **选择**：`DELETE FROM <table> WHERE source_instance = ? AND uid IN (...)`。
- **理由**：读回时已按 source 过滤，但把 source 谓词留在删除语句上，使删除范围在语句本身可读，并防御跨 source 的极端并发写入。
- **已考虑 alternative**：只按 uid 删除——依赖读回快照，语句本身看不出范围。

### D6：重复 uid 用 map 去重
- **选择**：inventory 装载进 `map[string]struct{}` 后再比较。
- **理由**：无暂存表即无主键冲突；重复 uid 天然幂等。远端 `extract_inventory` 取自主键列不会重复，但契约没禁止，去重是零成本防御。

### D7：`deleted` 计数口径不变
- **选择**：累加每条 DELETE 的 `RowsAffected`；级联删除的子行不计入。
- **理由**：与旧实现一致，推送脚本与操作者看到的数字含义不变。

## Risks / Trade-offs

- [Risk] reconcile 现在每 category 多一次按 `source_instance` 的全表读 → Mitigation：按主键索引顺序分页，总代价 O(n)，与原本整表 DELETE 同一量级；reconcile 是夜间批处理，不在请求热路径。
- [Risk] 分页读取与删除交错执行，若游标语义写错会漏删或重复删 → Mitigation：游标只前进、基于 uid 值而非行数；跨页测试（chunk=2）覆盖。
- [Risk] PG 上 `LIMIT $3` 的参数类型或 `IN` 展开与 SQLite 不一致 → Mitigation：只用标准 SQL；env-gated PG 测试在 CI 验证；本地缺口已在 Research Log 标为 blocker。
- [Trade-off] 稳态下每 category 仍要读完全部源行，即使只删几条 → 接受：这与删除语句自身必然要扫描的行数相同，且避免了 staging 写入。
- [Trade-off] 首次上线后第一次成功的 reconcile 会一次性删除积压 12 天的行 → 接受：这是既定语义的正确结果；事务内完成，失败整体回滚。
- [Risk] 把 const 改为 var 引入包级可变状态 → Mitigation：仅测试注入，不做运行时配置；测试不并行运行。

## Migration Plan

N/A — 本 change 不涉及 schema、接口或部署变更为强制项的迁移。合并并重启 LOCAL 后即生效；下一次 reconcile 会补齐此前 12 天积压的删除。回滚只需回退代码提交，镜像表无需修复（删除只针对远端已不存在的行；若误删，远端仍持有的行会在后续 delta 中重新 upsert，代价是一段统计空窗）。

## Open Questions

（无阻塞项。已知缺口：PostgreSQL 行为需 CI 的 env-gated 测试确认。）
