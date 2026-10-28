# surface-remote-inbox-bundles — Verify

**Date:** 2026-10-28
**Result:** Drift Report: **Clean**（无未实现需求、无范围外新增；1 项设计偏差已当场修复并回归验证）

## 检测方式

- 派发只读漂移检测 subagent，对照 `proposal.md`、`design.md`、`tasks.md`、`specs/cross-instance-inbox/spec.md`（6 组 Requirement）与 `git diff main...HEAD`（71 文件、+5257/−161），并按需读仓库当前源文件复核；逐 Scenario 给出实现落点与测试/e2e 断言的覆盖表。
- 校准：ledger 已裁定的 8 类有意偏差（hidden total 求和、整页降级、credential→unauthorized、T2 文件集、instance_id 盖章、workspaces 404→error、三项 drift remediation、workflow-move 对齐）不重复上报；风格/命名差异不算漂移。
- 无未收束 review round（本 change 无 `reviews/` 目录）。

## 核对结论（全部一致）

1. **Missing：无**。6 组 Requirement × 14 个 Scenario 全部有实现落点与 vitest/go/e2e 断言（覆盖表见检测过程）。
2. **Out of Scope：无**。diff 中超出 proposal 文字但未入 ledger 的改动逐一核实为支撑性细节：`switch-instance.ts` 深链 query 分隔修正（e2e 图4 断言该 URL，带测试）、`needs-you-inbox-route-loop-regression.test.tsx`、`storage_sweep_test.go` t.Cleanup（test-only）、`cross-instance-inbox` 规格）、`CompactPanelTitle` 提取（守代码量限制的重排）。
3. **重点设计决策逐条通过**：后端 fan-out 直连（不经浏览器 proxy port）；PAT 可见全部 workspace 聚合、`created_at` 升序 + `pending_id` 决胜、cap=limit、默认 50 与本地 inbox 逐参数一致；远端行零本地表写入；200/409 字节透传、拨号失败 502 `remote_unreachable`、远端 404 respond 走 error 不误报 not_active；降级闭集 + 降级也 200 + 降级 section 无答案组件；`respondTarget` 纯 additive（默认 URL 逐字节断言）且远端行抑制 late-answer；角标 count-known（`lastAppliedOk`，boot 只 seed 本机页）；双 flag 门控（后端路由 404 + 前端 controller 三重 gating）；触发集六项齐全；`switchInstance` 跳转；10s deadline、逐 ws 串行、Authorization 只由 credential 注入；i18n 7 语言 + pseudo、无 em dash。

## 发现并修复的偏差

- **[Design Deviation]** `instance_inbox_routes.go` pageRoute 对隐藏页路由先无条件 `ReadInbox` 再以 `ReadHidden` 整体覆盖，隐藏页请求实际付出 2×(1+W) 次远端拨号，超出 design「每实例 1+W 次串行读、10s deadline」的读放大界限（行为正确，纯效率问题；路由测试 fake 未计数 Read 调用故未拦截）。
- 修复（commit `a3fecc831`）：按 `hidden` 标志二选一调用；fake forwarder 增加 `readInboxCalls`/`readHiddenCalls` 计数，新增 `TestInstanceInboxPageRouteIssuesSingleFanOut` 断言两条路由各只发一次读，防回归。

## 测试覆盖

- 修复后：`go test -trimpath -count=1 ./internal/backendapp/` 全绿（56.6s，TMPDIR=/private/tmp）；`lint-go-changed` 两变更文件 0 issues；gofmt clean。
- Apply 阶段 exit gate 证据（ledger）：e2e `remote-inbox.spec.ts` 3/3 绿（覆盖测试图 1-7 + 降级重启重试草稿恢复 + 抢答收敛）；受影响 vitest 套件全绿；typecheck exit 0；i18n/文档四连校验通过。
