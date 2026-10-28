import type { Page } from "@playwright/test";
import { test, expect } from "../../fixtures/test-base";
import type { ApiClient } from "../../helpers/api-client";
import { waitForAgentMessage, waitForSessionState } from "../../helpers/session";
import { waitForHttp } from "../../helpers/causal-waits";
import {
  bootRemoteBackend,
  remoteBackendPortInputs,
  seedRemoteBackendTask,
  type RemoteBackendContext,
  type RemoteBackendSeed,
} from "../../fixtures/remote-backend";

/**
 * Dual-backend flows for the cross-instance inbox change (openspec
 * surface-remote-inbox-bundles, E2E 测试图 1-7). The local backend A runs with
 * KANDEV_FEATURES_MULTI_INSTANCE=true (needsYouInbox is already on in the e2e
 * profile); the remote backend B is seeded with a blocking clarification task
 * so A's Needs-you Inbox composes its rows, answers them in place through the
 * forward routes, counts them in the sidebar badge, jumps to the remote task,
 * degrades when B dies, forwards snoozes to B's own sidecar, and reports the
 * inbox-disabled reason when B turns its needs-you inbox off.
 */

const REMOTE_INSTANCE_NAME = "Remote Inbox Studio";
const ANSWER_TASK_TITLE = "Remote clarification to answer in place";
const SNOOZE_TASK_TITLE = "Remote clarification to snooze from here";
const RECOVER_TASK_TITLE = "Remote clarification that survives a restart";

// Switching between instances is a full reload; detect the fresh document by
// marking the current one first (mirrors multi-instance.spec.ts's helper).
async function switchBackToLocal(page: Page): Promise<void> {
  await page.evaluate(() => {
    (window as Window & { __remoteInboxE2EPageMarker?: boolean }).__remoteInboxE2EPageMarker = true;
  });
  await page.getByTestId("sidebar-instance-trigger").click();
  await page.getByTestId("sidebar-instance-item-local").click();
  await page.waitForFunction(() => {
    const w = window as Window & {
      __remoteInboxE2EPageMarker?: boolean;
      __KANDEV_BOOT_PAYLOAD__?: unknown;
    };
    return !w.__remoteInboxE2EPageMarker && Boolean(w.__KANDEV_BOOT_PAYLOAD__);
  });
}

async function waitForRemoteBoot(page: Page, instanceId: string): Promise<void> {
  await page.waitForFunction(
    (expected) =>
      (window as Window & { __KANDEV_INSTANCE_ID?: string }).__KANDEV_INSTANCE_ID === expected,
    instanceId,
  );
}

async function registerInstance(apiClient: ApiClient, baseUrl: string): Promise<string> {
  const res = await apiClient.rawRequest("POST", "/api/v1/instances", {
    name: REMOTE_INSTANCE_NAME,
    base_url: baseUrl,
    enabled: true,
  });
  expect(res.status).toBe(201);
  const created = (await res.json()) as { id: string };
  return created.id;
}

// One more blocking clarification task on the remote, for flows that need an
// unanswered row after an earlier one was answered.
async function seedRemoteClarification(
  seed: RemoteBackendSeed,
  title: string,
): Promise<{ taskId: string; sessionId: string }> {
  const task = await seed.client.createTaskWithAgent(seed.workspaceId, title, seed.agentProfileId, {
    description: "/e2e:clarification",
    workflow_id: seed.workflowId,
    workflow_step_id: seed.startStepId,
    repository_ids: [seed.repositoryId],
  });
  if (!task.session_id) throw new Error(`remote clarification seed returned no session: ${title}`);
  await waitForSessionState(seed.client, {
    taskId: task.id,
    sessionId: task.session_id,
    expectedState: "WAITING_FOR_INPUT",
    message: `remote clarification task should block: ${title}`,
    timeout: 60_000,
  });
  return { taskId: task.id, sessionId: task.session_id };
}

test.describe("remote needs-you inbox", () => {
  let releaseMultiInstanceFlag: (() => Promise<void>) | undefined;

  test.beforeAll(async ({ backend }) => {
    releaseMultiInstanceFlag = await backend.useEnv({
      KANDEV_FEATURES_MULTI_INSTANCE: "true",
    });
  });

  test.afterAll(async () => {
    await releaseMultiInstanceFlag?.();
  });

  test("remote rows list, jump, answer, snooze, and degrade with recovery (图1-6)", async ({
    testPage,
    backend,
    apiClient,
    seedData,
  }) => {
    test.setTimeout(480_000);
    const { portOffset, parallelSlot } = remoteBackendPortInputs(backend.port);
    let remote: RemoteBackendContext | undefined;

    try {
      remote = await bootRemoteBackend({
        localBaseUrl: backend.baseUrl,
        portOffset,
        parallelSlot,
      });
      const seed = await seedRemoteBackendTask(remote, "Remote inbox fixture task");
      const answerTask = await seedRemoteClarification(seed, ANSWER_TASK_TITLE);
      const instanceId = await registerInstance(apiClient, remote.baseUrl);

      // --- 图1: the local Inbox lists the remote bundle in its own section ---
      await testPage.goto("/needs-you-inbox");
      const section = testPage.getByTestId("remote-inbox-section");
      await expect(section).toBeVisible({ timeout: 30_000 });
      await expect(section.getByTestId("remote-inbox-section-title")).toHaveText(
        REMOTE_INSTANCE_NAME,
      );
      const remoteRow = section.getByTestId("needs-you-inbox-row").filter({
        hasText: ANSWER_TASK_TITLE,
      });
      await expect(remoteRow).toBeVisible();
      // The local page keeps its own empty state: no local bundle was seeded.
      await expect(testPage.getByTestId("needs-you-inbox-empty")).toBeVisible();

      // --- 图3: a fresh load of a normal route carries the remote row count ---
      await testPage.goto("/");
      const sidebarInbox = testPage.getByTestId("sidebar-needs-you-inbox");
      await expect(sidebarInbox).toBeVisible();
      await expect
        .poll(async () => (await sidebarInbox.textContent()) ?? "", {
          timeout: 30_000,
          message: "sidebar badge should include the landed remote row before the Inbox opens",
        })
        .toContain("1");

      // --- 图4: the remote row's open-task control switches to the task ---
      await testPage.goto("/needs-you-inbox");
      await expect(remoteRow).toBeVisible({ timeout: 30_000 });
      await remoteRow.getByTestId("needs-you-inbox-open-task").click();
      // The row's task link carries its session pin, so the instance switch
      // lands on /t/:id?sessionId=...&kandev_instance=:id.
      await testPage.waitForURL(
        new RegExp(`/t/${answerTask.taskId}\\?sessionId=.+&kandev_instance=${instanceId}$`),
      );
      await waitForRemoteBoot(testPage, instanceId);
      await expect(
        testPage.getByTestId("needs-you-inbox-row").filter({ hasText: ANSWER_TASK_TITLE }),
      ).toHaveCount(0);
      await switchBackToLocal(testPage);

      // --- 图2: answer in place; the remote session resumes and the row goes ---
      await testPage.goto("/needs-you-inbox");
      await expect(remoteRow).toBeVisible({ timeout: 30_000 });
      await remoteRow.getByTestId("needs-you-inbox-row-toggle").click();
      const overlay = remoteRow.getByTestId("clarification-overlay");
      await expect(overlay).toBeVisible({ timeout: 15_000 });
      await overlay.getByTestId("clarification-option").filter({ hasText: "PostgreSQL" }).click();
      await expect(remoteRow).toHaveCount(0, { timeout: 15_000 });
      await waitForAgentMessage(seed.client, answerTask.sessionId, "You answered:", 30_000);
      // The badge falls back with the row.
      await expect
        .poll(async () => (await sidebarInbox.textContent()) ?? "", { timeout: 30_000 })
        .not.toContain("1");

      // --- 图6: snooze lands on the remote sidecar, never a local table ---
      const snoozeTask = await seedRemoteClarification(seed, SNOOZE_TASK_TITLE);
      await testPage.goto("/needs-you-inbox");
      const snoozeRow = section.getByTestId("needs-you-inbox-row").filter({
        hasText: SNOOZE_TASK_TITLE,
      });
      await expect(snoozeRow).toBeVisible({ timeout: 30_000 });
      await snoozeRow.getByRole("button", { name: "Row actions" }).click();
      await testPage.getByRole("menuitem", { name: "Snooze" }).click();
      await testPage.getByRole("menuitem", { name: "1 hour" }).click();
      await expect(snoozeRow).toHaveCount(0, { timeout: 15_000 });

      const remoteHidden = await seed.client.rawRequest(
        "GET",
        `/api/v1/clarification-inbox/hidden?workspace_id=${seed.workspaceId}`,
      );
      expect(remoteHidden.status).toBe(200);
      const remoteHiddenBody = (await remoteHidden.json()) as {
        bundles: Array<{ pending_id: string; task_id: string }>;
      };
      expect(
        remoteHiddenBody.bundles.some((row) => row.task_id === snoozeTask.taskId),
        "the snoozed remote row must be hidden on the remote's own sidecar",
      ).toBe(true);

      const localHidden = await apiClient.rawRequest(
        "GET",
        `/api/v1/clarification-inbox/hidden?workspace_id=${seedData.workspaceId}`,
      );
      expect(localHidden.status).toBe(200);
      const localHiddenBody = (await localHidden.json()) as {
        bundles: Array<{ pending_id: string; task_id: string }>;
      };
      expect(
        localHiddenBody.bundles.filter((row) => row.task_id === snoozeTask.taskId),
        "no local sidecar row may exist for a remote bundle",
      ).toHaveLength(0);

      const hiddenPanel = section.getByTestId("needs-you-inbox-hidden-panel");
      await hiddenPanel.getByRole("button", { name: "Show hidden" }).click();
      const hiddenRow = hiddenPanel.getByTestId("needs-you-inbox-hidden-row");
      await expect(hiddenRow).toHaveCount(1);
      await hiddenRow.getByRole("button", { name: "Restore" }).click();
      await expect(snoozeRow).toBeVisible({ timeout: 30_000 });
      await expect
        .poll(async () => {
          const response = await seed.client.rawRequest(
            "GET",
            `/api/v1/clarification-inbox/hidden?workspace_id=${seed.workspaceId}`,
          );
          const body = (await response.json()) as { bundles: Array<{ task_id: string }> };
          return body.bundles.some((row) => row.task_id === snoozeTask.taskId);
        })
        .toBe(false);

      // --- 图5: kill B; the section degrades, recovers after restart+Retry ---
      await seedRemoteClarification(seed, RECOVER_TASK_TITLE);
      await testPage.goto("/needs-you-inbox");
      await expect(
        section.getByTestId("needs-you-inbox-row").filter({ hasText: RECOVER_TASK_TITLE }),
      ).toBeVisible({ timeout: 30_000 });
      await remote.kill();
      await testPage.goto("/needs-you-inbox");
      const degraded = section.getByTestId("remote-inbox-degraded");
      await expect(degraded).toBeVisible({ timeout: 30_000 });
      await expect(degraded).toContainText(REMOTE_INSTANCE_NAME);
      // The local page is untouched by the remote's death.
      await expect(testPage.getByTestId("needs-you-inbox-empty")).toBeVisible();

      await remote.restart();
      await section.getByTestId("remote-inbox-retry").click();
      await expect(degraded).toHaveCount(0, { timeout: 30_000 });
      await expect(
        section.getByTestId("needs-you-inbox-row").filter({ hasText: RECOVER_TASK_TITLE }),
      ).toBeVisible({ timeout: 30_000 });
    } finally {
      await remote?.dispose();
    }
  });

  test("remote answer remains retryable after disconnect and converges after a competing answer", async ({
    testPage,
    backend,
    apiClient,
  }) => {
    test.setTimeout(240_000);
    const { portOffset, parallelSlot } = remoteBackendPortInputs(backend.port);
    let remote: RemoteBackendContext | undefined;
    try {
      remote = await bootRemoteBackend({ localBaseUrl: backend.baseUrl, portOffset, parallelSlot });
      const seed = await seedRemoteBackendTask(remote, "Remote answer recovery fixture");
      await seedRemoteClarification(seed, "Remote answer to retry");
      const instanceId = await registerInstance(apiClient, remote.baseUrl);
      await testPage.goto("/needs-you-inbox");
      const section = testPage.getByTestId("remote-inbox-section");
      const retryRow = section.getByTestId("needs-you-inbox-row").filter({
        hasText: "Remote answer to retry",
      });
      await expect(retryRow).toBeVisible({ timeout: 30_000 });
      await retryRow.getByTestId("needs-you-inbox-row-toggle").click();
      const overlay = retryRow.getByTestId("clarification-overlay");
      const option = overlay.getByTestId("clarification-option").filter({ hasText: "PostgreSQL" });
      await remote.kill();
      const failedResponse = waitForHttp(testPage, "POST", /\/clarification\/[^/]+\/respond$/);
      await option.click();
      expect((await failedResponse).status()).toBe(502);
      await expect(overlay.getByTestId("clarification-submit-error")).toBeVisible();
      await expect(option).toHaveAttribute("data-selected", "true");
      await expect(overlay.getByTestId("clarification-retry")).toBeEnabled();
      await remote.restart();

      const raceTask = await seedRemoteClarification(seed, "Remote competing answer");
      await testPage.goto("/needs-you-inbox");
      const raceRow = section.getByTestId("needs-you-inbox-row").filter({
        hasText: "Remote competing answer",
      });
      await expect(raceRow).toBeVisible({ timeout: 30_000 });
      await raceRow.getByTestId("needs-you-inbox-row-toggle").click();
      const routePattern = `**/api/v1/instances/${instanceId}/clarification/*/respond`;
      await testPage.route(
        routePattern,
        async (route) => {
          const pendingId = new URL(route.request().url()).pathname.split("/").at(-2);
          const winner = await seed.client.rawRequest(
            "POST",
            `/api/v1/clarification/${pendingId}/respond`,
            route.request().postDataJSON(),
          );
          expect(winner.status).toBe(200);
          await route.continue();
        },
        { times: 1 },
      );
      const losingResponse = waitForHttp(testPage, "POST", /\/clarification\/[^/]+\/respond$/);
      await raceRow.getByTestId("clarification-option").filter({ hasText: "PostgreSQL" }).click();
      const response = await losingResponse;
      expect([200, 409]).toContain(response.status());
      if (response.status() === 200) expect((await response.json()).claimed).toBe(false);
      await expect(raceRow).toHaveCount(0, { timeout: 30_000 });
      await expect(testPage.getByTestId("clarification-submit-error")).toHaveCount(0);
      await waitForAgentMessage(seed.client, raceTask.sessionId, "You answered:", 30_000);
    } finally {
      await remote?.dispose();
    }
  });

  test("remote with its needs-you inbox off degrades with the named reason (图7)", async ({
    testPage,
    backend,
    apiClient,
  }) => {
    test.setTimeout(180_000);
    const { portOffset, parallelSlot } = remoteBackendPortInputs(backend.port);
    let remote: RemoteBackendContext | undefined;

    try {
      remote = await bootRemoteBackend({
        localBaseUrl: backend.baseUrl,
        portOffset,
        parallelSlot,
        envOverrides: { KANDEV_FEATURES_NEEDS_YOU_INBOX: "false" },
      });
      await seedRemoteBackendTask(remote, "Remote inbox-disabled fixture task");
      await registerInstance(apiClient, remote.baseUrl);

      await testPage.goto("/needs-you-inbox");
      const degraded = testPage.getByTestId("remote-inbox-degraded");
      await expect(degraded).toBeVisible({ timeout: 30_000 });
      await expect(degraded).toContainText(REMOTE_INSTANCE_NAME);
      await expect(degraded).toContainText("does not expose its needs-you inbox");
      await expect(testPage.getByTestId("remote-inbox-retry")).toBeVisible();
      // No answer form can render in the degraded state.
      await expect(testPage.getByTestId("clarification-overlay")).toHaveCount(0);
    } finally {
      await remote?.dispose();
    }
  });
});
