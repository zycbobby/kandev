import { cleanup, render, screen } from "@testing-library/react";
import { TooltipProvider } from "@kandev/ui/tooltip";
import { afterEach, describe, expect, it, vi } from "vitest";

const imageReads: Array<[string | null | undefined, string | null | undefined]> = [];

vi.mock("@/hooks/use-markdown-image", () => ({
  useMarkdownImage: (sessionId: string | null | undefined, path: string | null | undefined) => {
    imageReads.push([sessionId, path]);
    return {
      key: JSON.stringify([sessionId, path]),
      src: "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=",
    };
  },
}));

vi.mock("@/components/editors/external-vcs-file-link", () => ({
  ExternalVcsFileLink: (props: Record<string, unknown>) => (
    <span data-testid="external-vcs-file-link-props" data-props={JSON.stringify(props)} />
  ),
  useExternalVcsFileStatus: () => ({ status: "modified" }),
}));

vi.mock("@/hooks/domains/comments/use-markdown-preview-comments", () => ({
  useMarkdownPreviewComments: () => ({
    comments: [],
    commentView: null,
    currentSelection: null,
    textSelection: null,
    dismissOverlays: vi.fn(),
    showCommentsForRange: vi.fn(),
    closeCommentView: vi.fn(),
    closeComposer: vi.fn(),
    openComposer: vi.fn(),
    removeComment: vi.fn(),
    submitAndRunComment: vi.fn(),
    submitComment: vi.fn(),
    updateComment: vi.fn(),
  }),
}));

import { MarkdownPreviewContent } from "./markdown-preview-content";

afterEach(() => {
  cleanup();
  imageReads.length = 0;
});

describe("MarkdownPreviewContent local images", () => {
  it("reads a relative image from the previewed file's directory", () => {
    render(
      <TooltipProvider>
        <MarkdownPreviewContent
          path=".issues/demo/output/report.md"
          worktreePath="/workspace/popo"
          content={"![causal chain](causal-chain.svg)"}
          sessionId="session-1"
          onTogglePreview={vi.fn()}
        />
      </TooltipProvider>,
    );

    expect(imageReads).toContainEqual(["session-1", ".issues/demo/output/causal-chain.svg"]);
    const thumbnail = screen.getByRole("button", { name: /causal chain/i });
    expect(thumbnail.querySelector("img")?.getAttribute("src")).toBe(
      "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=",
    );
  });

  it("keeps external image sources on the browser path", () => {
    render(
      <TooltipProvider>
        <MarkdownPreviewContent
          path=".issues/demo/output/report.md"
          worktreePath="/workspace/popo"
          content={"![remote](https://images.example.test/chart.png)"}
          sessionId="session-1"
          onTogglePreview={vi.fn()}
        />
      </TooltipProvider>,
    );

    expect(imageReads.every(([, path]) => path === null)).toBe(true);
    expect(screen.getByAltText("remote").getAttribute("src")).toBe(
      "https://images.example.test/chart.png",
    );
  });
});
