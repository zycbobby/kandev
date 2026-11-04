# Apply Ledger: settle-abandoned-delivery-and-safe-deploy

> 由 `openspec-apply-change` 在 apply 全程维护，位置 `openspec/changes/<change-id>/ledger/index.md`。
> 与 `reviews/` ledger 同级，随 change 一并归档。markdown 形态，人类可读、跨 agent。

## Per-Task Ledger

> 每条 task 到达终态（done / rolled-back）时追加一行。
> `executor`：本次所用执行器（用户所选；无人值守自动回退时记 `auto`）。

> **执行器说明**：用户选定 `subagent-driven`（未完成实现 task 19 个，超过内联阈值）。因 Go 包交叉（journal / agentctl server api / orchestrator）无法安全并行，按依赖分波串行：Wave 1 = 1.1+2.1+2.2+3.1（Form B + journal/runtime 基元）与 7.1+7.2+8.1（部署/launcher，文件不相交）并行；Wave 2 = 4.1+4.2+5.1+6.1；Wave 3 = 9.x–11.x；Wave 4 = 12.1+13.1（控制器执行）。每个 child 只接收当前 task 的 work packet，不传全文 skill/specs。

| task id | executor | 验证方法 | 结果 | 状态 | 时间戳 |
|---------|----------|----------|------|------|--------|
| 1.1 | subagent-driven | `go test -trimpath -race -count=1 ./internal/orchestrator -run 'TestRetrySessionDelivery(Accepts\|Rejects)'` | `ok ... 2.045s`；successor 到达 runtime，非 execution 身份不匹配仍 blocked 且 runtime 未被调用 | `done` | 2026-11-04 12:53 |
| 2.1 | subagent-driven | `go test -trimpath -race -count=1 ./internal/agentctl/journal -run RetireRetained` + 全 journal 包 | `ok ... 1.117s`；dead owner + exact target 才 retire，live/mismatch/terminal 均拒绝且语义不变 | `done` | 2026-11-04 13:06 |
| 2.2 | subagent-driven | `go test -trimpath -race ./internal/agentctl/server/api ./internal/agent/runtime/agentctl -run 'RetainedRetirement\|RetireRetained'` | 两个包均 `ok`；unowned session 才可用，payload 不越界 | `done` | 2026-11-04 13:06 |
| 3.1 | subagent-driven | `go test -trimpath -race ./internal/agent/runtime/lifecycle -run RetireDeliveryRecord` + `go build ./internal/backendapp ./internal/agent/runtime/...` | `ok ... 1.250s`，build exit 0；live/retained 两路径均要求 death proof 与完整 inventory | `done` | 2026-11-04 13:06 |
| 4.1 | subagent-driven | `go build -trimpath ./internal/task/models/` | 编译通过；常量/类型齐全，无 schema 变更 | `done` | 2026-11-04 14:03 |
| 4.2 | subagent-driven | `go test -trimpath -race ./internal/task/repository/sqlite -run 'Abandoned\|PreIntake'` + `go run ./cmd/sqlguard ./internal` | `ok ... 6.824s`，sqlguard exit 0；临时 SQL 失败/幂等/精确 stream 绑定均覆盖；Postgres gated test 已写但本机无 DSN 跳过 | `done` | 2026-11-04 14:03 |
| 5.1 | subagent-driven | `go test -trimpath -race ./internal/orchestrator -run 'PreIntake\|Reconstruct\|Restart\|Pruned\|NativeDelivery\|TerminalRetry\|SessionDelivery\|Abandoned\|Succession'` + `go build ./...` | `ok ... 20.470s`，build exit 0；成功/回退/5 类 unsafe blocked/幂等/retire→SQL 失败收敛/archived+未授权前置拒绝均覆盖。偏离：仅 `uncertain` phase、无 `HistoryGap`、SQL `interrupted_unknown` 时启用（更保守，避免 pruned-history 与 successor 语义冲突） | `done` | 2026-11-04 14:03 |
| 6.1 | subagent-driven | `go test -trimpath -race ./internal/agent/runtime/lifecycle -run 'NativeResume'` | `ok ... 25.845s`；retired 且无 terminal evidence 的 peer 被跳过，未 retired peer 仍要求 terminal evidence。偏离：保留带 terminal evidence 的 retired peer 作为既有 proof（避免既有 projected-terminal 测试回归） | `done` | 2026-11-04 14:03 |
| 10.1 | subagent-driven | `go test -trimpath -race ./internal/agentctl/journal -run PendingSignal` + 全 journal 包 | `ok ... 1.806s`，全包 `ok ... 12.583s`；cap 256/TTL 24h、溢出 typed error、compaction 复制、无 bucket 旧库返回空列表 | `done` | 2026-11-04 14:12 |
| 9.1 | subagent-driven | `go test -trimpath -race ./internal/agentctl/server/api ./internal/agent/runtime/agentctl -run 'AgentStream'`；`./internal/common/agentstream ./internal/mcp/server` 全包 | 全部 `ok`；写/读 deadline、ping/pong、typed reason、MCP 优先级、event_queue_full 均有行为测试 | `done` | 2026-11-04 14:24 |
| 9.2 | subagent-driven | 同包常量断言测试 + 全包 | `PingInterval/PongTimeout/WriteDeadline/ReadDeadline/EventQueueMax*` 集中在 `internal/common/agentstream`，顺序断言通过 | `done` | 2026-11-04 14:24 |
| 10.2 | subagent-driven | `go test -trimpath -race ./internal/mcp/server ./internal/agentctl/server/api ./internal/agentctl/journal -run 'StepComplete\|SignalReplay'` + `./internal/mcp/handlers -run 'StepComplete'` | 全部 `ok`；capture-before-publish、typed capture errors、原 ID 重放、late response 标记 delivered、非幂等工具不 capture、重放只推进一次 | `done` | 2026-11-04 15:17 |
| 11.1 | subagent-driven | `go test -trimpath -race ./internal/orchestrator ./internal/agent/runtime/agentctl -run 'SignalDelivery\|CompletionPending'` + process/lifecycle/mcp handlers focused | 全部 `ok`；notice 不含 payload、一次性 MarkNotified、后端 last-agent-error 显示 completion-pending、accepted/chat fallback 清除且只推进一次 | `done` | 2026-11-04 15:17 |
| 11.2 | subagent-driven | `TestStepCompleteSignalCaptureIndependentOfBackendWriterLatency` + metric 标签测试 + AGENTS.md 审阅 | capture 在 backend writer 延迟下仍先落 journal；`workflow_step_signal_*` 与 `agent_stream_*` 标签闭集，已写入 AGENTS.md observability | `done` | 2026-11-04 15:17 |
| 12.1 | controller | `list-docs.py validate` + `lint-spec-files.py --all` + `openspec validate --strict` | 398 decisions / 1686 specifications 全部通过，change valid。偏离：durable-agent-delivery.md 已达 20 KB requirement 上限，新增 AC 拆入 `durable-delivery-abandoned-settlement.md` 并从原文件交叉引用；系统设计 mapping 同步新 AC ID | `done` | 2026-11-04 15:32 |
| 7.1 | subagent-driven | `bash scripts/deploy-user-service.test.sh`（busy/idle/`--force`/DB 缺失/不可读/表缺失/自定义路径/launchd + DB 字节不变） | 全部通过，gate 在 `publish_bundle` 前，拒绝时 runtime 未替换 | `done` | 2026-11-04 12:53 |
| 7.2 | subagent-driven | `bash scripts/make-deploy.test.sh`（dry-run 断言 `--force` 只出现在 deploy） | 全部通过，`service-install` 不含 `--force` | `done` | 2026-11-04 12:53 |
| 8.1 | subagent-driven | `go test -trimpath -race -count=1 ./internal/launcher`（清空 ambient KANDEV_* env） | `ok ... 27.473s`；unit 断言 60s，docs 同步；launchd 未改 | `done` | 2026-11-04 12:53 |

## Exit Gate

> 由 Step 4 在宣告完成前填写；三项必须全部满足，否则不得宣告完成或进入 verify / archive。

- [x] **Verification Strategy 入口通过**：按 packages 串行（`-p 1`）在干净环境（临时 `HOME`/`XDG_CONFIG_HOME`、清除 ambient `KANDEV_*`）执行。
  - `go test -trimpath -race -p 1 -count=1 ./internal/orchestrator -skip 'TestCompletedRelocation|TestSessionOpenResume|TestSessionOpenLaunchFailureExemptsOnlyVerifiedInspectionDeferral'` — `ok 248.433s`
  - `go test -trimpath -race -p 1 -count=1 ./internal/agent/runtime/lifecycle ./internal/agent/runtime/agentctl ./internal/agentctl/journal ./internal/agentctl/server/api ./internal/mcp/server ./internal/persistence/requiredstores ./internal/launcher` — 全部 `ok`（232.161s / 6.995s / 12.406s / 97.988s / 27.379s / 5.672s / 25.630s）
  - `go test -trimpath -race -p 1 -count=1 ./internal/task/repository/sqlite -run 'Abandoned|PreIntake'` — `ok 16.212s`
  - `go run ./cmd/sqlguard ./internal` — exit 0；`go test -race ./internal/persistence/storeconformance -count=1` — `ok 48.573s`
  - `bash scripts/deploy-user-service.test.sh` / `bash scripts/make-deploy.test.sh` — 全部通过
  - `go build ./...` — exit 0；`go vet` 覆盖 19 个改动包 — exit 0
  - **环境性例外（均在 base commit `26c227970` 复现，不是本 change 引入）**：
    - `internal/orchestrator` 的 8 个 worktree relocation/session-open 测试报 `worktree directory is corrupted ... checkout feature that cannot be transferred safely`；在 base commit 的干净 worktree 中以相同测试复现。其余 orchestrator 测试全绿。
    - `internal/task/repository/sqlite` 整包在本机会不定点超时（HEAD 两次分别停在 `TestResetTaskSessionBasesForRepository` / `TestSidebarQueryPreparationMemory`；base commit 停在 `TestSwitchTaskRunner_NoOpWhenAlreadyStoredProfile`）。base commit 同样超时；单测单独运行时通过。改动相关的 `Abandoned|PreIntake` 测试全绿。
    - `scripts/lint-go-changed` 不可用：本机 golangci-lint 1.59.1（Go 1.22 构建）读取 Go 1.26 export data 时 panic `unsupported version: 2`；已用 `go vet` 替代，正式 lint 由 CI 的固定版本执行。
  - **本 change 引入并已修复的回归**：`agentctl.(*Client).StreamUpdatesFrom` 的 ping pump 使用 runtime-bound context，而读循环只取消 dial context，导致 pump 与等待它的 goroutine 在流结束后泄漏；提交 `b77635266` 为每条 stream 建立独立生命周期并关闭 socket。修复后 `internal/agent/runtime/lifecycle` 全包（含 goleak）`ok 232.161s`。
- [x] **`openspec validate settle-abandoned-delivery-and-safe-deploy --strict` 通过** — 结果：`Change 'settle-abandoned-delivery-and-safe-deploy' is valid`（exit 0）
- [x] **验收方法与证据**：
  - 方法：Form A/B 使用真实 SQLite + bbolt + fake runtime HTTP 的集成测试；部署门禁用 fake HOME/unit/launcher 的 shell 测试；Form C 使用真实 WebSocket 端、agentctl journal 与 MCP 处理器。
  - 场景：验证清单路径 1–11 全部有对应测试并通过 — successor retry、dead owner + exact evidence → settled、truncated/live/active peer/hash mismatch → blocked 且无写入、retire→SQL 失败后收敛、native resume 忽略 retired peer、deploy busy/idle/`--force`、service active + DB 缺失/不可读拒绝、断流 capture→replay 只推进一次、超窗 `signal_delivery_failed` → completion-pending → 重发清除、event backlog 下控制请求仍送达、capture store 满/不可用返回 typed failure。
  - 结果：全部改动相关测试通过；no-fabrication / fail-closed 断言（journal 仅 retire、`fully_settled_at` 保持 NULL、notice 不含 payload）均有测试覆盖。
  - 未验证假设（按 plan 声明）：PostgreSQL gated test 因本机无 `KANDEV_TEST_POSTGRES_DSN` 跳过；remote executor / Windows 的 death proof 由 fail-closed 覆盖，不在本期实测。
