import type { Page } from "@playwright/test";
import { test, expect } from "../../fixtures/test-base";
import type { ApiClient } from "../../helpers/api-client";
import { dwell, waitForHttp, watchWs } from "../../helpers/causal-waits";
import { getCapturedNotifications } from "../../helpers/notifications-capture";
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

async function instanceHealth(apiClient: ApiClient, instanceId: string): Promise<InstanceHealth> {
  const res = await apiClient.rawRequest("GET", `/api/v1/instances/${instanceId}/health`);
  if (res.status !== 200) throw new Error(`instance health returned ${res.status}`);
  return (await res.json()) as InstanceHealth;
}

type NotificationProvider = { id: string; type: string; events: string[] };

/** Subscribes the local backend's default local provider to one more event
 *  kind, the same rule a locally emitted event follows. */
async function subscribeLocalProviderEvent(apiClient: ApiClient, event: string): Promise<void> {
  const listResponse = await apiClient.rawRequest("GET", "/api/v1/notification-providers");
  if (listResponse.status !== 200) {
    throw new Error(`notification providers returned ${listResponse.status}`);
  }
  const body = (await listResponse.json()) as { providers: NotificationProvider[] };
  const provider = body.providers.find((candidate) => candidate.type === "local");
  if (!provider) throw new Error("no local notification provider provisioned");
  if (provider.events.includes(event)) return;
  const updateResponse = await apiClient.rawRequest(
    "PATCH",
    `/api/v1/notification-providers/${provider.id}`,
    { events: [...provider.events, event] },
  );
  if (updateResponse.status !== 200) {
    throw new Error(`provider subscription update returned ${updateResponse.status}`);
  }
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

// Task workbenches collapse the app sidebar rail, and the instance picker
// renders only in the expanded sidebar, so expand the rail before
// interacting with that surface.
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

  test("multi-instance register, switch, chat, relayed notification, and kill flows", async ({
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
      // B's shell overwrites __KANDEV_API_PORT with the per-instance proxy
      // port; capture it now, because the page fixture's init script restores
      // the local backend port on every document, including the local one the
      // panel returns to for the relayed-notification flow.
      const remoteProxyPort = await testPage.evaluate(
        () => (window as Window & { __KANDEV_API_PORT?: string }).__KANDEV_API_PORT,
      );
      if (!remoteProxyPort) throw new Error("remote boot did not expose __KANDEV_API_PORT");
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
      // Relayed occurrences now flow through the shared notification
      // pipeline: a subscribed event kind notifies exactly like a local
      // event of that kind, with the origin instance carried on the frame.
      const subscribed = ws.waitForResponse("user.subscribe");
      await switchToLocalInstance(testPage);
      await subscribed;
      // Turn-finished is quiet by default, so opt the local user in before
      // the remote turn that must notify.
      await subscribeLocalProviderEvent(apiClient, "session.turn_finished");

      // The module's REST surface is gone with the module itself.
      const remoteNotifications = await apiClient.rawRequest("GET", "/api/v1/remote-notifications");
      expect(remoteNotifications.status).toBe(404);

      // Arm the frame wait before the action that publishes it, then drive
      // the turn directly against B (api-client, not through the proxy).
      const relayedFrame = ws.waitForEvent("session.turn_finished", {
        where: (payload) => payload.origin_instance_id === created.id,
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
      const frame = await relayedFrame;
      expect(frame.payload.origin_instance_name).toBe(REMOTE_INSTANCE_NAME);
      expect(frame.payload.task_id).toBe(remoteTaskId);

      // The unified pipeline notifies like a local event of the kind: the
      // browser notification carries the origin instance as its label.
      await expect
        .poll(
          async () =>
            (await getCapturedNotifications(testPage)).some(
              (notification) =>
                notification.title === `[${REMOTE_INSTANCE_NAME}] Agent turn finished`,
            ),
          { message: "Waiting for the relayed notification to reach the browser surface" },
        )
        .toBe(true);

      // The remote-only sidebar module no longer exists: no groups, no
      // unread badges, on the desktop sidebar or the phone navigation.
      await expect(testPage.getByTestId("sidebar-notifications-unread-total")).toHaveCount(0);
      await expect(testPage.getByTestId(`sidebar-notification-group-${created.id}`)).toHaveCount(0);
      await dwell(
        testPage,
        300,
        "negative-assertion",
        "asserting the relayed remote notification never raises an in-app toast",
      );
      await expect(testPage.getByTestId("toast-message")).toHaveCount(0);

      // --- Flow 5: kill B; health degrades, A unaffected, structured error ---
      const proxyPort = remoteProxyPort;
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

      // The panel already returned to the local instance for the relayed
      // notification, so the picker's local row is the active one and a
      // picker switch would be a no-op: a plain local navigation re-reads the
      // board instead.
      await testPage.goto("/");
      await expect(kanban.taskCard(afterTask.id)).toBeVisible();
    } finally {
      await remote?.dispose();
    }
  });

  test("remote waiting rows appear, resolve, degrade, and notify without affecting other sources", async ({
    testPage,
    backend,
    apiClient,
  }) => {
    test.setTimeout(420_000);
    const { portOffset, parallelSlot } = remoteBackendPortInputs(backend.port);
    let remote: RemoteBackendContext | undefined;

    try {
      remote = await bootRemoteBackend({
        localBaseUrl: backend.baseUrl,
        portOffset,
        parallelSlot,
      });
      const seed = await seedRemoteBackendTask(remote, "Remote waiting rows fixture task");
      // The fixture task's completed turn parks it in the template's Review
      // step, which is itself a manual step; complete it so the remote's
      // waiting page holds exactly the rows this test seeds.
      await seed.client.updateTaskState(seed.task.id, "COMPLETED");

      // A manual step on the remote's own workflow: a sessionless task parked
      // there is the remote's manual-step waiting row.
      const manualStepName = "Remote manual gate";
      const manualStep = await seed.client.createWorkflowStep(seed.workflowId, manualStepName, 99);
      const waitingTitle = "Remote task parked at the gate";
      const waitingTask = await seed.client.seedTask(seed.workspaceId, waitingTitle, {
        workflow_id: seed.workflowId,
        workflow_step_id: manualStep.id,
      });

      // Register the live remote B, plus a second instance that never answers:
      // its degradation must not hide B's rows or the local page.
      const ws = watchWs(testPage);
      const registerResponse = await apiClient.rawRequest("POST", "/api/v1/instances", {
        name: REMOTE_INSTANCE_NAME,
        base_url: remote.baseUrl,
        enabled: true,
      });
      expect(registerResponse.status).toBe(201);
      const instanceId = ((await registerResponse.json()) as { id: string }).id;

      const deadInstance = await apiClient.rawRequest("POST", "/api/v1/instances", {
        name: "Never reachable box",
        base_url: "http://localhost:65530",
        enabled: true,
      });
      expect(deadInstance.status).toBe(201);
      const deadInstanceId = ((await deadInstance.json()) as { id: string }).id;

      // --- The remote waiting row appears in B's section; the dead instance
      // degrades on its own; the local page still lists its own (empty) state.
      await testPage.goto("/needs-you-inbox");
      const liveSection = testPage.locator(
        `[data-testid="remote-inbox-section"][data-instance-id="${instanceId}"]`,
      );
      const deadSection = testPage.locator(
        `[data-testid="remote-inbox-section"][data-instance-id="${deadInstanceId}"]`,
      );
      const remoteRow = liveSection.getByTestId("needs-you-waiting-row").filter({
        hasText: waitingTitle,
      });
      await expect(remoteRow).toBeVisible({ timeout: 30_000 });
      await expect(remoteRow).toContainText(manualStepName);
      // The waiting row names its origin instance and offers no answer surface.
      await expect(liveSection.getByTestId("needs-you-inbox-instance-chip").first()).toHaveText(
        REMOTE_INSTANCE_NAME,
      );
      await expect(remoteRow.getByTestId("needs-you-inbox-row-toggle")).toHaveCount(0);

      await expect(deadSection.getByTestId("remote-inbox-degraded")).toBeVisible({
        timeout: 30_000,
      });
      await expect(deadSection.getByTestId("remote-inbox-degraded")).toContainText(
        "Never reachable box",
      );
      await expect(testPage.getByTestId("needs-you-inbox-empty")).toBeVisible();

      // --- Opening the remote waiting row switches the panel to B's task ---
      await remoteRow.getByTestId("needs-you-waiting-open-task").click();
      await testPage.waitForURL(
        new RegExp(`/t/${waitingTask.task_id}\\?[^/]*kandev_instance=${instanceId}$`),
      );
      await switchToLocalInstance(testPage);

      // --- The row resolves by re-read once the remote no longer reports it ---
      await seed.client.updateTaskState(waitingTask.task_id, "COMPLETED");
      await testPage.goto("/needs-you-inbox");
      await expect(remoteRow).toHaveCount(0, { timeout: 30_000 });
      await expect(liveSection.getByTestId("remote-inbox-empty")).toBeVisible({
        timeout: 30_000,
      });
      await expect(deadSection.getByTestId("remote-inbox-degraded")).toBeVisible();
      await expect(testPage.getByTestId("needs-you-inbox-empty")).toBeVisible();

      // --- A relayed manual-step entry refreshes B's page and notifies ---
      // The agent's own turn completion moves its task into the template's
      // Review step (a manual step), so the entry is attributable to the
      // agent and the moving operator is not excluded from the notification.
      const relayedTitle = "Remote task the agent parked";
      const relayedFrame = ws.waitForEvent("task.manual_step_entered", {
        where: (payload) => payload.origin_instance_id === instanceId,
        timeout: 90_000,
      });
      const relayedTask = await seed.client.createTaskWithAgent(
        seed.workspaceId,
        relayedTitle,
        seed.agentProfileId,
        {
          description: "/e2e:simple-message",
          workflow_id: seed.workflowId,
          workflow_step_id: seed.startStepId,
          repository_ids: [seed.repositoryId],
        },
      );
      if (!relayedTask.session_id) throw new Error("relayed manual-step task returned no session");
      await waitForSessionDone(
        seed.client,
        relayedTask.id,
        relayedTask.session_id,
        "Waiting for the remote turn that parks the task at the manual step",
        60_000,
      );
      const frame = await relayedFrame;
      expect(frame.payload.task_id).toBe(relayedTask.id);

      // The frame refreshes B's page without a reload, so the new wait lists.
      const relayedRow = liveSection.getByTestId("needs-you-waiting-row").filter({
        hasText: relayedTitle,
      });
      await expect(relayedRow).toBeVisible({ timeout: 30_000 });
      // The unified pipeline notifies like a local event of the kind, with the
      // origin instance as the label.
      await expect
        .poll(
          async () =>
            (await getCapturedNotifications(testPage)).some(
              (notification) =>
                notification.title === `[${REMOTE_INSTANCE_NAME}] Task waiting on you`,
            ),
          { message: "Waiting for the relayed manual-step notification" },
        )
        .toBe(true);

      // --- Killing B degrades only B's section ---
      await remote.kill();
      await testPage.goto("/needs-you-inbox");
      await expect(liveSection.getByTestId("remote-inbox-degraded")).toBeVisible({
        timeout: 30_000,
      });
      await expect(liveSection.getByTestId("remote-inbox-degraded")).toContainText(
        REMOTE_INSTANCE_NAME,
      );
      await expect(liveSection.getByTestId("needs-you-waiting-row")).toHaveCount(0);
      await expect(deadSection.getByTestId("remote-inbox-degraded")).toBeVisible();
      await expect(testPage.getByTestId("needs-you-inbox-empty")).toBeVisible();
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

    // The remote-notifications REST surface is removed with the module, not
    // flag-gated: it answers 404 in every build.
    const remoteNotifications = await apiClient.rawRequest("GET", "/api/v1/remote-notifications");
    expect(remoteNotifications.status).toBe(404);

    const shellResponse = await testPage.request.get(
      `/?kandev_instance=${encodeURIComponent(created.id)}`,
    );
    expect(shellResponse.status()).toBe(200);
    const shellHtml = await shellResponse.text();
    expect(shellHtml).toContain("__KANDEV_BOOT_PAYLOAD__");
    expect(shellHtml).not.toContain("__KANDEV_API_PORT");
    expect(shellHtml).not.toContain("__KANDEV_INSTANCE_ID");

    await testPage.goto("/");
    // The sidebar itself renders (registry row persisted, flag off), the
    // picker stays hidden, and no remote-only notification module or entry
    // exists anywhere in the navigation.
    await expect(testPage.getByTestId("app-sidebar-scroll")).toBeVisible();
    await expect(testPage.getByTestId("sidebar-instance-trigger")).toHaveCount(0);
    await expect(testPage.getByTestId("sidebar-notifications-unread-total")).toHaveCount(0);
    await expect(testPage.getByTestId("sidebar-notification-group-local")).toHaveCount(0);
    await expect(testPage.getByTestId(`sidebar-notification-group-${created.id}`)).toHaveCount(0);
  });
});
