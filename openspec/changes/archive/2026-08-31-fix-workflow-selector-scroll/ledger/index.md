# Apply Ledger: fix-workflow-selector-scroll

> 由 `openspec-apply-change` 在 apply 全程维护，位置 `openspec/changes/<change-id>/ledger/index.md`。
> Executor: `subagent-driven`（用户显式指定 `superpowers:subagent-driven-development`，不交互选择）。

## Pre-flight Scan

| # | 涉及 task / file | 结论 |
|---|------------------|------|
| 1 | Task 1 (`workflow-selector-row.tsx`) → Task 2 (`workflow-selector-row.test.tsx`) | Task 2 断言 `PopoverContent.className` 含 `overflow-y-auto` 与 `max-h-`，依赖 Task 1 先行；plan 已注明「若顺序颠倒，先执行 Task 1」。无冲突，按序执行。 |
| 2 | Task 3 (`create-task.spec.ts`) ↔ Task 4 (`mobile-create-task-workflow-selector.spec.ts`) | 两文件独立、测试名不重叠；均依赖 Task 1 的滚动修复。无共享文件冲突。 |
| 3 | Verification Strategy 一致性 | 首选 e2e（桌面+移动）、辅助 vitest 组件回归；与 tasks.md 4 个 task 及「验证清单」一致。 |

## Per-Task Ledger

| task id | executor | 验证方法 | 结果 | 状态 | 时间戳 |
|---------|----------|----------|------|------|--------|
| Task 1 | subagent-driven | typecheck + eslint `--max-warnings 0`（commit 3f6e6b9ab） | PASS；className 逐字节符合 spec | `done` | 2026-08-31 13:22 |
| Task 2 | subagent-driven | vitest run + eslint + typecheck（commit ebdc5c5ff） | PASS；1 test 断言 `overflow-y-auto` 与 `max-h-` | `done` | 2026-08-31 13:31 |
| Task 3 | subagent-driven | playwright chromium -g "scrolls the workflow selector"（commit 90923a3cb） | PASS；1 passed | `done` | 2026-08-31 13:48 |
| Task 4 | subagent-driven | playwright --config e2e/playwright.config.ts --project=mobile-chrome（commit 7b71da01c） | PASS；1 passed | `done` | 2026-08-31 13:56 |
| Task 5 | subagent-driven | 真实触摸拖动 E2E（红→绿）+ 桌面/移动 E2E + vitest + typecheck + eslint（commit a6e0c76f9） | PASS；红：scrollTop 恒 0；绿：1 passed×2 | `done` | 2026-08-31 15:40 |

## Rulings

- **Task 1 Ruling**: plan 的 Step 1 内联多行 JSX 与 Step 2「通过，无报错」互斥——prettier 强制 className 独占一行，使 `WorkflowSelectorRow` 函数 103 行 > 100（`max-lines-per-function`），在 `--max-warnings 0` 提交门禁下失败。按 repo CLAUDE.md「hit a limit: extract a helper」提取模块常量 `POPOVER_CONTENT_CLASS`，渲染 className 逐字节不变。Spec（行为权威）完全满足。成本若错：仅格式形态偏离 plan 文本，无行为差异。
- **Task 5 Ruling**: 对齐 combobox 的 3 个新增属性（`pointer-events-auto` + `portalContainer` + `onWheel`）使 `WorkflowSelectorRow` 从 100 行增至 106 行，再次触发 `max-lines-per-function`。按 CLAUDE.md 提取 `workflows.map(...)` 主体为模块局部 `WorkflowOptionList`（`setOpen(false)` → `closePopover()`，行为逐字节等价）。Reviewer 确认提取行为保持、`portalContainer={null}` 对 config-section 消费者安全回退 body portal。成本若错：仅组件拆分，无行为差异。

## Exit Gate

- [x] **Verification Strategy 入口通过**（Round 1 追加 Task 5 后重跑）：
  - 移动端真实触摸滚动 E2E `playwright --config e2e/playwright.config.ts tests/task/mobile-create-task-workflow-selector.spec.ts --project=mobile-chrome` → `1 passed (3.6s)`（含 CDP 触摸拖动断言 `scrollTop` 变化，修复前 `scrollTop` 恒 0 复现）。
  - 桌面 E2E `tests/task/create-task.spec.ts --project=chromium -g "scrolls the workflow selector"` → `1 passed (4.1s)`。
  - 组件回归 `vitest run components/workflow-selector-row.test.tsx` → `1 passed`；typecheck exit 0；eslint `--max-warnings 0` exit 0。
- [x] **`openspec validate fix-workflow-selector-scroll --strict` 通过**：`Change 'fix-workflow-selector-scroll' is valid`
- [x] **验收方法与证据**：
  - 方法：生产 Vite 构建上跑 Playwright 桌面 + 移动两条 E2E（移动端含真实 CDP 触摸拖动断言），外加 vitest 组件回归、typecheck、eslint、openspec validate。
  - 场景：多工作流（15 条 `Scroll WF 0..14`）下打开新建任务弹窗 → 工作流选择器 Popover 出现纵向滚动区域 → 移动端真实触摸拖动后 `scrollTop` 变化 → 滚动并选中最后一条 → trigger 回填；移动端额外断言无横向溢出。
  - 结果：Task 1-4 修复高度约束，Task 5（round-1 review 追加）修复移动端触摸滚动（对齐 `combobox.tsx`：`pointer-events-auto` + `portalContainer` + `onWheel stopPropagation`）；全部验证通过。红→绿证据：修复前 `scrollTop` 0→0（5s 超时失败），修复后 1 passed。
