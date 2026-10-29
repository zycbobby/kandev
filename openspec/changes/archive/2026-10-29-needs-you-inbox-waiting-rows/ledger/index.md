# Apply ledger — needs-you-inbox-waiting-rows

## Pre-flight Scan

Executor: subagent-driven-development (36 implementation sub-items grouped into Task 01–05; each group is one work packet dispatched to a fresh implementer).

| 检查对 | 生产/消费 | 结论 |
|--------|-----------|------|
| 01 ↔ 02 | 01 提供 `task.manual_step_entered` 事件与 `StepTransition.ActorKind/ActorID`；02 提供 waiting 行与合并读 | 无冲突；02 的 refresh 触发依赖 01 的事件名，顺序满足 |
| 01 ↔ 03 | 01 在 `notifications/service/service.go` 增事件/`HandleManualStepWait`/origin 字段；03 在同一文件增 `HandleRelayedOccurrence` | 顺序修改同一文件；03 的 origin 字段由 01 预先落地，接口一致 |
| 02 ↔ 03 | 03 更新 `backendapp/helpers.go` 与 `use-remote-inbox-controller.ts`；02 已改前者并新建后者相关读取 | 顺序修改，无接口冲突 |
| 02 ↔ 04 | 02 创建 `GET /api/v1/needs-you-inbox`（+hidden）；04 消费该端点并新增实例转发 | 接口契约一致（`{rows,count,hidden_count,has_more,next_snooze_expiry}`） |
| 03 ↔ 04 | 03 改 `instance_relays.go`（notifier 注入）；04 在 `instance_inbox_routes.go` 新增转发 | 不同文件；`initInstanceRelays` 签名以 03 为准，04 只消费 |
| 01–04 ↔ 05 | 05 只改文档与状态 | 无冲突 |
| 组内自洽 | 每组“写失败测试 → 实现”成对；文件清单覆盖 Interfaces 所列产物 | 无自相矛盾 |

Ruling: 执行顺序固定 1→2→3→4→5；组 3 的侧边栏删除必须在组 2 的 refresh 触发落地之后 — 已由 tasks.md 顺序保证，实施时不得提前。

## Per-Task Ledger

| task | executor | 验证方法 | 结果 | 状态 | 时间戳 |
|------|----------|----------|------|------|--------|
| Task 01（1.1–1.8） | subagent (sonnet) | `go test ./internal/orchestrator/... -run ManualStep`、`./internal/backendapp/... -run 'ManualStep\|Gateway'`、`./internal/notifications/...`；vitest 50/50；`i18n:check`；typecheck；lint-go-changed。组命令中 8+34+2+1 个失败经 pristine HEAD worktree 对比为环境性既有失败，非本 task 引入。 | 全部新测试通过；修复 round 1：事件常量移入 `internal/events`，并补正默认订阅断言（4 事件）。 | done | 2026-10-28 |
| Task 02（2.1–2.9） | subagent (sonnet) | 后端 22 包 ok；93 个失败与干净 HEAD baseline 逐名一致（零新增）；前端 112 文件/1009 tests；typecheck、i18n:check、i18n:ratchet、e2e:sleep-ratchet、changed-file lint；E2E `--grep "waiting rows"` 3/3。 | 全部新测试与 E2E 通过；修复 round 1：移除 variadic 兼容参数，`RegisterRoutes`/`InboxBootSummary` 改为必填 waiting store 并更新测试。 | done | 2026-10-28 |
| Task 03（3.1–3.6） | subagent (sonnet) | 后端 `instances/... notifications/... backendapp/...`：12 个失败与 pristine HEAD 逐名一致（零新增）；前端 135 文件/1440 tests、typecheck、i18n:check；E2E `--grep "multi-instance"` 5/5；`go vet ./internal/... ./pkg/...` 干净。 | 全部新测试与 E2E 通过；清理 round 1：移除 8 份 catalog 中两个孤立 locale key，i18n:check 通过。golangci-lint 本机未安装，仅 go vet。 | done | 2026-10-29 |
| Task 04（4.1–4.6） | subagent (sonnet) | 后端 `instances/...` 全绿、`backendapp` 仅 3 个 pristine HEAD 同名的 `/var` symlink 失败；前端 17 文件/186 tests、typecheck、i18n:check/ratchet；E2E `--grep "multi-instance"` 修复后 6/6（含新 remote waiting test 与 remote-inbox 回归）；changed-file lint 0 issues。 | 全部新测试与 E2E 通过；修复 round 1：在 landed `remote-inbox.spec.ts` 中完成任务 fixture，消除其合法的 Review 手工步等待行对旧断言的干扰。`ReadNeedsYou(ctx, limit)` 采用与 landed `ReadInbox` 一致的 workspace discovery，tasks.md 的 workspaceID 签名视为过期描述。 | done | 2026-10-29 |

## Exit Gate

执行时间：2026-10-29。`reviews/state.json` 不存在，无 review session 阻塞。

1. **Verification Strategy 入口：通过。**
   - 后端 `go test ./internal/orchestrator/... ./internal/notifications/... ./internal/clarification/... ./internal/task/repository/sqlite/... ./internal/instances/... ./internal/backendapp/... -count=1`：当前树 55 个失败，pristine HEAD 基线 55 个失败，失败名集合逐名一致（`comm -23`/`comm -13` 均为空，零新增、零修复）。失败集中在 orchestrator(8)、orchestrator/executor(34)、notifications/service(2)、task/repository/sqlite(1)、backendapp(10)，与各组 baseline 记录一致，均为本机环境性既有失败。新增测试全部通过。
   - 前端 `pnpm vitest run lib/state/slices/needs-you-inbox lib/api/domains app/needs-you-inbox lib/ws/handlers/notifications.test.ts lib/coordinator`：112 文件 / 1051 tests 全部通过；`pnpm run typecheck` 通过。
   - i18n：`pnpm run i18n:check` 六项全过；`pnpm run i18n:ratchet` 通过（0 added + 24 modified clean）。
   - E2E：`--grep "waiting rows"` 4/4 通过；`--grep "multi-instance"` 6/6 通过（含 flag-off 回归与新远端等待行流程）。
   - 文档校验：`openspec validate --all --strict --no-interactive` 13/13 通过；`list-docs.py validate` 通过（395 decisions / 1681 specs）；`lint-spec-files.py --all` 通过；`git diff --check -- docs openspec` 干净。
   - 编译覆盖：`go vet ./internal/... ./pkg/...` 通过（含测试文件编译）。
2. **`openspec validate`：通过。** `openspec validate --all --strict --no-interactive` → 13 passed, 0 failed（含 `needs-you-inbox-waiting-rows`）。
3. **验收方法与证据：**
   - 本地等待 episode：后端 `manual_step_wait_test.go`（无 on_enter / 仅 reset_agent_context / 自动步 / complete-on-enter / sessionless / 重复收敛去重）+ notifications service 自移动抑制测试；E2E `needs-you-inbox-waiting-rows.spec.ts` 三例（出现、dismiss/restore、离开后消失）。
   - 合并读同源：`needs_you_inbox_handlers_test.go` 断言 rows/count/hidden_count/has_more 同源、隐藏行不占位、源失败不返回部分列表；boot summary 复用同一 merge helper。
   - 远端等待行：`remoteinbox` client/aggregate 测试覆盖跨 workspace fan-out、排序/截断、404/401/transport 降级；`instance_inbox_routes_test.go` 覆盖单次 fan-out 与降级 200；E2E 覆盖出现、打开、远端完成消失、实例降级隔离。
   - 统一通知：relay v2 四事件、dedup 一次投递、不写本地 occurrence、origin payload、订阅静默、self-move 排除；E2E 断言 relayed `session.turn_finished`/manual-step 走通知设置、带 origin 标签、无打开动作、侧边栏模块 testid 与 `/api/v1/remote-notifications` 均不存在。
   - 回归：`multi-instance` flag-off E2E 断言面板惰性、无模块、端点 404；`features.needsYouInbox` off 时通知仍独立投递（Task 01 设计）。
   - 已知未在本机执行：`make -C apps/backend lint`（golangci-lint 未安装；CI 负责全量 lint）；Postgres 方言分支仅由代码测试覆盖（`KANDEV_TEST_POSTGRES_DSN` 未设置）。
