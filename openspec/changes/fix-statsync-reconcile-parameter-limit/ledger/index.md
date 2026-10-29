# Apply Ledger: fix-statsync-reconcile-parameter-limit

> 由 `openspec-apply-change` 在 apply 全程维护，位置 `openspec/changes/fix-statsync-reconcile-parameter-limit/ledger/index.md`。
> 与 `reviews/` ledger 同级，随 change 一并归档。markdown 形态，人类可读、跨 agent。

## Per-Task Ledger

| task id | executor | 验证方法 | 结果 | 状态 | 时间戳 |
|---------|----------|----------|------|------|--------|
| 1.1 | inline | `go vet ./internal/statsync/`；新建 `repository_reconcile_test.go` 含夹具助手与失败用例 | `VET_OK`；文件建立。夹具助手 `withInventoryChunkSize` 因在 2.1 前无调用方触发 `unused` 门禁，随 2.1 落地；const→var 提前到 1.1 使助手可编译 | `done` | `2026-10-29 13:30` |
| 1.2 | inline | `go test -run TestApplyReconcileLargeInventoryExceedsStatementBudget -v`（旧实现） | FAIL：`reconcile statsync_messages: too many SQL variables`，与生产日志逐字一致 | `done` | `2026-10-29 13:31` |
| 1.3 | inline | 重写 `deleteOutsideInventory` + `uidSet` / `deleteCategoryOutsideInventory` / `deleteUIDBatches`；`grep "NOT IN" repository.go` | 聚焦用例转 PASS；`NO_NOT_IN_LEFT` | `done` | `2026-10-29 13:32` |
| 1.4 | inline | `go test ./internal/statsync/ -count=1`（聚焦 + 全包） | PASS（含既有 `TestApplyReconcileDeletesOutsideInventoryWithCascade`、`TestApplyReconcileEmptyInventoryClearsSource`） | `done` | `2026-10-29 13:32` |
| 1.5 | inline | `git commit` + pre-commit（gofmt / go-lint / commitlint） | `1cdd0803c2 fix(statsync): delete reconcile rows with bounded statements` | `done` | `2026-10-29 13:32` |
| 2.1 | inline | `TestApplyReconcileKeepsInventoryAcrossPages`（chunk=2，8 条 message，inventory 列 1..7） | PASS：剩余集合恰为 msg-1..msg-7 | `done` | `2026-10-29 13:33` |
| 2.2 | inline | `TestApplyReconcileEmptyCategoryDeletesWholeCategory` | PASS：messages=0，tasks/sessions/turns/commits 各 1 | `done` | `2026-10-29 13:33` |
| 2.3 | inline | `TestApplyReconcileDeletesOutsideInventoryPerCategory`（含重复 uid） | PASS：三张子表各删 1 行，父表不变，`Deleted == 3` | `done` | `2026-10-29 13:33` |
| 2.4 | inline | `TestApplyReconcileLeavesOtherSourcesIntact` | PASS：仅 `remote-1:msg-1` 被删，`remote-2` 行链完整 | `done` | `2026-10-29 13:33` |
| 2.5 | inline | `go test -run ApplyReconcile -v`（7 个用例） | 全部 PASS；提交 `18f804eb2e test(statsync): cover reconcile paging and category scoping` | `done` | `2026-10-29 13:33` |
| 3.1 | inline | `TestIngestHandlerAcceptsReconcileAtProductionInventoryScale`（32767 uid，HTTP 路由） | PASS：`200`，`deleted == 1` | `done` | `2026-10-29 13:34` |
| 3.2 | inline | `TestReconcileDeletesOutsideInventoryPostgres`（env-gated） | **SKIP（未验证）**：本机 `KANDEV_TEST_POSTGRES_DSN` 未设置。不得记为通过；验证入口留给 CI | `done` | `2026-10-29 13:34` |
| 3.3 | inline | `go test ./internal/statsync/ -count=1`；`bash scripts/lint-go-changed <2 files>`；`go -C apps/backend run ./cmd/sqlguard ./internal` | 包测试 PASS（PG 用例 SKIP）；lint `0 issues`；sqlguard exit=0，无输出 | `done` | `2026-10-29 13:35` |
| 3.4 | inline | `git commit` + pre-commit | `2374188694 test(statsync): verify reconcile contract at HTTP scale and on postgres` | `done` | `2026-10-29 13:35` |

## Exit Gate

> 由 Step 4 在宣告完成前填写；三项必须全部满足，否则不得宣告完成或进入 verify / archive。

- [x] **Verification Strategy 入口通过**：`go -C apps/backend test ./internal/statsync/ -count=1` — 结果：`ok github.com/kandev/kandev/internal/statsync`（全包 PASS，`TestReconcileDeletesOutsideInventoryPostgres` 按设计 SKIP）
- [x] **`openspec validate fix-statsync-reconcile-parameter-limit --strict` 通过** — 结果：`Change 'fix-statsync-reconcile-parameter-limit' is valid`
- [x] **验收方法与证据**：
  - 方法：包内真实 SQLite 集成测试 + gin 路由 HTTP 验收 + env-gated PostgreSQL 测试；另跑 changed-file lint 与 sqlguard。
  - 场景：① 32767 uid 原故障回归（先红后绿，红时错误文本与生产逐字一致）；② chunk=2 跨页保留/删除；③ 空 category 整类删除；④ 多 category 混合 + 重复 uid；⑤ 跨 source 隔离；⑥ HTTP 层 32767 uid 返回 200 + `deleted=1`。
  - 结果：6 条路径全部 PASS；`Deleted` 精确等于直接删除行数；PG 路径本机 SKIP，**未验证**，需在设置 `KANDEV_TEST_POSTGRES_DSN` 的 CI 中确认。

### 全量后端基线 diff

- 基线：`9c81855338`（改动前）在独立 worktree 复跑 `make -C apps/backend test`。
- 结果：基线与改动后各 21 个失败包，**包名集合完全一致**（`diff` 为空，仅耗时不同），零新增失败；`internal/statsync` 两次均为 `ok`。
- 说明：本机存在既有的环境性失败（agent runtime / agentctl / storage / task 等），与本次改动无关。

### PostgreSQL 未验证声明

`TestReconcileDeletesOutsideInventoryPostgres` 在本机因 `KANDEV_TEST_POSTGRES_DSN` 未设置而 SKIP。**该路径视为未验证**，不得在任何后续 verify/archive 记录中写成通过；验证入口为设置 DSN 的 CI。
