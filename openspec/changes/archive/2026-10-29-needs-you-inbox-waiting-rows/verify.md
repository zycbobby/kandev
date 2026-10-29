# needs-you-inbox-waiting-rows — Verify

**Date:** 2026-10-29
**Result:** Drift Found → 按用户裁决修复 → 复验通过（无未实现需求、无范围外新增）

## 检测方式

- 4 个只读漂移检测 subagent 并行，分别覆盖 Task 01+03（通知桥与模块移除）、Task 02（本地等待行与合并读）、Task 04（远端等待行）、Task 05 + 横切（文档、flag、schema、i18n、删除残留）。
- 对照 `proposal.md`、`design.md`（D1–D12）、`tasks.md`、两份 spec delta（`cross-instance-inbox`、`cross-instance-notifications`）、`docs/specs/ui` 需求与系统设计。
- 实现基线是工作树未提交改动（`git diff` + 未跟踪文件），不是 `main...HEAD`；`reviews/` 不存在，无 review round 阻塞。
- 校准：Apply ledger 已裁定的 5 项有意偏差不重复上报（events 常量位置、`RegisterRoutes`/`InboxBootSummary` 必填 waiting store、`ReadNeedsYou(ctx, limit)` 走 workspace discovery、`remote-inbox.spec.ts` fixture 完成任务、D8 无打开动作）；风格与命名差异不算漂移。

## 核对结论

1. **Task 02 本地等待行 + 合并读：Clean。** SQL 收窄（workspace、非终态、`complete_task_on_enter=0`、无 STARTING/RUNNING session、无 `queued_messages`）、Go 内 `StepRunsOnEntry` 过滤、无 session 任务可列出、`mw:` episode 身份与回退、overscan/HasMore、合并读同源（count/list/hidden/truncation）、源失败不返回部分列表、boot summary 复用同一 merge helper、sidecar 复用无新表、rows union 与 question 行行为不变，全部有实现与测试断言。
2. **Task 04 远端等待行：Clean。** 远端聚合端点按实例凭据读取、本地转发 `GET /api/v1/instances/:id/needs-you-inbox`（+hidden）、404→inbox_disabled / 401/403→unauthorized / transport→unreachable、单次 fan-out、排序与截断契约、远端行不落本地表、远端 clarification 契约未改。
3. **Task 01+03 通知桥：2 处问题，均已处置。** 见下表 F3、F4。
4. **Task 05 + 横切：4 处问题，均已处置。** 见下表 F1、F2、F5、F6。
5. **Out of Scope：无。** 清单外 9 个文件（`pkg/websocket/message.go`、`instance_routes.go`、`main.go`、`ws/router.ts`、`session-events.ts`、`app-state-types.ts`、`store.ts`、`app-sidebar-instance-picker.tsx`、`question_handlers_cross_entry_race_test.go`）逐一核实为删除模块或签名变更的必要连带改动。
6. **横切检查全部通过：** 无新增 runtime flag、无新表/迁移、被删表面无残留活引用、7 locale + pseudo 键齐全、无 U+2014、无通知打开动作且桌面插件未升级、flag-off 行为保持、通知投递不受 `features.needsYouInbox` 影响。

## 发现与处置

| # | 类别 | 位置 | 处置 |
|---|------|------|------|
| F1 | Missing | `docs/public/coverage.json:74,85` 仍引用已删除的 `notifications-section.tsx` 及其测试；`node scripts/validate-public-docs.mjs` 退出码 1，CI 的 `notify-docs.yml`、`isolated-windows-tests.yml` 会红 | 删除两条引用；复验 exit 0（56 pages） |
| F2 | Design Deviation | `docs/specs/ui/system-design/needs-you-inbox-waiting-rows.md` 称本地聚合响应"附带各实例远端页与降级状态"，与 D10 及实现（SPA 逐实例读）矛盾；AC-005.1 的 "all row kinds" 有同样歧义 | 改为"远端行按实例单独读、本地响应不含远端行"；AC-005.1 限定为本地行种并指向 AC-005.6 |
| F3 | Design Deviation | relayed 事件改走共享 pipeline 后，Local provider 以事件名作 Action 广播；对端 relay 会把该帧当新事件接收（忽略 `origin_instance_id`），在双向注册下回写台账、在传递式拓扑下把 origin 改标为中间实例，违反 delta 的 "carrying the origin instance identity" 与"同一事件被中继回源实例"失败模式 | relay `record` 跳过带 `origin_instance_id` 的帧；live socket 测试新增 already-relayed 帧并断言通知器仍只收一次 |
| F4 | Missing（低概率） | delta 的"不同实例 ID 碰撞不混淆"要求 both delivered，但投递去重键是 `(provider_id, event_type, occurrence_id)`，不含实例；该去重同时承担跨路径去重，改键会重新引入 F3 的重复提醒 | 按用户裁决改 spec 文案：`Relayed admission` 以 (instance, event, occurrence) 幂等，投递按 occurrence 跨 origin 去重；场景改为"台账保留、各自标注实例，同一逻辑 occurrence 不重复播报" |
| F5 | Missing（覆盖） | `docs/plans/.../plan.md` 列了 waiting row 的 mobile parity E2E，Results 又写未实现，而 status 为 implemented | 用户裁决不补 E2E；E2E 条目改为 `Deferred (follow-up)`，与 Results 一致 |
| F6 | 清理 | `APP_SIDEBAR_SECTION_IDS.notifications` 无引用；8 个 locale catalog 的 `notificationDismiss` 无代码引用 | 两者删除 |

## 修复后验证

- 后端：`go test ./internal/instances/relay/... -count=1` → ok（1.37s）；`gofmt -l` 无输出。
- 公开文档：`node scripts/validate-public-docs.mjs` → exit 0，56 pages。
- OpenSpec：`openspec validate --all --strict --no-interactive` → 13 passed, 0 failed。
- 规格/文档：`list-docs.py validate` → 395 decisions / 1681 specs；`lint-spec-files.py --all` → all passed。
- 前端 i18n：`pnpm --filter @kandev/web run i18n:check` → keys、`<Trans>`、复数、模块级 `t()`、em dash、非 JSX copy 六项全过。
- 前端测试：`vitest run components/app-sidebar` → 31 files / 352 tests passed。
- 类型检查：`pnpm --filter @kandev/web run typecheck` → 通过。
- 空白检查：`git diff --check -- docs openspec apps` → clean。

## 遗留（用户裁决）

- 移动端 waiting-row Playwright E2E 未实现，plan 已标记 Deferred (follow-up)，由仓库 mobile-parity 规则继续追踪。
- 跨实例 occurrence ID 碰撞不再"都播报"，是投递层跨路径去重的有意代价；已写入 spec 场景，避免契约与实现分叉。

## Overall Decision

- [x] PASS — 可进入 archive。
