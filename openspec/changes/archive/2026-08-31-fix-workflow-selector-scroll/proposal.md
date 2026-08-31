## Why

新建任务弹窗里，当已配置的工作流较多时，工作流选择器下拉列表没有任何垂直滚动，超出视口的工作流既看不到也无法选中，移动端和桌面端同样受影响。这是 `WorkflowSelectorRow` 组件缺少高度约束的实现缺陷，属于「应该能滚动浏览/选择，实际却溢出不可达」的现有行为偏离，现在修复以避免多工作流用户无法创建目标工作流的任务。

## What Changes

**工作流选择器下拉列表的高度与滚动约束**
- From: `WorkflowSelectorRow` 的 `PopoverContent` 类名仅 `w-auto min-w-[300px] max-w-none p-1`，无 `max-h` / `overflow` 约束；`workflows.map(...)` 将所有工作流平铺渲染，且 Popover 默认 `portal=true` 渲染在 body 层，列表无限增高，超出视口部分不可见、不可点。
- To: 给该 `PopoverContent`（及承载列表的容器）加上 `max-h` + `overflow-y-auto overscroll-contain` 约束，对齐同表单 `Combobox`（`combobox.tsx:220` 的 `max-h-[var(--radix-popover-content-available-height)]`）与仓库内同类列表浮层的既有写法，使多工作流场景下列表纵向滚动、全部可浏览可选。
- Reason: 修复多工作流场景下部分工作流不可见、不可选的缺陷。
- Impact: non-breaking；纯前端视觉/可用性修复，不改数据、接口与工作流语义。

**移动端同源修复**
- From: `app/tasks/mobile-tasks-create-dialog.tsx` 复用 `TaskCreateDialog` → 同一 `WorkflowSelectorRow`，无独立 Drawer/Sheet 替代路径，移动端同样无滚动。
- To: 上述滚动约束在移动端同样生效（Popover 可用高度随视口收缩）。
- Reason: 用户明示移动端与桌面端均受影响。
- Impact: non-breaking；不改组件复用结构。

## Capabilities

### New Capabilities

- `workflow-selector`: 新建任务弹窗中的工作流选择器——其下拉列表在选项较多时必须约束自身高度并纵向滚动，保证桌面端与移动端都能完整浏览并选中全部工作流。

### Modified Capabilities

（无：`openspec/specs/` 目前仅有 `runtime-debugging`，无覆盖该 UI surface 的既有 capability spec；本 change 新增 `workflow-selector` capability 固化正确行为。）

## Triage

- **现象**：预期「工作流多时列表可滚动查看/选择全部」；实际「列表无滚动，超出视口部分不可见且不可选」，桌面端与移动端一致。
- **受影响 spec**：无既有 spec 覆盖该 UI surface（`openspec/specs/` 仅 `runtime-debugging`）；本 change 新增 `workflow-selector` capability delta 描述正确行为。
- **性质判定**：A 实现 bug——正确行为（多选项可滚动浏览/选择）是既有可用性预期，`WorkflowSelectorRow` 组件实现缺少滚动约束，偏离该预期。
- **复现**：读代码即确认。`apps/web/components/workflow-selector-row.tsx:128` 的 `PopoverContent` 类名为 `w-auto min-w-[300px] max-w-none p-1`，无 `max-h`/`overflow`；`:144` 起 `workflows.map(...)` 平铺渲染全部项；`apps/packages/ui/src/popover.tsx:20` Popover 默认 `portal=true` 渲染于 body 层，弹窗内部滚动区域帮不上忙。
- **根因初判**：`WorkflowSelectorRow` 的 `PopoverContent` 缺少 `max-h` + `overflow-y-auto` 约束。

## Research Log

### 未知 1：工作流选择器实现位置与滚动缺失根因
- **未知描述**：定位「新建任务选择工作流」的组件与渲染路径，确认无滚动的根因是否在组件本身。
- **验证方式**：读代码。
- **结论（试通证据）**：`WorkflowSelectorRow`（`apps/web/components/workflow-selector-row.tsx`）用 Radix `Popover` 渲染选项，`PopoverContent` 无任何高度/滚动约束，`workflows.map` 平铺全部按钮；`TaskCreateDialog` 的 `WorkflowSection` 在 `workflows.length > 1` 时渲染它。
- **证据摘要**：`workflow-selector-row.tsx:128`（PopoverContent 类名）、`:144-192`（平铺列表）；`task-create-dialog-form-body.tsx:303`（`workflows.length > 1` 入口）；`apps/packages/ui/src/popover.tsx:20`（默认 portal）。

### 未知 2：既有正确写法可复用
- **未知描述**：仓库内是否已有「浮层列表约束高度并滚动」的成熟写法，避免发明新方案。
- **验证方式**：读代码、grep 同类列表浮层类名。
- **结论（试通证据）**：同表单 `Combobox`（agent/executor 选择器）已用 `max-h-[var(--radix-popover-content-available-height)]` + `pointer-events-auto`；仓库内多个列表浮层用 `max-h-[...] overflow-y-auto overscroll-contain`。
- **证据摘要**：`combobox.tsx:220`；`components/settings/mode-combobox.tsx:63`、`components/review/review-pr-selector.tsx:82`、`components/runs/run-filters.tsx:41`。

### 未知 3：移动端是否同源受影响
- **未知描述**：确认移动端是否走同一组件（决定是否单点修复即可覆盖两端）。
- **验证方式**：读代码 + grep `WorkflowSelectorRow` 引用。
- **结论（试通证据）**：移动端 `app/tasks/mobile-tasks-create-dialog.tsx` 复用 `TaskCreateDialog`，最终渲染同一 `WorkflowSelectorRow`；grep 确认该组件仅被 `task-create-dialog-form-body.tsx` 与 `automations/config-section.tsx` 引用，无移动端 Drawer/Sheet 替代路径。
- **证据摘要**：`mobile-tasks-create-dialog.tsx:28`（`<TaskCreateDialog .../>`）；grep `WorkflowSelectorRow` 引用结果。

## Impact

- **代码**：`apps/web/components/workflow-selector-row.tsx`（主要改动：`PopoverContent` 及列表容器加 `max-h` + `overflow-y-auto overscroll-contain`）。可能顺带核对 `components/automations/config-section.tsx` 这一同组件的另一消费方是否受益（若其复用同一选择器则自动受益，无需额外改动）。
- **接口**：无后端、无 API、无数据模型变更。
- **依赖**：无新增依赖；复用既有 `@kandev/ui/popover` 与 Tailwind 工具类。
- **测试**：新增/更新 `workflow-selector-row` 的组件测试，覆盖「多工作流时列表受高度约束并出现纵向滚动容器」；若存在 Playwright E2E 覆盖，补一条多工作流场景断言。按 `apps/web/CLAUDE.md` 在 plan/apply 阶段加载 `mobile-parity` skill 验证桌面/移动两端能力对等。
- **i18n**：无新增用户可见文案。
