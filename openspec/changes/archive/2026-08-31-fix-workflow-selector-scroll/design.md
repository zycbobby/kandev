# design: fix-workflow-selector-scroll

## Context

新建任务弹窗（`TaskCreateDialog`）中的工作流选择器 `WorkflowSelectorRow` 用 Radix `Popover` 渲染工作流列表。当工作流较多时，`PopoverContent`（`apps/web/components/workflow-selector-row.tsx:128`）类名仅为 `w-auto min-w-[300px] max-w-none p-1`，没有任何 `max-h` / `overflow` 约束；`workflows.map(...)`（`:144`）将所有工作流平铺渲染为 `<button>`，且 `@kandev/ui/popover` 默认 `portal=true` 渲染在 body 层（`apps/packages/ui/src/popover.tsx:20`）。结果是列表无限增高、超出视口的工作流不可见且不可点，桌面端与移动端同源受影响（移动端 `app/tasks/mobile-tasks-create-dialog.tsx` 复用同一 `TaskCreateDialog` → `WorkflowSelectorRow`，无 Drawer/Sheet 替代路径）。

## Goals

- 工作流较多时，选择器下拉列表受视口可用高度约束并纵向滚动，全部工作流可浏览、可选中。
- 桌面端与移动端行为一致（同一修复覆盖两端）。
- 工作流较少时不强制出现滚动条。

## Non-Goals

- 不把 Popover 改造成移动端 Drawer/Sheet（用户未要求，且属独立增强）。
- 不限制列表宽度（`max-w-none` 保持现状，横向溢出不在本 change 范围）。
- 不改工作流数据、接口或选择语义。

## Decisions

### D1：给 `PopoverContent` 增加高度与滚动约束，而非引入新滚动组件

给 `WorkflowSelectorRow` 的 `PopoverContent` 追加 `max-h-[var(--radix-popover-content-available-height)] overflow-y-auto overscroll-contain`。

- 依据：同表单的 `Combobox`（`components/combobox.tsx:220`）已用 `max-h-[var(--radix-popover-content-available-height)]`，该 CSS 变量由 Radix 提供（内容距锚点与视口边缘的可用高度），在仓库内已被证明有效；`overflow-y-auto overscroll-contain` 是仓库内列表浮层的通用写法（`components/review/review-pr-selector.tsx:82`、`components/settings/mode-combobox.tsx:63`、`components/runs/run-filters.tsx:41`）。
- 取舍：相比引入 `@kandev/ui/scroll-area` 或把手动拆分「固定 header + 滚动列表」，直接在 PopoverContent 上约束高度最小、改动最少；header（`工作流` 标签）会随列表一起滚动，属可接受的折中，不单独固定。

### D1.1（Round 1 review 追加）：移动端需对齐 combobox 的 PopoverContent 接线

仅加 `max-h + overflow-y-auto` 在移动端不生效——`@kandev/ui/popover.tsx` 默认 `portal=true` 且无 `portalContainer`，`WorkflowSelectorRow` 的 PopoverContent 渲染进 `document.body`，而 modal `TaskCreateDialog` 打开期间 Radix 给 `<body>` 设 `pointer-events: none` + scroll lock（`apps/packages/ui/src/lib/dialog-body-lock.ts`），导致移动端触摸滚动/点击被拦截。

- 修正：`WorkflowSelectorRow` 复用 `useTaskCreateDialogPopoverContainer()`，让 PopoverContent 渲染进 dialog 容器，并追加 `pointer-events-auto` + `onWheel={(e) => e.stopPropagation()}`，与 `combobox.tsx:181/220/225-227` 完全一致。非 dialog 上下文（`automations/config-section.tsx`）该 hook 返回 `null`，PopoverContent 回退 body portal，不受影响。
- 验证：移动端 E2E 改用真实触摸拖动（而非 `scrollIntoViewIfNeeded` 编程式滚动）断言 `scrollTop` 变化。

## Risks / Trade-offs

- **Header 随列表滚动**：列表很长时顶部的「工作流」标签会滚出可视区。影响轻微，接受；若后续需要固定 header，可再做 `sticky top-0` 或拆内层滚动容器。
- **滚轮事件被弹窗容器截获**：`WorkflowSelectorRow` 渲染在 `Dialog` 内，`overflow-y-auto` 生效后，在列表上滚动时是否会连动滚动背后的弹窗，取决于 Radix 默认事件传播。当前未加 `onWheel` stopPropagation；若 E2E 或手工验证发现弹窗背景被连带滚动，再按 `combobox.tsx:227` 补 `onWheel={(e) => e.stopPropagation()}`（作为验证清单中的观察点，不预埋实现）。
- **`--radix-popover-content-available-height` 在极端小视口**：该变量随视口收缩，理论上不会溢出；移动端 E2E 覆盖此场景。

## Migration Plan

纯前端样式修复，无数据迁移、无接口变更、无持久化状态变更。上线即生效，无需回滚脚本。

## Open Questions

- 是否需要固定列表顶部 header（`工作流` 标签）？默认接受滚动，暂不处理。
- 横向溢出（长工作流名在窄视口）是否需要一并处理？本 change 不处理，列为后续候选。

## Research Log

### 未知 1：根因是否在组件本身
- **未知描述**：确认无滚动是组件缺约束，还是弹窗容器/滚动区域导致的。
- **验证方式**：读代码。
- **结论（试通证据）**：`WorkflowSelectorRow` 的 `PopoverContent` 无 `max-h`/`overflow`，`workflows.map` 平铺；Popover 默认 portal 到 body，弹窗内部滚动区域帮不上忙。
- **证据摘要**：`apps/web/components/workflow-selector-row.tsx:128`（PopoverContent 类名）、`:144-192`（平铺列表）；`apps/packages/ui/src/popover.tsx:20`（默认 portal=true）。

### 未知 2：可复用的正确写法
- **未知描述**：仓库内是否已有「浮层列表约束高度并滚动」的成熟写法。
- **验证方式**：读代码 + grep 同类列表浮层类名。
- **结论（试通证据）**：`Combobox` 已用 `max-h-[var(--radix-popover-content-available-height)]`；多个列表浮层用 `max-h-[...] overflow-y-auto overscroll-contain`。
- **证据摘要**：`components/combobox.tsx:220`；`components/review/review-pr-selector.tsx:82`；`components/settings/mode-combobox.tsx:63`；`components/runs/run-filters.tsx:41`。

### 未知 3：移动端是否同源受影响
- **未知描述**：确认移动端是否走同一组件，决定单点修复是否覆盖两端。
- **验证方式**：读代码 + grep 引用。
- **结论（试通证据）**：移动端复用 `TaskCreateDialog` → 同一 `WorkflowSelectorRow`；`WorkflowSelectorRow` 仅被 `task-create-dialog-form-body.tsx` 与 `automations/config-section.tsx` 引用，无移动端 Drawer/Sheet 替代。
- **证据摘要**：`app/tasks/mobile-tasks-create-dialog.tsx:28`；grep `WorkflowSelectorRow` 引用结果；`e2e/tests/kanban/mobile-kanban.spec.ts:333` 移动端打开同一 `workflow-selector-trigger`。

### 未知 4：测试缝合点（组件测试与 E2E 既有模式）
- **未知描述**：确认组件测试与 E2E 的既有写法，避免发明新机制。
- **验证方式**：读既有测试。
- **结论（试通证据）**：组件测试可 mock `@kandev/ui/popover` 并捕获 `PopoverContent` 的 `className`（`components/folder-picker.test.tsx:9-22` 范式）；桌面 E2E 用 `useRegularMode()` + `KanbanPage` 打开 create dialog（`e2e/tests/task/create-task.spec.ts`），移动端 E2E 用 `MobileKanbanPage.mobileFab`（`e2e/tests/kanban/mobile-kanban.spec.ts:333`），`mobile-*.spec.ts` 自动归入 `mobile-chrome`（Pixel 5）项目（`e2e/playwright.config.ts:82-85`）。工作流可用 `apiClient.createWorkflow(workspaceId, name, "simple")` 循环创建（`e2e/helpers/api-client.ts:406`）。
- **证据摘要**：上述文件行号。

## Mobile design contract（mobile-parity）

- **desktop 用户价值**：新建任务时从工作流下拉列表浏览并选中任意一个工作流。
- **mobile 入口**：移动端 kanban FAB（`mobile-fab`）打开 create dialog，点击 `workflow-selector-trigger` 打开同一工作流选择 Popover。
- **最近已上线 mobile 参照**：`e2e/tests/kanban/mobile-kanban.spec.ts`（同一 create dialog 的 workflow selector 触发与断言）。
- **信息层级与主操作**：列表以工作流名称为主，选中即关闭并回填 trigger；单一滚动容器。
- **presentation 选择**：保持现状（inline Popover），不改为底部 Drawer——任务频率低、内容为单选列表，Popover + 滚动足以承载；改为 Drawer 属独立增强，本 change 不纳入。
- **surface rationale**：一次性单选列表，Popover 的轻量浮动 + 可用高度滚动即可满足，避免为低频率任务引入新导航面。
- **单一滚动 owner / 动态视口 / safe-area / 触控目标**：滚动 owner 为 `PopoverContent` 本身；`max-h` 用 `var(--radix-popover-content-available-height)` 随视口动态收缩；safe-area 由 Radix Popover 的碰撞/翻转处理（本 change 不新增 bottom-fixed 控件）；工作流行 `min-h-11`（44px）已满足触控目标，本 change 不调整密度。
- **共享状态/业务逻辑**：选择逻辑、`snapshots`、`agentProfiles` 全部复用现有 `WorkflowSelectorRow`，无移动端专用 view-model 分叉。
- **mobile Playwright 场景**：见 `tasks.md` Task 4。
