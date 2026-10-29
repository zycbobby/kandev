// needs-you-inbox-waiting-rows (REQ-UI-NEEDS-YOU-INBOX-003/005): a task parked
// on a manual workflow step with no session and no queued turn appears as a
// waiting row in the merged Inbox read. The row names the task and the step,
// offers open/dismiss/snooze with no inline answer surface, and disappears
// once the task stops waiting.
import { test, expect } from "../../fixtures/test-base";

test.describe("Needs-you Inbox waiting rows", () => {
  test("lists a sessionless manual-step task as a waiting row counted by the merged read", async ({
    testPage,
    apiClient,
    seedData,
  }) => {
    test.setTimeout(90_000);
    const stepName = "Manual review";
    const step = await apiClient.createWorkflowStep(seedData.workflowId, stepName, 99);
    const title = "Waiting rows fixture task";
    const { task_id: taskId } = await apiClient.seedTask(seedData.workspaceId, title, {
      workflow_id: seedData.workflowId,
      workflow_step_id: step.id,
    });

    await testPage.goto("/needs-you-inbox");

    const row = testPage.getByTestId("needs-you-waiting-row").filter({ hasText: title });
    await expect(row).toBeVisible({ timeout: 30_000 });
    await expect(row).toContainText(stepName);

    // A waiting row has no inline answer surface: no expandable question
    // panel, so the question row's toggle control must not exist inside it.
    await expect(row.getByTestId("needs-you-inbox-row-toggle")).toHaveCount(0);
    await expect(row.getByTestId("needs-you-inbox-row")).toHaveCount(0);

    // The merged read counts the waiting row, not only bundles: with no
    // questions pending, the sidebar badge is exactly the waiting row.
    await testPage.goto("/");
    const sidebarInbox = testPage.getByTestId("sidebar-needs-you-inbox");
    await expect(sidebarInbox).toBeVisible();
    await expect
      .poll(async () => (await sidebarInbox.textContent()) ?? "", {
        timeout: 30_000,
        message: "sidebar badge should count the waiting row before the Inbox opens",
      })
      .toContain("1");

    // Opening the task from the row navigates to it.
    await testPage.goto("/needs-you-inbox");
    await row.getByTestId("needs-you-waiting-open-task").click();
    await expect(testPage).toHaveURL(new RegExp(`/t/${taskId}$`));
  });

  test("dismisses and restores a waiting row through the shared sidecar", async ({
    testPage,
    apiClient,
    seedData,
  }) => {
    test.setTimeout(90_000);
    const step = await apiClient.createWorkflowStep(seedData.workflowId, "Manual gate", 99);
    const title = "Waiting rows dismiss fixture";
    await apiClient.seedTask(seedData.workspaceId, title, {
      workflow_id: seedData.workflowId,
      workflow_step_id: step.id,
    });

    await testPage.goto("/needs-you-inbox");
    const row = testPage.getByTestId("needs-you-waiting-row").filter({ hasText: title });
    await expect(row).toBeVisible({ timeout: 30_000 });

    await row.getByRole("button", { name: "Row actions" }).click();
    await testPage.getByRole("menuitem", { name: "Dismiss" }).click();

    // The dismiss writes the per-user sidecar and the next read drops the row;
    // the hidden panel discloses exactly one hidden row.
    await expect(row).toHaveCount(0);
    const hiddenPanel = testPage.getByTestId("needs-you-inbox-hidden-panel");
    await expect(hiddenPanel).toBeVisible();
    await expect(hiddenPanel).toContainText("1 row is hidden");

    await hiddenPanel.getByRole("button", { name: "Show hidden" }).click();
    const hiddenRow = hiddenPanel.getByTestId("needs-you-inbox-hidden-row").filter({
      hasText: title,
    });
    await expect(hiddenRow).toBeVisible();

    await hiddenRow.getByRole("button", { name: "Restore" }).click();
    await expect(row).toBeVisible({ timeout: 30_000 });
    await expect(hiddenPanel).toHaveCount(0);
  });

  test("removes the waiting row once the task is no longer waiting", async ({
    testPage,
    apiClient,
    seedData,
  }) => {
    test.setTimeout(90_000);
    const step = await apiClient.createWorkflowStep(seedData.workflowId, "Manual sign-off", 99);
    const title = "Waiting rows resolve fixture";
    const { task_id: taskId } = await apiClient.seedTask(seedData.workspaceId, title, {
      workflow_id: seedData.workflowId,
      workflow_step_id: step.id,
    });

    await testPage.goto("/needs-you-inbox");
    const row = testPage.getByTestId("needs-you-waiting-row").filter({ hasText: title });
    await expect(row).toBeVisible({ timeout: 30_000 });

    // A terminal task is not waiting on anyone: the next read drops the row
    // and the empty state takes over.
    await apiClient.updateTaskState(taskId, "COMPLETED");
    await testPage.goto("/needs-you-inbox");

    await expect(row).toHaveCount(0);
    await expect(testPage.getByTestId("needs-you-inbox-empty")).toBeVisible({
      timeout: 30_000,
    });
  });
});
