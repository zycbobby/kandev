// Routing: /t/{taskId} (task-keyed).
//
// A text-encoded image (SVG) must open as a rendered image rather than the
// source editor, with an explicit path back to the source and a way to return.
// A text file that merely carries a binary image extension (.png) must keep
// opening in the editor.
import path from "node:path";
import { type Page } from "@playwright/test";
import { test, expect } from "../../fixtures/test-base";
import type { SeedData } from "../../fixtures/test-base";
import type { ApiClient } from "../../helpers/api-client";
import type { BackendContext } from "../../fixtures/backend";
import { GitHelper, makeGitEnv, createStandardProfile } from "../../helpers/git-helper";
import { SessionPage } from "../../pages/session-page";

const SVG_SOURCE =
  '<svg xmlns="http://www.w3.org/2000/svg" width="120" height="80">' +
  '<rect width="120" height="80" fill="#4ade80"/></svg>';
const TEXT_PNG_CONTENT = "this file is plain text despite its extension";
const EDITED_SVG = '<svg xmlns="http://www.w3.org/2000/svg" width="40" height="40"/>';
const IMAGE_VIEWER = "image-viewer";
const SHOW_SOURCE = "show-source-toggle";
const PREVIEW_SVG = "svg-preview-toggle";

async function setupSvgTask({
  testPage,
  apiClient,
  seedData,
  backend,
  taskTitle,
}: {
  testPage: Page;
  apiClient: ApiClient;
  seedData: SeedData;
  backend: BackendContext;
  taskTitle: string;
}): Promise<{ session: SessionPage; svgPath: string; textPngPath: string }> {
  // Root-level, uniquely named files: the desktop file tree renders nested
  // directories collapsed, and the seeded repo is shared by every test in the
  // worker, so a second commit of the same path would have nothing to add.
  const suffix = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  const svgPath = `svg-preview-${suffix}.svg`;
  const textPngPath = `text-png-${suffix}.png`;
  const git = new GitHelper(
    path.join(backend.tmpDir, "repos", "e2e-repo"),
    makeGitEnv(backend.tmpDir),
  );
  git.createFile(svgPath, SVG_SOURCE);
  git.createFile(textPngPath, TEXT_PNG_CONTENT);
  git.stageAll();
  git.commit(`add ${svgPath} and ${textPngPath}`);

  const profile = await createStandardProfile(apiClient, `svg-preview-${Date.now()}`);
  const task = await apiClient.createTaskWithAgent(seedData.workspaceId, taskTitle, profile.id, {
    description: "/e2e:simple-message",
    workflow_id: seedData.workflowId,
    workflow_step_id: seedData.startStepId,
    repository_ids: [seedData.repositoryId],
  });

  await testPage.goto(`/t/${task.id}`);
  const session = new SessionPage(testPage);
  await session.waitForLoad();
  await session.waitForChatIdle({ timeout: 45_000 });
  return { session, svgPath, textPngPath };
}

async function openFile(session: SessionPage, filePath: string): Promise<void> {
  await session.clickTab("Files");
  const node = session.files.locator(`[data-testid="file-tree-node"][data-path="${filePath}"]`);
  await expect(node).toBeVisible({ timeout: 15_000 });
  await node.click();
}

/** Waits for the image to actually decode rather than merely be present. */
async function expectDecodedImage(testPage: Page): Promise<void> {
  const image = testPage.getByTestId(IMAGE_VIEWER);
  await expect(image).toBeVisible({ timeout: 15_000 });
  await expect
    .poll(async () => image.evaluate((node) => (node as HTMLImageElement).naturalWidth))
    .toBeGreaterThan(0);
}

test.describe("SVG file preview", () => {
  test.describe.configure({ retries: 1 });

  test("renders an SVG as an image and round-trips through the source editor", async ({
    testPage,
    apiClient,
    seedData,
    backend,
  }) => {
    test.setTimeout(120_000);

    const { session, svgPath } = await setupSvgTask({
      testPage,
      apiClient,
      seedData,
      backend,
      taskTitle: "SVG Preview Toggle",
    });

    await openFile(session, svgPath);

    // Opens on the rendered image, not the XML source.
    await expectDecodedImage(testPage);
    await expect(testPage.getByTestId(PREVIEW_SVG)).toHaveCount(0);

    // Show code: the source editor appears with a way back to the image.
    await testPage.getByTestId(SHOW_SOURCE).click();
    await expect(testPage.getByTestId("file-source-panel")).toBeVisible({ timeout: 10_000 });
    const backToImage = testPage.getByTestId(PREVIEW_SVG);
    await expect(backToImage).toBeVisible({ timeout: 10_000 });
    await expect(testPage.getByTestId(IMAGE_VIEWER)).toHaveCount(0);
    await expect(testPage.locator(".monaco-editor:visible .view-lines")).toContainText("svg");

    // Preview SVG: back to the rendered image.
    await backToImage.click();
    await expectDecodedImage(testPage);
    await expect(testPage.getByTestId(PREVIEW_SVG)).toHaveCount(0);
  });

  test("saves an SVG edit made in the source state", async ({
    testPage,
    apiClient,
    seedData,
    backend,
  }) => {
    test.setTimeout(120_000);

    const { session, svgPath } = await setupSvgTask({
      testPage,
      apiClient,
      seedData,
      backend,
      taskTitle: "SVG Preview Save",
    });

    await openFile(session, svgPath);
    await testPage.getByTestId(SHOW_SOURCE).click();

    const editor = testPage.locator(".monaco-editor:visible");
    await expect(editor).toBeVisible({ timeout: 10_000 });
    await editor.locator(".view-lines").click();
    await testPage.keyboard.press("ControlOrMeta+A");
    // insertText, not type(): per-keystroke typing collides with Monaco's
    // auto-closing pairs, which eats the "/" characters in the XML.
    await testPage.keyboard.insertText(EDITED_SVG);

    // Scope to the file source panel: "Save" is not unique in the task page.
    const sourcePanel = testPage.getByTestId("file-source-panel");
    const saveButton = sourcePanel.getByRole("button", { name: /Save/ });
    await expect(saveButton).toBeEnabled({ timeout: 10_000 });
    await saveButton.click();
    await expect(saveButton).toBeDisabled({ timeout: 15_000 });

    // Returning to the image re-renders from the edited buffer. The edit is
    // arbitrary text, and a hand-edited document is not guaranteed to stay
    // well-formed XML, so this asserts the state round-trip rather than a
    // successful decode: a malformed SVG renders as a broken image with the
    // source one action away, which is its documented recoverable state.
    await testPage.getByTestId(PREVIEW_SVG).click();
    await expect(testPage.getByTestId(IMAGE_VIEWER)).toBeVisible({ timeout: 10_000 });
    await expect(testPage.getByTestId(SHOW_SOURCE)).toBeVisible();

    // The edit survives the round-trip.
    await testPage.getByTestId(SHOW_SOURCE).click();
    await expect(testPage.locator(".monaco-editor:visible .view-lines")).toContainText(
      'width="40"',
    );
  });

  test("keeps a text file with a binary image extension in the editor", async ({
    testPage,
    apiClient,
    seedData,
    backend,
  }) => {
    test.setTimeout(120_000);

    const { session, textPngPath } = await setupSvgTask({
      testPage,
      apiClient,
      seedData,
      backend,
      taskTitle: "SVG Preview Text Png",
    });

    await openFile(session, textPngPath);

    await expect(testPage.locator(".monaco-editor:visible .view-lines")).toContainText(
      "plain text despite its extension",
    );
    await expect(testPage.getByTestId(IMAGE_VIEWER)).toHaveCount(0);
    await expect(testPage.getByTestId(SHOW_SOURCE)).toHaveCount(0);
  });
});
