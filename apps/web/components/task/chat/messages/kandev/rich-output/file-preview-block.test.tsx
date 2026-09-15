import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceFilePreviewState } from "@/hooks/domains/session/use-workspace-file-preview";
import type { RichOutputFileBlock } from "./types";

const load = vi.fn();
let previewState: WorkspaceFilePreviewState = { kind: "idle" };

vi.mock("@/hooks/domains/session/use-workspace-file-preview", () => ({
  useWorkspaceFilePreview: () => ({ load, state: previewState }),
}));

import { FilePreviewBlock } from "./file-preview-block";

const SVG_SOURCE = '<svg xmlns="http://www.w3.org/2000/svg"><rect width="8" height="8"/></svg>';
const TEXT_SOURCE = 'export const greeting = "hello";';

afterEach(cleanup);

beforeEach(() => {
  load.mockClear();
});

function renderExpandedBlock(path: string, content: string, isBinary: boolean) {
  previewState = { kind: "loaded", response: { content, is_binary: isBinary } as never };
  const block: RichOutputFileBlock = { type: "file", path, title: "Sample" };

  render(<FilePreviewBlock block={block} sessionId="session-1" />);
  fireEvent.click(screen.getByTestId("rich-output-file-preview-toggle"));
}

describe("FilePreviewBlock preview content", () => {
  it("renders a text-encoded SVG as a base64 data URL image", () => {
    renderExpandedBlock("assets/logo.svg", SVG_SOURCE, false);

    expect(screen.getByRole("img").getAttribute("src")).toBe(
      `data:image/svg+xml;base64,${btoa(SVG_SOURCE)}`,
    );
  });

  it("keeps the preview-unavailable copy for a binary non-image file", () => {
    renderExpandedBlock("dist/bundle.zip", "UEsDBAo=", true);

    expect(screen.getByText("Preview unavailable for this file type.")).toBeTruthy();
    expect(screen.queryByRole("img")).toBeNull();
  });

  it("keeps the truncated text preview for a source file", () => {
    renderExpandedBlock("src/index.ts", TEXT_SOURCE, false);

    expect(screen.getByText(TEXT_SOURCE)).toBeTruthy();
    expect(screen.queryByRole("img")).toBeNull();
  });

  it("still renders a binary image from its existing payload", () => {
    renderExpandedBlock("assets/photo.png", "QUJD", true);

    expect(screen.getByRole("img").getAttribute("src")).toBe("data:image/png;base64,QUJD");
  });
});
