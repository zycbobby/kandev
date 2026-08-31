// This file starts with `mobile-` so Playwright runs it on the Pixel 5 project.
import { expect, type Locator, type Page } from "@playwright/test";
import { test } from "../../fixtures/test-base";
import { MobileKanbanPage } from "../../pages/mobile-kanban-page";

/** CDP touch-drags the popover upward, which should scroll its content DOWN. */
async function touchScrollDown(page: Page, scrollElement: Locator) {
  const box = await scrollElement.boundingBox();
  if (!box) throw new Error("popover scroll container has no bounding box");
  const cdp = await page.context().newCDPSession(page);
  const centerX = box.x + box.width / 2;
  const startY = box.y + box.height - 20;
  const endY = box.y + 20;
  await cdp.send("Input.dispatchTouchEvent", {
    type: "touchStart",
    touchPoints: [{ x: centerX, y: startY }],
  });
  for (let i = 1; i <= 8; i += 1) {
    const y = startY + ((endY - startY) * i) / 8;
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchMove",
      touchPoints: [{ x: centerX, y }],
    });
  }
  await cdp.send("Input.dispatchTouchEvent", {
    type: "touchEnd",
    touchPoints: [],
  });
}

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

      const overflows = await popover.evaluate((el) => el.scrollHeight > el.clientHeight);
      expect(overflows).toBe(true);

      // Real touch drag: the popover list must scroll without a mouse wheel.
      const scrollTopBefore = await popover.evaluate((el) => el.scrollTop);
      await touchScrollDown(testPage, popover);
      await expect
        .poll(() => popover.evaluate((el) => el.scrollTop), {
          timeout: 5_000,
          message: "Touch drag should scroll the workflow list",
        })
        .toBeGreaterThan(scrollTopBefore);

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
      await Promise.all(created.map((wf) => apiClient.deleteWorkflow(wf.id).catch(() => {})));
    }
  });
});
