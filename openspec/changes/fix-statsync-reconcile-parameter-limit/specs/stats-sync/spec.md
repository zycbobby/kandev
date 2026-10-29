# stats-sync

## 背景

`stats-sync` 的对账语义要求：远端每 N 次成功推送携带一份全量 uid inventory，本地接收端据此删除该 source 在 inventory 之外的镜像行，使远端删除在一个对账周期内收敛（见「周期性全量对账处理远端删除」）。

生产事故暴露了这条语义缺少规模约束：接收端把整份 inventory 绑定进**一条** DELETE 语句（`WHERE source_instance = ? AND uid NOT IN (...) AND uid NOT IN (...) ...`），绑定参数总量等于 `1 + len(inventory)`。当远端积累到约 18.6 万条 message uid 时，远超 SQLite 的 `SQLITE_MAX_VARIABLE_NUMBER`（本仓库内置驱动为 32766），ingest 返回 500，错误为 `reconcile statsync_messages: too many SQL variables`，删除从未执行。由于推送脚本只在全部 chunk 成功后才推进 watermark 与 push_count，失败被永久重试，镜像表只增不减。

原实现的分块（每 500 个 uid 一个 `AND uid NOT IN (...)` 子句）只限制了单个子句的长度，没有限制整条语句的绑定参数总量——限制是按语句计、不是按子句计。spec 需要把"任意被接收的规模都必须完成删除"写成显式要求，避免同类实现退化为"小数据能过、生产规模必崩"。

## 方案与决策

- **删除按有界集合分语句执行，而不是把全部分块拼进一条语句。** 实现取「按 uid 游标分页读回该 source 现有 uid → Go 内与 inventory 求差 → `sqlx.In` 分批 `DELETE ... uid IN (...)`」：无 DDL、SQLite 与 PostgreSQL 同形，且稳态下几乎不删东西，写锁窗口最短。
- **拒绝"每 chunk 一条 `uid NOT IN (chunk)`"。** 实测证明它删错数据：每条语句只看得见自己那一段 uid，别的分段的合法行在它眼里"不在 inventory 中"而被删除。
- **拒绝临时暂存表方案作为首选。** 它在 SQLite 上可行（事务内 temp DDL 随回滚消失），但引入 DDL、重复 uid 主键冲突、连接池残留与 PG 的 DROP 语义，且每晚要把整份 inventory 写入 staging，拉长 SQLite 写者锁。
- **语义保持不变**：按 `source_instance` 删除 uid 不在 inventory 中的行；空 category inventory 仍删除该 source 的整个 category；`deleted` 计数仍为各语句 `RowsAffected` 之和；全部语句仍在同一事务内。
- **不把参数上限写成实现细节。** Requirement 只陈述行为：删除必须对接收端接受的任意 inventory 规模完成，不得因超过数据库单条语句的绑定参数上限而失败。如何分语句属于实现选择。

## MODIFIED Requirements

### Requirement: 周期性全量对账处理远端删除

Periodically (every N successful pushes), the sync script SHALL include a full ID inventory of the source; the local instance SHALL delete mirrored rows of that source that are absent from the inventory so remote deletions converge within one reconciliation cycle. The deletion SHALL complete for any inventory size the ingest endpoint accepts: the local instance MUST NOT fail a reconciliation because the inventory exceeds the database's bound-parameter limit for a single SQL statement, and every statement it executes MUST keep a bounded parameter count that is independent of the inventory length.

#### Scenario: 远端删除在下一个对账周期收敛
- **WHEN** a task is deleted on the remote instance and the next inventory-bearing push completes
- **THEN** its mirrored rows are removed locally and merged statistics no longer count it

#### Scenario: 对账清单规模超过单条语句的参数上限
- **WHEN** the inventory lists more uids than one SQL statement can bind (for example about 186000 message uids against SQLite's 32766-parameter limit)
- **THEN** the reconcile still deletes exactly the mirrored rows of that source that are absent from the inventory, keeps the rows the inventory lists, and answers 200 with the deletion count

#### Scenario: 空 inventory 删除整个类别
- **WHEN** a reconcile push carries an empty inventory for one category
- **THEN** every mirrored row of that source in that category is deleted, and the same applies to categories the inventory omits

## Known Failure Modes

| Symptom | Cause | Fix |
|---------|-------|-----|
| 对账返回 500，日志为 `reconcile <table>: too many SQL variables`，远端删除始终不收敛 | 把全部 chunk 分块拼进同一条 DELETE，绑定参数 = 1 + inventory 长度，超过 SQLite 的 32766 上限（PostgreSQL 亦有同类按语句计的限制） | 按有界集合分语句执行删除；不得把整份 inventory 绑定进单条语句 |
| 分块删除后仍删掉了 inventory 内的行 | 对每个 chunk 单独执行 `uid NOT IN (chunk)`，每条语句看不到其它分段的 uid | 删除条件必须对全量 inventory 成立（临时表或 Go 侧差集），不能简单分块 |
