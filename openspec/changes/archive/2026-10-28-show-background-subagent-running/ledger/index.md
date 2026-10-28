# Ledger — show-background-subagent-running

## Per-Task Ledger

| task | executor | 验证方法 | 结果 | 状态 | 时间戳 |
|------|----------|----------|------|------|--------|
| Task 1 | inline（用户显式指定） | 聚焦 vitest 3 文件（subagent-status.test.ts 先红后绿；三文件 58/58 PASS） | PASS | done | 2026-10-27T22:49+08:00 |
| Task 2 | inline（用户显式指定） | 聚焦 vitest subagent-status.test.ts（新 describe 先红 16 用例后绿；全文件 37/37 PASS） | PASS | done | 2026-10-27T22:53+08:00 |
| Task 3 | inline（用户显式指定） | 聚焦 vitest 组件+纯函数 70/70 PASS；i18n:check（Node 24.22.1，六子项全过）；i18n:ratchet 2 added+12 modified clean；typecheck PASS（本机 tsc 需 NODE_OPTIONS=--max-old-space-size=12288，默认堆 OOM 属环境特性） | PASS | done | 2026-10-27T23:06+08:00 |
| Task 4 | inline（用户显式指定） | 聚焦 vitest turn-group-message.test.tsx（后台用例先红后绿；8/8 PASS，含全终态守卫用例）；eslint 两文件 0 警告（复杂度超限已抽 isSubagentMessageRunning helper、重复字面量已常量化） | PASS | done | 2026-10-27T23:09+08:00 |
| Task 5 | inline（用户显式指定） | 全量验证收尾（Step 1 聚焦 vitest 78/78；Step 2 typecheck/lint/i18n:check/i18n:ratchet；Step 3 范围自检；Step 4 openspec validate） | PASS | done | 2026-10-27T23:12+08:00 |

## Exit Gate

- **review 阻塞检查**：`reviews/state.json` 不存在（无 collecting review session、无 applyBlocked），不阻塞。
- **门禁 1 — Verification Strategy 入口通过**：PASS。逐条真实执行：
  - 聚焦 vitest 3 文件（subagent-status.test.ts / tool-subagent-message.test.tsx / turn-group-message.test.tsx）：**78/78 PASS**。
  - `pnpm -C apps/web run typecheck`：PASS（注意：本机默认堆下 tsc native OOM，需 `NODE_OPTIONS=--max-old-space-size=12288` 与 Node 24，属环境特性，非代码问题）。
  - `pnpm -C apps --filter @kandev/web lint`：PASS（0 error / 0 warning）。
  - `pnpm -C apps/web run i18n:check`：PASS（Node 24.22.1；六子项：keys/Trans indices/inline plurals/module-scope t/em-dash/nonjsx-copy 全过；ja、ko、pt-pt、zh-cn、zh-hk、zh-tw complete）。
  - `pnpm -C apps/web run i18n:ratchet`：PASS（2 added + 12 modified clean；allowlist 643 entries intact）。
- **门禁 2 — `openspec validate show-background-subagent-running --strict --no-interactive`**：PASS（"Change is valid"）。
- **门禁 3 — 验收方法与证据**：验收为组件级集成测试（真实组件 + 真实 en catalog 渲染）+ 纯函数表驱动单测，覆盖 design.md「E2E 测试图」全部 6 条用户路径：
  1. turn 活跃期派发后台卡 → `tool-subagent-message.test.tsx`「shows the background-running label after spawn completes...」（含 spinner role=status、后台标签、无通用 Working）。
  2. turn 结束后后台态+折叠+meta chip → 「keeps the background-running state after the containing turn ends」（无完成勾、`subagent-meta-background` chip 可见）。
  3. Cursor `is_async` 形状 → 「shows the background-running state for an is_async payload without status」。
  4. 折叠组头运行指示 → `turn-group-message.test.tsx`「keeps the group running indicator while a background subagent runs after the turn ends」。
  5. 无信号永久停留（无翻转路径）→ `subagent-status.test.ts` 纯解析用例「async + tool complete + turn inactive → background」钉死 payload 驱动、不依赖 tool status/turn 活跃性；spec 边界记录「无完成信号时保持后台运行态」「不用粗粒度计数伪造完成」。
  6. 同步回归护栏 → 既有用例未动且全绿（Codex stale 失活 :297 用例、同步 working/terminal 全表、组内全终态无指示守卫用例）。
  - 结果：以上命令与用例在本会话真实执行，全部通过；证据即各 Task ledger 行与本节命令输出。

**结论**：三项门禁全部满足，apply 阶段完成。后续建议运行 `openspec-verify-change` 检查漂移。
