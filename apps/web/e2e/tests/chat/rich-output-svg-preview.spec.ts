// Routing: /t/{taskId} (task-keyed).
//
// Path 4 of the change checklist: a chat rich-output block that references an
// SVG renders the image (not the truncated text preview), and its existing
// open-file action leads to the full viewer, which renders the same image.
import fs from "node:fs";
import path from "node:path";
import { test, expect } from "../../fixtures/test-base";
import { SessionPage } from "../../pages/session-page";

const SVG_FILE = "rich-output-logo.svg";
const SVG_CONTENT =
  '<svg xmlns="http://www.w3.org/2000/svg" width="120" height="80">' +
  '<rect width="120" height="80" fill="#a78bfa"/></svg>';

function richOutputScript(): string {
  const payload = {
    version: 1,
    title: "Asset report",
    blocks: [
      {
        type: "file",
        path: SVG_FILE,
        title: "Rendered logo",
        caption: "Workspace-backed SVG asset.",
        mime_type: "image/svg+xml",
      },
    ],
  };
  return `e2e:mcp:kandev:show_rich_output_kandev(${JSON.stringify(payload)})`;
}

test("renders a chat rich-output SVG block as an image and opens the viewer", async ({
  testPage,
  apiClient,
  seedData,
}) => {
  test.setTimeout(120_000);

  fs.writeFileSync(path.join(seedData.repositoryPath, SVG_FILE), SVG_CONTENT);
  const task = await apiClient.createTaskWithAgent(
    seedData.workspaceId,
    "Rich output SVG",
    seedData.agentProfileId,
    {
      description: richOutputScript(),
      workflow_id: seedData.workflowId,
      workflow_step_id: seedData.startStepId,
      repository_ids: [seedData.repositoryId],
    },
  );

  await testPage.goto(`/t/${task.id}`);
  const session = new SessionPage(testPage);
  await session.waitForLoad();
  await session.waitForChatIdle({ timeout: 45_000 });

  const block = session.activeChat().getByTestId("rich-output-file");
  await expect(block).toBeVisible({ timeout: 30_000 });
  await block.getByTestId("rich-output-file-preview-toggle").click();

  // The block renders the image, not the truncated source text. Match the
  // `img` element rather than the img role: the block's buttons contain
  // Tabler icon SVGs, which also satisfy the role query.
  const blockImage = block.locator("img");
  await expect(blockImage).toBeVisible({ timeout: 15_000 });
  await expect
    .poll(async () => blockImage.evaluate((node) => (node as HTMLImageElement).naturalWidth))
    .toBeGreaterThan(0);
  await expect(block).not.toContainText("<svg");

  // The existing open-file action reaches the full viewer, which renders it too.
  await block.getByTestId("rich-output-file-open").click();
  const viewerImage = testPage.getByTestId("image-viewer");
  await expect(viewerImage).toBeVisible({ timeout: 15_000 });
  await expect
    .poll(async () => viewerImage.evaluate((node) => (node as HTMLImageElement).naturalWidth))
    .toBeGreaterThan(0);
});
