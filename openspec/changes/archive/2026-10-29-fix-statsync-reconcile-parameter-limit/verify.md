# fix-statsync-reconcile-parameter-limit — Verify

**Date:** 2026-10-29
**Result:** Clean（无未实现需求、无范围外新增、无设计偏差）

## 检测方式

- 派发一个只读漂移检测 subagent，对照 `proposal.md`、`design.md`（D1–D7）、`tasks.md`、spec delta（`specs/stats-sync/spec.md`）逐条核对实现。
- 实现基线为 `main...HEAD`（4 个提交：`1cdd0803c2` / `18f804eb2e` / `2374188694` / `03237aaff3`），工作树干净；`reviews/` 不存在，无 review round 阻塞。
- 校准：代码风格与命名差异不计为漂移；PostgreSQL 未验证属 ledger 已登记的已知缺口，不报为 drift。

## 核对结论

1. **Missing：无。** What Changes 四项全部落地：① `deleteOutsideInventory` 改为「uid 游标分页读回 → Go 侧差集 → `sqlx.In` 分批 DELETE」，inventory 不出现在 SQL 中；② `inventoryChunkSize` 注释改述「限制按语句计、非按子句计」并由 `const` 改为 `var`；③ 新增 `repository_reconcile_test.go`；④ spec delta 补齐 Requirement、3 个 Scenario 与两条 Known Failure Modes。
2. **spec delta 3 个 Scenario 均有覆盖。**
   - 远端删除收敛：既有 `TestApplyReconcileDeletesOutsideInventoryWithCascade` + 大规模回归用例。
   - 对账清单超单条语句参数上限：`TestApplyReconcileLargeInventoryExceedsStatementBudget`（32767 个 message uid，越过 SQLite 32766）与 HTTP 层 `TestIngestHandlerAcceptsReconcileAtProductionInventoryScale`（200 + `deleted=1`）。
   - 空 inventory 删除整类：`TestApplyReconcileEmptyCategoryDeletesWholeCategory`；「inventory 省略某 category」由既有 `TestApplyReconcileEmptyInventoryClearsSource` 覆盖，实现上走同一 `len(keep)==0` 短路路径。
3. **Out of Scope：无。** `git diff main...HEAD --name-only` 仅含 `repository.go`、`repository_reconcile_test.go` 与 openspec 变更文档。`scripts/statsync/push.py`、镜像表 schema / `v_stats_*` 视图 / 索引（含未新增 `source_instance` 索引）、错误响应契约、请求体上限、其它包的 `NOT IN` 用法均零改动；无新增依赖、配置项或 feature flag。
4. **Design Deviation：无。** D1 差集方案（无暂存表、无按 chunk 的 `NOT IN`）、D2 uid 游标而非 OFFSET（`uid > ?`，游标取本页末 uid，`OFFSET` 零命中）、D3 `const`→`var` 且分页 `LIMIT` 与删除批共用同一常量、D4 空集合短路、D5 删除语句带 `source_instance` 谓词、D6 `map[string]struct{}` 去重、D7 仅累加直接删除的 `RowsAffected`（`Deleted == 3` 精确断言），逐条符合。

## 参数上界核验

| 路径 | 语句 | 绑定参数 |
|------|------|----------|
| 空 inventory 整类删除 | `DELETE FROM <table> WHERE source_instance = ?` | 1 |
| 分页读回 | `SELECT uid ... WHERE source_instance = ? AND uid > ? ORDER BY uid LIMIT ?` | 3 |
| 分批删除 | `DELETE FROM <table> WHERE source_instance = ? AND uid IN (...)` | ≤ `1 + inventoryChunkSize` = 501 |

三条路径的参数数量均与 inventory 长度无关，满足 delta 的 "bounded parameter count that is independent of the inventory length"。

## 修复后验证

- `go -C apps/backend test ./internal/statsync/ -count=1` → `ok github.com/kandev/kandev/internal/statsync 0.200s`（verify 阶段复跑）。
- 聚焦用例 `-run 'Reconcile|ProductionInventoryScale' -v` → 全部 PASS。
- `bash scripts/lint-go-changed <changed-go-files>` → 0 issues；`go -C apps/backend run ./cmd/sqlguard ./internal` → exit 0。
- 全量后端与基线 `9c81855338` 各 21 个失败包、集合完全一致，零新增。

## 遗留（已知未验证）

- PostgreSQL：`TestReconcileDeletesOutsideInventoryPostgres` 本机因 `KANDEV_TEST_POSTGRES_DSN` 未设置而 SKIP，**视为未验证**，验证入口为设置该 DSN 的 CI。本记录与归档说明均不得写成通过。

## Overall Decision

- [x] PASS — 可进入 archive。
