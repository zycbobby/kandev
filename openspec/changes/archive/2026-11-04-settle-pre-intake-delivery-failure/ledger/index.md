# Apply Ledger: settle-pre-intake-delivery-failure

> 由 `openspec-apply-change` 在 apply 全程维护，位置 `openspec/changes/<change-id>/ledger/index.md`。
> 与 `reviews/` ledger 同级，随 change 一并归档。markdown 形态，人类可读、跨 agent。

## Per-Task Ledger

> 每条 task 到达终态（done / rolled-back）时追加一行。
> `executor`：本次所用执行器（用户所选；无人值守自动回退时记 `auto`）。

| task id | executor | 验证方法 | 结果 | 状态 | 时间戳 |
|---------|----------|----------|------|------|--------|
| 1 | inline | `go test -trimpath ./internal/agentctl/journal ./internal/agent/runtime/lifecycle ./internal/agent/runtime/agentctl`（absence 用例）+ `./internal/orchestrator -run TestRetrySessionDeliveryReconstructsMissingSubmission` | PASS：缺失/存在/异代 owner 三态、retained 前后快照 staleness、live 404 映射、`IsDeliverySubmissionNotFound` | `done` | `2026-11-03 23:41` |
| 2 | inline | `go test -trimpath ./internal/task/repository/sqlite -run 'TestSettlePreIntakeAgentDeliverySubmission|TestPostgresSettlePreIntake'`；`go run ./cmd/sqlguard ./internal`；`go test ./internal/persistence/storeconformance` | PASS：结算/幂等/冲突/无关 block 保留；PostgreSQL 用例无 DSN 时 SKIP；sqlguard 与 storeconformance 通过 | `done` | `2026-11-04 00:05` |
| 3 | inline | `go test -trimpath ./internal/orchestrator -run 'TestRetrySessionDelivery(.*PreIntake\|KeepsBlock\|SettlesProvably\|KeepsArchived)'` + retry/delivery 回归；`./internal/agent/runtime/lifecycle ./internal/agent/runtime/agentctl` 全包 | PASS：可证明未投递 retry 返回 settled、释放队列、不派发 prompt；另一候选/截断/非空 stream/outcome 不符/归档任务均保持 blocked；重放幂等 | `done` | `2026-11-04 00:22` |
| 4 | inline | `go test -trimpath ./internal/agentctl/server/process ./internal/agentctl/server/api ./internal/agent/runtime/agentctl ./internal/orchestrator -run 'Admit\|MapsRejectedKind\|SettlesRejectedSubmission\|KeepsUncertain\|DurableAdmission\|DurableSubmission'` | PASS：写前拒收/写后不确定 sentinel 分离；API 返回 typed kind/reason；client 映射 rejected；orchestrator 即时结算、失败回退 uncertain + block | `done` | `2026-11-04 00:33` |
| 5 | inline | `go test -trimpath -race ./internal/agentctl/journal ./internal/agentctl/server/process ./internal/agentctl/server/api ./internal/agent/runtime/lifecycle ./internal/agent/runtime/agentctl ./internal/orchestrator -run 'EventTooLarge\|DeliveryCapability\|ForwardUpdates\|BlockedAdmission\|AdmissionBlocked\|CapabilityPropagates'`；受影响包全量 | PASS：超大事件跳过、pump 存活、AdmissionBlocked 可见且容量恢复后清除、typed blocked 不建 submission/block；全量失败仅因绝对路径 TMPDIR 触发 unix socket 长度限制（改用短 TMPDIR 后通过） | `done` | `2026-11-04 00:50` |
| 6 | inline | `scripts/lint-spec-files.py --all`；`scripts/list-docs.py validate`；`openspec validate ... --strict` | PASS。注意：现存 requirements / system-design 两个文件分别只剩 12 / 126 字节余量，无法按计划原地追加；按 `docs/specs/guide/structure-and-ownership.md` 的「按能力拆分」要求，新增 `requirements/durable-agent-record-recovery.md`（迁移 REQ-008）以腾出主需求文件余量，并新增 `system-design/durable-agent-delivery-settlement.md` 承载新契约；主 requirements 文档保留 REQ-006/007 的新 AC 与链接 | `done` | `2026-11-04 01:12` |
| 7.1 | inline | `go test -trimpath -race`：sqlite / journal / process / api；orchestrator；lifecycle；agentctl；`storeconformance`；`go run ./cmd/sqlguard ./internal` | PASS。sqlite/journal/process/api 全绿；orchestrator 358s 全绿、lifecycle 250s 全绿（需 `GIT_CONFIG_NOSYSTEM=1` + 空 `GIT_CONFIG_GLOBAL`，否则宿主 git-lfs 的全局 `filter.lfs.*` 会让 relocation 测试误判 checkout 特性）；agentctl 全绿；storeconformance 66s 全绿；sqlguard exit 0。`KANDEV_TEST_POSTGRES_DSN` 未设置，PostgreSQL gated 用例按设计 SKIP | `done` | `2026-11-04 01:40` |
| 7.2 | inline | 安装 golangci-lint v2.15.0（built with go1.26.0）后执行 `bash scripts/lint-go-changed <40 changed files>` | PASS：`0 issues`。系统自带 golangci-lint 1.59.1 无法解析本仓库的 v2 配置与 Go 1.26 export data（`unsupported version: 2`），属于宿主工具链限制 | `done` | `2026-11-04 01:44` |
| 7.3 | inline | 部署后对真实会话 `062a1281` 执行 Retry connection 冒烟 | **未执行**：需要修复版 backend 指向真实 Kandev home 且影响会话处于可操作状态；本环境只有 worktree，无该运行实例。留待部署验收 | `pending` | `2026-11-04 01:44` |

## Exit Gate

> 由 Step 4 在宣告完成前填写；三项必须全部满足，否则不得宣告完成或进入 verify / archive。

- [x] **Verification Strategy 入口通过**：`
  cd apps/backend
  go test -trimpath -race ./internal/task/repository/sqlite ./internal/orchestrator ./internal/agent/runtime/lifecycle ./internal/agent/runtime/agentctl ./internal/agentctl/journal ./internal/agentctl/server/process ./internal/agentctl/server/api
  go test -trimpath -race ./internal/persistence/storeconformance
  go run ./cmd/sqlguard ./internal
  ` — 结果：全部 PASS（orchestrator 358s、lifecycle 250s、storeconformance 66s、sqlguard exit 0；race 用例均通过）。环境说明：宿主在 global/system git config 中定义了 `filter.lfs.*`，会让 worktree relocation 测试误判 checkout 特性，验证时使用 `GIT_CONFIG_NOSYSTEM=1` + 空 `GIT_CONFIG_GLOBAL`（文件可写，避免 `/dev/null` 锁失败）；`KANDEV_TEST_POSTGRES_DSN` 未设置，PostgreSQL gated 用例 SKIP。
- [x] **`openspec validate settle-pre-intake-delivery-failure --strict` 通过**：`Change 'settle-pre-intake-delivery-failure' is valid`；`scripts/lint-spec-files.py --all` 与 `scripts/list-docs.py validate` 同时通过（396 decisions / 1683 specifications）。
- [x] **验收方法与证据**：
  - 方法：integration-test 为主（真实 SQLite/bbolt/journal/HTTP 组件），配合 repository 单元测试、race 运行、sqlguard、spec 校验与 lint。
  - 场景：写前拒收即时结算；SQL 缺失但 journal 可证明未投递的 retry 结算；journal 丢失/截断/其它候选/非空 stream/outcome 不符/归档任务的保守拒绝；并发 retry 幂等；agentctl 超大事件跳过与 blocked health 可见/恢复。
  - 结果：`SessionDeliveryRecoveryResponse = settled` 且不派发 prompt、释放 parked queue；保守场景全部 `blocked` 且 block 保持 open；写前拒收无 block、写后失败回退 `interrupted_unknown` + block；`AdmissionBlocked` 在 storage error 时可见、容量恢复且 pump 存活时清除；`scripts/lint-go-changed` 0 issues。
  - 未覆盖：Task 7.3 的部署后真实会话冒烟（需要部署后的 backend 与真实 Kandev home），留待部署验收。
