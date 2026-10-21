import { expect } from "@playwright/test";
import { dwell } from "../../helpers/causal-waits";
import { test } from "../../fixtures/test-base";
import { SessionPage } from "../../pages/session-page";
import {
  creationBaseMotherCommit,
  creationBaseOwnCommit,
  restoreWorktreeCreationBaseRepository,
  seedWorktreeCreationBaseTask,
} from "./worktree-creation-base-comparison-helpers";

test.describe("Worktree creation base comparison", () => {
  test.describe.configure({ timeout: 180_000 });

  const createdWorktrees: Array<{
    taskId: string;
    sessionId: string;
    worktreeID: string;
    worktreePath: string;
  }> = [];
  test.afterEach(async ({ seedData, backend, apiClient }) => {
    for (const created of createdWorktrees.splice(0)) {
      if (created.sessionId) {
        await apiClient
          .stopSession({
            session_id: created.sessionId,
            reason: "worktree creation base E2E cleanup",
            force: true,
          })
          .catch(() => {
            // The session may already have stopped; cleanup continues.
          });
        // The stop is asynchronous: wait for the session to leave its live
        // states so task deletion does not defer the worktree cleanup behind
        // a session the executor still considers running.
        const stopDeadline = Date.now() + 20_000;
        for (;;) {
          const sessions = await apiClient.listTaskSessions(created.taskId).catch(() => null);
          const state = sessions?.sessions?.find((s) => s.id === created.sessionId)?.state;
          if (
            state === undefined ||
            state === "CANCELLED" ||
            state === "COMPLETED" ||
            state === "ERROR" ||
            Date.now() > stopDeadline
          ) {
            break;
          }
          await dwell(250, "poll-interval", "session stop settle before task deletion");
        }
      }
      await apiClient.deleteTask(created.taskId).catch(() => {
        // The fixture reset also deletes tasks; a failure here is non-fatal.
      });
      // Task deletion leaves the physical worktree row to an asynchronous
      // cleanup worker. That worker does not settle between tests, and the
      // worker fixture's next reset fails its pre-delete inspection when the
      // directory has already been removed underneath the surviving row.
      // Remove this scenario's own row deterministically so the reset has
      // nothing left to inspect, and take the directory with it.
      const { execFileSync } = await import("node:child_process");
      const fs = await import("node:fs");
      try {
        execFileSync("sqlite3", [
          backend.tmpDir + "/kandev.db",
          "delete from task_environment_repos where worktree_id = '" + created.worktreeID + "'",
        ]);
      } catch {
        // Database gone: the worker is tearing down anyway.
      }
      try {
        fs.rmSync(created.worktreePath, { recursive: true, force: true });
      } catch {
        // The directory may already be gone.
      }
    }
    restoreWorktreeCreationBaseRepository(seedData, backend);
  });

  test("shows only this worktree's own work against the recorded creation base", async ({
    testPage,
    apiClient,
    seedData,
    backend,
  }) => {
    const { task, sessionId, worktreePath, worktreeID } = await seedWorktreeCreationBaseTask(
      apiClient,
      seedData,
      backend,
    );
    createdWorktrees.push({ taskId: task.id, sessionId, worktreeID, worktreePath });

    await testPage.goto(`/t/${task.id}`);
    const session = new SessionPage(testPage);
    await session.waitForLoad();
    await session.waitForChatIdle({ timeout: 45_000 });
    await session.clickTab("Changes");
    await expect(session.changes).toBeVisible({ timeout: 15_000 });

    // Commits: exactly the one commit made in this worktree.
    await session.expandCommitsSection();
    const commitRows = session.commitsSection().locator('[data-testid^="commit-row-"]');
    await expect(commitRows).toHaveCount(1);
    await expect(session.commitsSection()).toContainText(creationBaseOwnCommit);
    await expect(session.commitsSection()).not.toContainText(creationBaseMotherCommit);

    // The commit row's stat reflects the narrowed range: only the one file
    // this worktree itself committed.
    const ownRow = session.commitsSection().locator('[data-testid^="commit-row-"]').first();
    await expect(ownRow).toContainText("+1 -0");

    // Ahead is counted against the mother branch (1), not origin/main (2).
    await expect(session.commitsSection().getByTestId("commits-repo-push")).toContainText("1");

    // The branch details card names the recorded mother branch.
    const branchDetails = testPage.getByRole("button", {
      name: "Show branch and Git credential details",
    });
    await branchDetails.hover();
    await expect(testPage.getByTestId("changes-creation-base")).toContainText("main");
  });

  test("a checkout-branch task without a creation base keeps the origin-first default", async ({
    testPage,
    apiClient,
    seedData,
    backend,
  }) => {
    const { task, sessionId, worktreePath, worktreeID } = await seedWorktreeCreationBaseTask(
      apiClient,
      seedData,
      backend,
      { legacyCheckoutBranch: "feature/creation-base-legacy" },
    );
    createdWorktrees.push({ taskId: task.id, sessionId, worktreeID, worktreePath });

    await testPage.goto(`/t/${task.id}`);
    const session = new SessionPage(testPage);
    await session.waitForLoad();
    await session.waitForChatIdle({ timeout: 45_000 });
    await session.clickTab("Changes");
    await expect(session.changes).toBeVisible({ timeout: 15_000 });

    // No creation base was recorded: the origin-first default mixes in the
    // mother branch's own unpushed commits (mother + own).
    await session.expandCommitsSection();
    const commitRows = session.commitsSection().locator('[data-testid^="commit-row-"]');
    await expect(commitRows).toHaveCount(2);
    await expect(session.commitsSection()).toContainText(creationBaseMotherCommit);
    await expect(session.commitsSection()).toContainText(creationBaseOwnCommit);

    const branchDetails = testPage.getByRole("button", {
      name: "Show branch and Git credential details",
    });
    await branchDetails.hover();
    await expect(testPage.getByTestId("changes-creation-base")).toHaveCount(0);
  });

  test("a manual picker selection overrides the creation base", async ({
    testPage,
    apiClient,
    seedData,
    backend,
  }) => {
    const { task, sessionId, worktreePath, worktreeID } = await seedWorktreeCreationBaseTask(
      apiClient,
      seedData,
      backend,
    );
    createdWorktrees.push({ taskId: task.id, sessionId, worktreeID, worktreePath });

    await testPage.goto(`/t/${task.id}`);
    const session = new SessionPage(testPage);
    await session.waitForLoad();
    await session.waitForChatIdle({ timeout: 45_000 });
    await session.clickTab("Changes");
    await expect(session.changes).toBeVisible({ timeout: 15_000 });
    await session.expandCommitsSection();
    await expect(session.commitsSection().locator('[data-testid^="commit-row-"]')).toHaveCount(1);

    // Manually select a different comparison branch through the picker. The
    // row becomes user-selected, the creation base stops applying, and the
    // panel falls back to the ordinary origin-first semantics. The picker
    // lives inside the branch details hover card, so open it first.
    const branchDetails = testPage.getByRole("button", {
      name: "Show branch and Git credential details",
    });
    await branchDetails.hover();
    const pickerTrigger = testPage.getByTestId("base-branch-picker-trigger");
    await expect(pickerTrigger).toBeVisible({ timeout: 15_000 });
    await pickerTrigger.click();
    const option = testPage.getByRole("option", { name: "fallback-base", exact: true });
    await expect(option).toBeVisible({ timeout: 15_000 });
    await option.click();

    await expect
      .poll(async () => session.commitsSection().locator('[data-testid^="commit-row-"]').count(), {
        timeout: 30_000,
        message: "the manual selection falls back to the origin-first range",
      })
      .toBe(2);
    await expect(session.commitsSection()).toContainText(creationBaseMotherCommit);

    await branchDetails.hover();
    await expect(testPage.getByTestId("changes-creation-base")).toHaveCount(0, {
      timeout: 30_000,
    });
  });
});
