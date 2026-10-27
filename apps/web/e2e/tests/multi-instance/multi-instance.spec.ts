import type { Page } from "@playwright/test";
import { test, expect } from "../../fixtures/test-base";
import type { ApiClient } from "../../helpers/api-client";
import { dwell, waitForHttp, watchWs } from "../../helpers/causal-waits";
import { waitForAgentMessage, waitForSessionDone } from "../../helpers/session";
import {
  bootRemoteBackend,
  remoteBackendPortInputs,
  seedRemoteBackendTask,
  type RemoteBackendContext,
  type RemoteBackendSeed,
} from "../../fixtures/remote-backend";
import { KanbanPage } from "../../pages/kanban-page";
import { SessionPage } from "../../pages/session-page";

/**
 * Dual-backend flows for the multi-instance change (openspec
 * add-multi-instance-management, E2E 测试图 1-8). The local backend A runs
 * with KANDEV_FEATURES_MULTI_INSTANCE=true (scoped via backend.useEnv, so
 * every later restart keeps the flag until the scope is released); the
 * remote backend B is booted by fixtures/remote-backend.ts with an isolated
 * home, its own port band, and a version-stamped binary copy so A's health
 * probe reports it supported.
 */

const REMOTE_INSTANCE_NAME = "Remote Studio";
const REMOTE_TASK_TITLE = "Remote-only fixture task";
const LOCAL_TASK_TITLE = "Local-only fixture task";
const REMOTE_TURN_ONE_REPLY = "remote turn reply one";
const REMOTE_TURN_TWO_REPLY = "remote turn reply two";

type InstanceHealth = {
  reachable: boolean;
  version?: string;
  error?: string;
  supported: boolean;
};

type RemoteNotificationRow = {
  id: string;
  instance_id: string;
  event: string;
  occurrence_id: string;
  task_id: string;
  session_id: string;
  title: string;
  body: string;
  created_at: string;
  read: boolean;
  dismissed: boolean;
};

async function instanceHealth(apiClient: ApiClient, instanceId: string): Promise<InstanceHealth> {
  const res = await apiClient.rawRequest("GET", `/api/v1/instances/${instanceId}/health`);
  if (res.status !== 200) throw new Error(`instance health returned ${res.status}`);
  return (await res.json()) as InstanceHealth;
}

async function remoteNotificationRows(apiClient: ApiClient): Promise<RemoteNotificationRow[]> {
  const res = await apiClient.rawRequest("GET", "/api/v1/remote-notifications");
  if (res.status !== 200) throw new Error(`remote notifications returned ${res.status}`);
  return (await res.json()) as RemoteNotificationRow[];
}

// Switching back to the local instance is a full reload. Detect the fresh
// document by marking the current one first and then waiting for a document
// that lost the marker and carries the local boot payload. The payload alone
// is not enough: the SPA assigns it on a remote boot too, once its
// app-state fallback resolves, so the pre-switch remote page already has it.
async function switchToLocalInstance(page: Page): Promise<void> {
  await expandSidebarIfNeeded(page);
  await page.evaluate(() => {
    (window as Window & { __multiInstanceE2EPageMarker?: boolean }).__multiInstanceE2EPageMarker =
      true;
  });
  await switchInstanceViaPicker(
    page,
    "sidebar-instance-item-local",
    /^\/api\/v1\/instances\/local\/select$/,
  );
  await page.waitForFunction(() => {
    const w = window as Window & {
      __multiInstanceE2EPageMarker?: boolean;
      __KANDEV_BOOT_PAYLOAD__?: unknown;
    };
    return !w.__multiInstanceE2EPageMarker && Boolean(w.__KANDEV_BOOT_PAYLOAD__);
  });
}

function waitForRemoteBoot(page: Page, instanceId: string): Promise<void> {
  return page.waitForFunction(
    (expected) =>
      (window as Window & { __KANDEV_INSTANCE_ID?: string }).__KANDEV_INSTANCE_ID === expected,
    instanceId,
  );
}

async function switchInstanceViaPicker(
  page: Page,
  itemId: string,
  expectedSelectPath: RegExp,
): Promise<void> {
  const selectSent = waitForHttp(page, "POST", expectedSelectPath);
  await page.getByTestId("sidebar-instance-trigger").click();
  await page.getByTestId(itemId).click();
  await selectSent;
}

// Task workbenches collapse the app sidebar rail, and the instance picker and
// the notifications module's rows render only in the expanded sidebar, so
// expand the rail before interacting with either surface.
async function expandSidebarIfNeeded(page: Page): Promise<void> {
  const header = page.getByTestId("app-sidebar-header");
  await expect(header).toBeAttached();
  if ((await header.getAttribute("data-sidebar-header-collapsed")) === "true") {
    await page.getByLabel("Expand sidebar").click();
  }
  await expect(header).toHaveAttribute("data-sidebar-header-collapsed", "false");
}

test.describe("multi-instance", () => {
  // Released either by the flag-off test (flow 8 restarts the backend with
  // the flag off) or by afterAll; the callback is one-shot.
  let releaseMultiInstanceFlag: (() => Promise<void>) | undefined;

  test.beforeAll(async ({ backend }) => {
    releaseMultiInstanceFlag = await backend.useEnv({
      KANDEV_FEATURES_MULTI_INSTANCE: "true",
    });
  });

  test.afterAll(async () => {
    await releaseMultiInstanceFlag?.();
  });

  test("multi-instance register, switch, chat, notify, read, and kill flows", async ({
    testPage,
    backend,
    apiClient,
    seedData,
  }) => {
    test.setTimeout(300_000);
    const { portOffset, parallelSlot } = remoteBackendPortInputs(backend.port);
    let remote: RemoteBackendContext | undefined;
    let remoteSeed: RemoteBackendSeed | undefined;

    try {
      remote = await bootRemoteBackend({
        localBaseUrl: backend.baseUrl,
        portOffset,
        parallelSlot,
      });
      remoteSeed = await seedRemoteBackendTask(remote, REMOTE_TASK_TITLE);
      const remoteTaskId = remoteSeed.task.id;
      const remoteSessionId = remoteSeed.task.session_id;
      const remoteClient = remoteSeed.client;
      const localTask = await apiClient.createTask(seedData.workspaceId, LOCAL_TASK_TITLE, {
        workflow_id: seedData.workflowId,
        workflow_step_id: seedData.startStepId,
      });

      // --- Flow 1: register B in A's settings page ---
      const ws = watchWs(testPage);
      const instanceCreated = waitForHttp(testPage, "POST", /^\/api\/v1\/instances$/);
      await testPage.goto("/settings/system/instances");
      await testPage.getByRole("button", { name: "Add instance" }).click();
      await testPage.locator("#instance-name").fill(REMOTE_INSTANCE_NAME);
      await testPage.locator("#instance-base-url").fill(remote.baseUrl);
      const floatingSave = testPage.getByTestId("settings-floating-save");
      const saveButton = floatingSave.getByRole("button", { name: /^Save( changes)?$/i });
      await expect(saveButton).toBeEnabled();
      await saveButton.click();
      const createResponse = await instanceCreated;
      expect(createResponse.status()).toBe(201);
      const created = (await createResponse.json()) as { id: string; name: string };
      await expect(testPage.getByTestId(`instance-row-${created.id}`)).toBeVisible();

      // The health poller runs on the e2e-shared 90s cadence; a
      // just-registered instance is otherwise first probed on the next pass.
      // Restarting A runs the poller's immediate boot pass instead, which
      // also re-arms the per-instance proxy and relay from the registry.
      await backend.restart();
      await expect
        .poll(() => instanceHealth(apiClient, created.id), {
          timeout: 20_000,
          message: "Waiting for the boot-pass health probe to report the remote as supported",
        })
        .toMatchObject({ reachable: true, supported: true });

      // Settings page: health green + version shown.
      await testPage.goto("/settings/system/instances");
      const healthBanner = testPage.getByTestId(`instance-health-${created.id}`);
      await expect(healthBanner).toHaveAttribute("data-state", "ok");
      await expect(healthBanner).toContainText("v0.98.0");

      // The picker appears once a page load sees the registered instance.
      await testPage.goto("/");
      await expect(testPage.getByTestId("sidebar-instance-trigger")).toBeVisible();

      // --- Flow 2: switch to B via the picker; the board shows B's task ---
      // The remote shell omits the boot payload, so the board's data comes
      // from the app-state round trip through the per-instance proxy.
      const remoteAppStateLoaded = waitForHttp(testPage, "GET", /^\/api\/v1\/app-state$/);
      await switchInstanceViaPicker(
        testPage,
        `sidebar-instance-item-${created.id}`,
        /^\/api\/v1\/instances\/[^/]+\/select$/,
      );
      await waitForRemoteBoot(testPage, created.id);
      await remoteAppStateLoaded;
      const kanban = new KanbanPage(testPage);
      await expect(kanban.board).toBeVisible();
      await expect(kanban.taskCard(remoteTaskId)).toBeVisible();
      await expect(kanban.taskCard(localTask.id)).toHaveCount(0);

      // --- Flow 3: chat in B's task; B's state transitions, panel via proxy ---
      await testPage.goto(`/t/${remoteTaskId}`);
      const session = new SessionPage(testPage);
      await session.waitForLoad();
      await session.waitForDirectInput();
      await session.sendMessage(`e2e:message("${REMOTE_TURN_ONE_REPLY}")`);
      await waitForAgentMessage(remoteClient, remoteSessionId, REMOTE_TURN_ONE_REPLY, 30_000);
      await waitForSessionDone(
        remoteClient,
        remoteTaskId,
        remoteSessionId,
        "Waiting for the remote chat turn to finish",
        30_000,
      );
      await expect(
        session.activeChat().getByText(REMOTE_TURN_ONE_REPLY, { exact: true }),
      ).toBeVisible();

      // --- Flow 4: switch back to A; a remote turn completion notifies ---
      // The module's rows come from the local remote-notifications REST load,
      // and live frames only arrive on a subscribed socket (no replay), so
      // both are awaited before the badge baseline is read.
      const notificationsLoaded = waitForHttp(testPage, "GET", /^\/api\/v1\/remote-notifications$/);
      const subscribed = ws.waitForResponse("user.subscribe");
      await switchToLocalInstance(testPage);
      await notificationsLoaded;
      await subscribed;

      const totalBadge = testPage.getByTestId("sidebar-notifications-unread-total");
      const instanceBadge = testPage.getByTestId(`sidebar-instance-unread-${created.id}`);
      const pickerTrigger = testPage.getByTestId("sidebar-instance-trigger");
      await expect(totalBadge).toBeVisible();
      // The module badge renders in the sidebar; the picker's per-instance
      // badge renders inside its dropdown, so open it to read the baseline.
      await pickerTrigger.click();
      await expect(instanceBadge).toBeVisible();
      const beforeTotal = Number(await totalBadge.textContent());
      const beforeInstance = Number(await instanceBadge.textContent());
      await testPage.keyboard.press("Escape");

      // Arm the frame wait before the action that publishes it, then drive
      // the turn directly against B (api-client, not through the proxy).
      const remoteFrame = ws.waitForEvent("remote_notification", {
        where: (payload) => payload.task_id === remoteTaskId,
      });
      await remoteClient.addUserMessage(
        remoteTaskId,
        remoteSessionId,
        `e2e:message("${REMOTE_TURN_TWO_REPLY}")`,
      );
      await waitForSessionDone(
        remoteClient,
        remoteTaskId,
        remoteSessionId,
        "Waiting for the notification-triggering remote turn to finish",
        30_000,
      );
      await remoteFrame;

      await expect(testPage.getByTestId(`sidebar-notification-group-${created.id}`)).toBeVisible();
      await expect(totalBadge).toHaveText(String(beforeTotal + 1));
      await pickerTrigger.click();
      await expect(instanceBadge).toHaveText(String(beforeInstance + 1));
      await testPage.keyboard.press("Escape");
      await dwell(
        testPage,
        300,
        "negative-assertion",
        "asserting the relayed remote notification never raises a toast",
      );
      await expect(testPage.getByTestId("toast-message")).toHaveCount(0);

      // --- Flow 5: click the entry; switch to B on the task route ---
      const rows = await remoteNotificationRows(apiClient);
      const entry = rows.filter((row) => row.task_id === remoteTaskId).at(-1);
      if (!entry) throw new Error("no relayed notification entry for the remote task");
      await testPage.getByTestId(`sidebar-notification-row-${entry.id}`).click();
      await testPage.waitForURL(new RegExp(`/t/${remoteTaskId}\\?kandev_instance=${created.id}$`));
      await waitForRemoteBoot(testPage, created.id);
      const remoteSession = new SessionPage(testPage);
      await remoteSession.waitForLoad();
      await expect(
        remoteSession.activeChat().getByText(REMOTE_TURN_TWO_REPLY, { exact: true }),
      ).toBeVisible();

      // --- Flow 6: mark all read; badge clears and survives reload ---
      const readAllSent = waitForHttp(
        testPage,
        "POST",
        /^\/api\/v1\/remote-notifications\/read-all$/,
      );
      await testPage.getByTestId("sidebar-notifications-read-all").click();
      await readAllSent;
      await expect(totalBadge).toHaveCount(0);
      await expect(instanceBadge).toHaveCount(0);
      // The reload's module REST read is the cause the cleared badges hang
      // off; awaiting it keeps "stays cleared" from passing on a slow load.
      const notificationsReloaded = waitForHttp(
        testPage,
        "GET",
        /^\/api\/v1\/remote-notifications$/,
      );
      await testPage.reload();
      await remoteSession.waitForLoad();
      await expandSidebarIfNeeded(testPage);
      await notificationsReloaded;
      await expect(testPage.getByTestId("sidebar-notifications-unread-total")).toHaveCount(0);
      // Open the picker so its per-instance badge absence is asserted inside
      // the rendered dropdown, not behind a closed one.
      await testPage.getByTestId("sidebar-instance-trigger").click();
      await expect(testPage.getByTestId(`sidebar-instance-unread-${created.id}`)).toHaveCount(0);
      await testPage.keyboard.press("Escape");
      await expect(testPage.getByTestId(`sidebar-notification-row-${entry.id}`)).toBeVisible();

      // --- Flow 7: kill B; health degrades, A unaffected, structured error ---
      const proxyPort = await testPage.evaluate(
        () => (window as Window & { __KANDEV_API_PORT?: string }).__KANDEV_API_PORT,
      );
      if (!proxyPort) throw new Error("remote boot did not expose __KANDEV_API_PORT");
      await remote.kill();

      const proxyError = await testPage.request.get(
        `http://localhost:${proxyPort}/api/v1/workspaces`,
      );
      expect(proxyError.status()).toBe(502);
      expect(await proxyError.json()).toMatchObject({
        error: "remote_unreachable",
        instance: REMOTE_INSTANCE_NAME,
      });

      // A's own task operations keep working while the remote is dead.
      const afterTask = await apiClient.createTask(
        seedData.workspaceId,
        "Local task after remote death",
        { workflow_id: seedData.workflowId, workflow_step_id: seedData.startStepId },
      );

      // Same restart as flow 1: run the health poller's boot pass now
      // instead of waiting out the 90s cadence.
      await backend.restart();
      await expect
        .poll(() => instanceHealth(apiClient, created.id), {
          timeout: 20_000,
          message: "Waiting for the boot-pass health probe to report the dead remote",
        })
        .toMatchObject({ reachable: false, supported: false });

      // The proxy rebinds its persisted port at boot and still surfaces the
      // structured error for the dead remote.
      const proxyErrorAfterRestart = await testPage.request.get(
        `http://localhost:${proxyPort}/api/v1/workspaces`,
      );
      expect(proxyErrorAfterRestart.status()).toBe(502);
      expect(await proxyErrorAfterRestart.json()).toMatchObject({
        error: "remote_unreachable",
        instance: REMOTE_INSTANCE_NAME,
      });

      await switchToLocalInstance(testPage);
      await expect(kanban.taskCard(afterTask.id)).toBeVisible();
    } finally {
      await remote?.dispose();
    }
  });

  test("multi-instance flag off keeps the federation surface inert", async ({
    testPage,
    apiClient,
  }) => {
    test.setTimeout(120_000);

    // Register a row while the flag is still on, so the flag-off assertions
    // below prove the surface stays inert with registry rows persisted.
    const createResponse = await apiClient.rawRequest("POST", "/api/v1/instances", {
      name: REMOTE_INSTANCE_NAME,
      base_url: "http://localhost:65530",
      enabled: true,
    });
    expect(createResponse.status).toBe(201);
    const created = (await createResponse.json()) as { id: string };

    // Flow 8: a backend booted with the flag off exposes none of the
    // federation surface, and the shell serves the local boot even when an
    // instance parameter is present. Releasing the scoped env restarts the
    // backend without KANDEV_FEATURES_MULTI_INSTANCE.
    await releaseMultiInstanceFlag?.();

    const listResponse = await apiClient.rawRequest("GET", "/api/v1/instances");
    expect(listResponse.status).toBe(404);

    const shellResponse = await testPage.request.get(
      `/?kandev_instance=${encodeURIComponent(created.id)}`,
    );
    expect(shellResponse.status()).toBe(200);
    const shellHtml = await shellResponse.text();
    expect(shellHtml).toContain("__KANDEV_BOOT_PAYLOAD__");
    expect(shellHtml).not.toContain("__KANDEV_API_PORT");
    expect(shellHtml).not.toContain("__KANDEV_INSTANCE_ID");

    await testPage.goto("/");
    // The sidebar itself renders (registry row persisted, flag off), and the
    // picker and notifications module stay hidden.
    await expect(testPage.getByTestId("app-sidebar-scroll")).toBeVisible();
    await expect(testPage.getByTestId("sidebar-instance-trigger")).toHaveCount(0);
    await expect(testPage.getByTestId("sidebar-notifications-unread-total")).toHaveCount(0);
  });
});
