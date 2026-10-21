import { expect, type Locator } from "@playwright/test";
import { test } from "../../fixtures/test-base";
import { SessionPage } from "../../pages/session-page";
import {
  creationBaseMotherCommit,
  creationBaseOwnCommit,
  restoreWorktreeCreationBaseRepository,
  seedWorktreeCreationBaseTask,
} from "./worktree-creation-base-comparison-helpers";

async function expandSection(toggle: Locator) {
  await expect
    .poll(
      async () => {
        if ((await toggle.getAttribute("aria-expanded")) === "true") return true;
        await toggle.tap();
        return (await toggle.getAttribute("aria-expanded")) === "true";
      },
      { timeout: 15_000 },
    )
    .toBe(true);
}

test.describe("Mobile worktree creation base comparison", () => {
  test.describe.configure({ timeout: 180_000 });

  test.afterEach(async ({ seedData, backend }) => {
    restoreWorktreeCreationBaseRepository(seedData, backend);
  });

  test("shows only this worktree's own work and the mother branch in the touch drawer", async ({
    testPage,
    apiClient,
    seedData,
    backend,
  }) => {
    const { task } = await seedWorktreeCreationBaseTask(apiClient, seedData, backend);

    await testPage.goto(`/t/${task.id}`);
    const session = new SessionPage(testPage);
    await session.waitForLoad();
    await session.waitForChatIdle({ timeout: 45_000 });
    await expect(
      testPage.getByRole("navigation").getByRole("button", { name: /Changes$/ }),
    ).toBeVisible({
      timeout: 15_000,
    });
    await testPage
      .getByRole("navigation")
      .getByRole("button", { name: /Changes$/ })
      .tap();

    const changes = testPage.getByTestId("mobile-changes-panel");
    await expect(changes).toBeVisible({ timeout: 15_000 });
    const commitsToggle = changes.getByTestId("commits-section-collapse-toggle");
    await expect(commitsToggle).toBeVisible({ timeout: 45_000 });
    await expandSection(commitsToggle);

    await expect(changes.locator('[data-testid^="commit-row-"]')).toHaveCount(1);
    await expect(changes).toContainText(creationBaseOwnCommit);
    await expect(changes).not.toContainText(creationBaseMotherCommit);

    // The single commit row's stat reflects the narrowed range: only the one
    // file this worktree itself committed.
    const ownRow = changes.locator('[data-testid^="commit-row-"]').first();
    await expect(ownRow).toContainText("+1 -0");

    // The touch branch drawer surfaces the recorded mother branch.
    const branchDetails = changes.getByRole("button", {
      name: "Show branch and Git credential details",
    });
    await expect(branchDetails).toBeVisible({ timeout: 15_000 });
    await branchDetails.tap();
    const drawer = testPage.getByTestId("changes-creation-base");
    await expect(drawer).toBeVisible({ timeout: 15_000 });
    await expect(drawer).toContainText("main");
  });
});
