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

      const overflows = await popover.evaluate((el) => el.scrollHeight > el.clientHeight);
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
      await Promise.all(created.map((wf) => apiClient.deleteWorkflow(wf.id).catch(() => {})));
    }
  });
});
