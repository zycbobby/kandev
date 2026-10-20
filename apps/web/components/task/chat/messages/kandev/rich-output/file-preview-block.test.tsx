import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceFilePreviewState } from "@/hooks/domains/session/use-workspace-file-preview";
import type { FilePreviewProps } from "@/lib/rich-output/catalog";

const load = vi.fn();
let previewState: WorkspaceFilePreviewState = { kind: "idle" };

vi.mock("@/hooks/domains/session/use-workspace-file-preview", () => ({
  useWorkspaceFilePreview: () => ({ load, state: previewState }),
}));

import { FilePreview } from "./file-preview-block";

const SVG_SOURCE = '<svg xmlns="http://www.w3.org/2000/svg"><rect width="8" height="8"/></svg>';
const TEXT_SOURCE = 'export const greeting = "hello";';

afterEach(cleanup);

beforeEach(() => {
  load.mockClear();
});

function renderPreview(path: string, content: string, isBinary: boolean) {
  previewState = { kind: "loaded", response: { content, is_binary: isBinary } as never };
  const file: FilePreviewProps = { path, title: "Sample" };

  render(<FilePreview {...file} sessionId="session-1" />);
}

// Image-kind previews start expanded; everything else opens on toggle.
function expandIfCollapsed() {
  const toggle = screen.getByTestId("rich-output-file-preview-toggle");
  if (toggle.getAttribute("aria-expanded") !== "true") fireEvent.click(toggle);
}

describe("FilePreview preview content", () => {
  it("renders a text-encoded SVG as a base64 data URL image", () => {
    renderPreview("assets/logo.svg", SVG_SOURCE, false);
    expandIfCollapsed();

    expect(screen.getByRole("img").getAttribute("src")).toBe(
      `data:image/svg+xml;base64,${btoa(SVG_SOURCE)}`,
    );
  });

  it("keeps the preview-unavailable copy for a binary non-image file", () => {
    renderPreview("dist/bundle.zip", "UEsDBAo=", true);
    expandIfCollapsed();

    expect(screen.getByText("Preview unavailable for this file type.")).toBeTruthy();
    expect(screen.queryByRole("img")).toBeNull();
  });

  it("keeps the truncated text preview for a source file", () => {
    renderPreview("src/index.ts", TEXT_SOURCE, false);
    expandIfCollapsed();

    expect(screen.getByText(TEXT_SOURCE)).toBeTruthy();
    expect(screen.queryByRole("img")).toBeNull();
  });

  it("still renders a binary image from its existing payload", () => {
    renderPreview("assets/photo.png", "QUJD", true);
    expandIfCollapsed();

    expect(screen.getByRole("img").getAttribute("src")).toBe("data:image/png;base64,QUJD");
  });
});
