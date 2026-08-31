# tasks: fix-workflow-selector-scroll

## Verification Strategy

- **验证类型：e2e-script（首选）**。核心验收是两条 Playwright 路径——桌面端与移动端各一条——证明「多工作流时选择器列表受高度约束并纵向滚动、最末尾工作流可被选中」。
- **组件回归（unit-test，辅助）**：一条 vitest 断言 `WorkflowSelectorRow` 传给 `PopoverContent` 的 `className` 含 `overflow-y-auto` 与 `max-h` 约束，锁住 class 契约，防止后续误删。
- 最终验收以 E2E 通过为准；组件测试作为快速回归防线，不替代 E2E。

## E2E 测试图

1. **桌面端**（`chromium` 项目，Desktop Chrome）：
   1. 打开创建任务弹窗（sidebar New Task）。
   2. 点 `workflow-selector-trigger` 打开工作流选择 Popover。
   3. 断言 Popover 内部出现纵向滚动区域（`scrollHeight > clientHeight`）。
   4. 滚动并点击列表中最后一个工作流。
   5. 断言 `workflow-selector-trigger` 回填该工作流名称。

2. **移动端**（`mobile-chrome` 项目，Pixel 5）：
   1. 移动端 kanban FAB（`mobile-fab`）打开创建任务弹窗。
   2. 点 `workflow-selector-trigger` 打开同一 Popover。
   3. 断言列表在窄视口下仍可滚动并选中最后一个工作流。
   4. 断言页面无横向溢出（`scrollWidth <= clientWidth`）。

## 实现清单

### Task 1: 给工作流选择器 PopoverContent 加高度与滚动约束

**Files:**
- Modify: `apps/web/components/workflow-selector-row.tsx:128`

**Interfaces:**
- 无新增接口；仅改 `PopoverContent` 的 `className`。
- Produces: `WorkflowSelectorRow` 的下拉列表在内容超过可用高度时出现纵向滚动，类名含 `max-h-[var(--radix-popover-content-available-height)] overflow-y-auto overscroll-contain`。

- [x] **Step 1: 修改 `PopoverContent` 类名**

将 `apps/web/components/workflow-selector-row.tsx` 中：

```tsx
<PopoverContent className="w-auto min-w-[300px] max-w-none p-1" align="start">
```

改为：

```tsx
<PopoverContent
  className="w-auto min-w-[300px] max-w-none p-1 max-h-[var(--radix-popover-content-available-height)] overflow-y-auto overscroll-contain"
  align="start"
>
```

> 注：内联多行 JSX 会触发 `max-lines-per-function`（103 > 100）并使 eslint `--max-warnings 0` 门禁失败，故按 CLAUDE.md 提取模块常量 `POPOVER_CONTENT_CLASS`，渲染 className 逐字节不变（见 ledger Ruling）。

- [x] **Step 2: 运行类型检查与 lint**

Run: `cd apps/web && pnpm run typecheck && pnpm exec eslint components/workflow-selector-row.tsx`
Expected: 通过，无报错。

- [x] **Step 3: Commit**

```bash
git add apps/web/components/workflow-selector-row.tsx
git commit -m "fix: constrain workflow selector popover height with scroll"
```

### Task 2: 组件回归测试锁定 scroll class 契约

**Files:**
- Create: `apps/web/components/workflow-selector-row.test.tsx`

**Interfaces:**
- Consumes: `WorkflowSelectorRow`（`components/workflow-selector-row.tsx`）的 props 形状（见该文件 `WorkflowSelectorRowProps`）。
- Produces: 测试断言 `PopoverContent` 收到的 `className` 含 `overflow-y-auto` 与 `max-h-`。

- [x] **Step 1: 写失败测试**

创建 `apps/web/components/workflow-selector-row.test.tsx`：

```tsx
import { fireEvent, render, screen } from "@testing-library/react";
import { type ReactElement, type ReactNode, cloneElement } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { WorkflowSelectorRow } from "./workflow-selector-row";

let capturedContentClassName: string | undefined;

vi.mock("@kandev/ui/popover", () => ({
  Popover: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  PopoverTrigger: ({ children }: { children: ReactElement<{ onClick?: () => void }> }) =>
    cloneElement(children, { onClick: () => capturedContentClassName !== undefined }),
  PopoverContent: ({ children, className }: { children: ReactNode; className?: string }) => {
    capturedContentClassName = className;
    return <div>{children}</div>;
  },
}));

vi.mock("@kandev/ui/tooltip", () => ({
  Tooltip: ({ children }: { children: ReactNode }) => <>{children}</>,
  TooltipTrigger: ({ children }: { children: ReactNode }) => <>{children}</>,
  TooltipContent: ({ children }: { children: ReactNode }) => <>{children}</>,
}));

afterEach(() => {
  capturedContentClassName = undefined;
});

const workflows = Array.from({ length: 10 }, (_, i) => ({
  id: `wf-${i}`,
  name: `Workflow ${i}`,
}));

describe("WorkflowSelectorRow popover scroll constraint", () => {
  it("constrains the popover list height and enables vertical scroll", () => {
    render(
      <WorkflowSelectorRow
        workflows={workflows}
        snapshots={{}}
        selectedWorkflowId={null}
        onWorkflowChange={vi.fn()}
        agentProfiles={[]}
      />,
    );
    fireEvent.click(screen.getByTestId("workflow-selector-trigger"));
    expect(capturedContentClassName).toContain("overflow-y-auto");
    expect(capturedContentClassName).toContain("max-h-");
  });
});
```

- [x] **Step 2: 运行测试确认失败**

Run: `cd apps/web && pnpm vitest run components/workflow-selector-row.test.tsx`
Expected: FAIL——`capturedContentClassName` 为 `w-auto min-w-[300px] max-w-none p-1`，不含 `overflow-y-auto`。

- [x] **Step 3: 确认修复后测试通过**

（Task 1 已改组件；若顺序颠倒，先执行 Task 1。）

Run: `cd apps/web && pnpm vitest run components/workflow-selector-row.test.tsx`
Expected: PASS。

- [x] **Step 4: Commit**

```bash
git add apps/web/components/workflow-selector-row.test.tsx
git commit -m "test: lock workflow selector popover scroll classes"
```

### Task 3: 桌面端 E2E——多工作流可滚动并选中最后一个

**Files:**
- Modify: `apps/web/e2e/tests/task/create-task.spec.ts`（追加一个 test）

**Interfaces:**
- Consumes: `apiClient.createWorkflow(workspaceId, name, "simple")`（`e2e/helpers/api-client.ts:406`）、`apiClient.deleteWorkflow(id)`、`seedData.workspaceId`、`KanbanPage`（`e2e/pages/kanban-page.ts`）。
- Produces: 断言 Popover 出现内部纵向滚动，且最末尾工作流可被选中并回填 trigger。

- [x] **Step 1: 追加桌面 E2E 测试**

在 `apps/web/e2e/tests/task/create-task.spec.ts` 的 `test.describe("Task creation", ...)` 内追加：

```tsx
test("scrolls the workflow selector when many workflows are configured", async ({
  testPage,
  apiClient,
  seedData,
}) => {
  const created = [];
  for (let i = 0; i < 15; i += 1) {
    created.push(
      await apiClient.createWorkflow(seedData.workspaceId, `Scroll WF ${i}`, "simple"),
    );
  }
  try {
    const kanban = new KanbanPage(testPage);
    await kanban.goto();
    await kanban.createTaskButton.first().click();

    const dialog = testPage.getByTestId("create-task-dialog");
    await expect(dialog).toBeVisible();
    await dialog.getByTestId("workflow-selector-trigger").click();

    const popover = testPage.locator('[data-slot="popover-content"]').filter({
      hasText: "Scroll WF",
    });
    await expect(popover).toBeVisible();

    const overflows = await popover.evaluate(
      (el) => el.scrollHeight > el.clientHeight,
    );
    expect(overflows).toBe(true);

    const last = popover.getByRole("button", { name: /Scroll WF 14/ });
    await last.scrollIntoViewIfNeeded();
    await last.click();

    await expect(dialog.getByTestId("workflow-selector-trigger")).toContainText("Scroll WF 14");
  } finally {
    await Promise.all(
      created.map((wf) => apiClient.deleteWorkflow(wf.id).catch(() => {})),
    );
  }
});
```

- [x] **Step 2: 运行桌面 E2E**

Run: `cd apps/web && pnpm exec playwright test tests/task/create-task.spec.ts --project=chromium -g "scrolls the workflow selector"`（需先构建前端，见「验证清单」）
Expected: PASS。

- [x] **Step 3: Commit**

```bash
git add apps/web/e2e/tests/task/create-task.spec.ts
git commit -m "test(e2e): cover workflow selector scroll on desktop"
```

### Task 4: 移动端 E2E——窄视口下同样可滚动并选中

**Files:**
- Create: `apps/web/e2e/tests/task/mobile-create-task-workflow-selector.spec.ts`

**Interfaces:**
- Consumes: `MobileKanbanPage`（`e2e/pages/mobile-kanban-page.ts`，`mobileFab` locator）、`apiClient.createWorkflow` / `deleteWorkflow`、`seedData.workspaceId`。
- Produces: 移动端（Pixel 5，自动归 `mobile-chrome` 项目）断言列表可滚动、可选中最后一个工作流，且无横向溢出。

- [x] **Step 1: 新建移动端 E2E 测试**

创建 `apps/web/e2e/tests/task/mobile-create-task-workflow-selector.spec.ts`：

```tsx
import { test, expect } from "../../fixtures/test-base";
import { MobileKanbanPage } from "../../pages/mobile-kanban-page";

test.describe("Mobile create-task workflow selector", () => {
  test("scrolls the workflow selector on a narrow viewport", async ({
    testPage,
    apiClient,
    seedData,
  }) => {
    const created = [];
    for (let i = 0; i < 15; i += 1) {
      created.push(
        await apiClient.createWorkflow(seedData.workspaceId, `Scroll WF ${i}`, "simple"),
      );
    }
    try {
      const mobile = new MobileKanbanPage(testPage);
      await mobile.goto();
      await mobile.mobileFab.click();

      const dialog = testPage.getByRole("dialog");
      await expect(dialog.getByTestId("workflow-selector-trigger")).toBeVisible();
      await dialog.getByTestId("workflow-selector-trigger").click();

      const popover = testPage.locator('[data-slot="popover-content"]').filter({
        hasText: "Scroll WF",
      });
      await expect(popover).toBeVisible();

      const overflows = await popover.evaluate(
        (el) => el.scrollHeight > el.clientHeight,
      );
      expect(overflows).toBe(true);

      const last = popover.getByRole("button", { name: /Scroll WF 14/ });
      await last.scrollIntoViewIfNeeded();
      await last.click();

      await expect(dialog.getByTestId("workflow-selector-trigger")).toContainText("Scroll WF 14");

      const pageWidth = await testPage.evaluate(() => ({
        scroll: document.documentElement.scrollWidth,
        client: document.documentElement.clientWidth,
      }));
      expect(pageWidth.scroll).toBeLessThanOrEqual(pageWidth.client);
    } finally {
      await Promise.all(
        created.map((wf) => apiClient.deleteWorkflow(wf.id).catch(() => {})),
      );
    }
  });
});
```

- [x] **Step 2: 运行移动端 E2E**

Run: `cd apps/web && pnpm exec playwright test tests/task/mobile-create-task-workflow-selector.spec.ts --project=mobile-chrome`（需先构建前端，见「验证清单」）
Expected: PASS。

- [x] **Step 3: Commit**

```bash
git add apps/web/e2e/tests/task/mobile-create-task-workflow-selector.spec.ts
git commit -m "test(e2e): cover workflow selector scroll on mobile"
```

## 验证清单

- [x] `cd apps/web && pnpm run typecheck` 通过
- [x] `cd apps/web && pnpm exec eslint components/workflow-selector-row.tsx components/workflow-selector-row.test.tsx` 通过
- [x] `cd apps/web && pnpm vitest run components/workflow-selector-row.test.tsx` 通过
- [x] `make build-web`（或 `cd apps && pnpm --filter @kandev/web build:vite`）——E2E 跑在生产 Vite 构建上，前端改动后必须重建，否则测到旧代码
- [x] `cd apps/web && pnpm exec playwright test tests/task/create-task.spec.ts --project=chromium -g "scrolls the workflow selector"` 通过
- [x] `cd apps/web && pnpm exec playwright test tests/task/mobile-create-task-workflow-selector.spec.ts --project=mobile-chrome` 通过
- [x] `openspec validate fix-workflow-selector-scroll --strict --no-interactive` 通过
- [x] 观察点：若手工/E2E 发现滚动列表时背后弹窗被连带滚动，按 `components/combobox.tsx:227` 给 `PopoverContent` 补 `onWheel={(e) => e.stopPropagation()}`（本轮不预埋）——本轮未观察到连带滚动，两条 E2E 均无需 `onWheel` 即通过，故不预埋。

## Round 1 review 追加任务

> 用户反馈「移动端还是不行，无法滚动」后追加（见 `reviews/1.md`）。

### Task 5: 移动端工作流选择器对齐 combobox 的 PopoverContent 接线，恢复真实触摸滚动

**Files:**
- Modify: `apps/web/components/workflow-selector-row.tsx`

**Interfaces:**
- Consumes: `useTaskCreateDialogPopoverContainer()`（`hooks/use-task-create-dialog-popover-container.tsx`）——与 `combobox.tsx:181` 相同。
- Produces: `WorkflowSelectorRow` 的 PopoverContent 渲染进 `TaskCreateDialog` 的 dialog 容器（而非 `document.body`），并带 `pointer-events-auto` + `onWheel stopPropagation`，与 `combobox.tsx:218-227` 一致。

- [x] **Step 1: 对齐 PopoverContent 接线**

将 `apps/web/components/workflow-selector-row.tsx` 中 `WorkflowSelectorRow` 组件的 PopoverContent 改为与 `components/combobox.tsx` 相同的接线：

1. 组件内 `const portalContainer = useTaskCreateDialogPopoverContainer();`
2. `PopoverContent` 增加 `portal={false}`（或按 combobox 用 `portalContainer`）、`portalContainer={portalContainer}`、`pointer-events-auto`（追加进 `POPOVER_CONTENT_CLASS` 或 className）、`onWheel={(event) => event.stopPropagation()}`。
3. 保持 `max-h-[var(--radix-popover-content-available-height)] overflow-y-auto overscroll-contain` 不变。

注意 `useTaskCreateDialogPopoverContainer()` 在非 dialog 上下文（`automations/config-section.tsx`）返回 `null`；`PopoverContent` 的 `portalContainer` 为 `null` 时按 `@kandev/ui/popover.tsx` 回退到 body portal，不影响该消费方。

- [x] **Step 2: 红——用真实触摸滚动复现移动端不可滚动**

在 `apps/web/e2e/tests/task/mobile-create-task-workflow-selector.spec.ts` 增加/修改断言：用 `testPage.touchscreen` 或 `locator.dispatchEvent("wheel"/"touch")` 在 popover 上真实拖动，断言列表 `scrollTop` 变化；修复前应失败（复现）。若现有 spec 已用 `scrollIntoViewIfNeeded` 编程式滚动，替换/补充为真实触摸拖动断言。

- [x] **Step 3: 绿——接线后触摸滚动生效，桌面回归不回归**

重跑：`cd apps/web && pnpm exec playwright test tests/task/mobile-create-task-workflow-selector.spec.ts --project=mobile-chrome` 与 `pnpm exec playwright test tests/task/create-task.spec.ts --project=chromium -g "scrolls the workflow selector"`，均 PASS；`pnpm vitest run components/workflow-selector-row.test.tsx` PASS（若 Step 1 改动 className 形态需同步更新该测试）。

- [x] **Step 4: Commit**

```bash
git add apps/web/components/workflow-selector-row.tsx apps/web/e2e/tests/task/mobile-create-task-workflow-selector.spec.ts
git commit -m "fix: make workflow selector popover scrollable on mobile"
```
