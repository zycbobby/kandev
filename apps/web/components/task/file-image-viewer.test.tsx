import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FileImageViewer } from "./file-image-viewer";

const SVG_SOURCE = '<svg xmlns="http://www.w3.org/2000/svg"><rect width="8" height="8"/></svg>';

// FileViewerHeader resolves the VCS link against the session store; the header
// chrome is not what these cases assert.
vi.mock("./file-viewer-header", () => ({
  FileViewerHeader: ({ actions }: { actions?: React.ReactNode }) => <div>{actions}</div>,
}));

afterEach(cleanup);

describe("FileImageViewer source", () => {
  it("passes binary content through as the data URL payload", () => {
    render(<FileImageViewer path="assets/logo.png" content="QUJD" isBinary />);

    expect(screen.getByTestId("image-viewer").getAttribute("src")).toBe(
      "data:image/png;base64,QUJD",
    );
  });

  it("base64-encodes UTF-8 text content", () => {
    render(<FileImageViewer path="assets/logo.svg" content={SVG_SOURCE} />);

    expect(screen.getByTestId("image-viewer").getAttribute("src")).toBe(
      `data:image/svg+xml;base64,${btoa(SVG_SOURCE)}`,
    );
  });
});

describe("FileImageViewer source toggle", () => {
  it("hides the action when no handler is provided", () => {
    render(<FileImageViewer path="assets/logo.png" content="QUJD" isBinary />);

    expect(screen.queryByTestId("show-source-toggle")).toBeNull();
  });

  it("invokes the handler from the header action", () => {
    const onShowSource = vi.fn();
    render(
      <FileImageViewer path="assets/logo.svg" content={SVG_SOURCE} onShowSource={onShowSource} />,
    );

    screen.getByTestId("show-source-toggle").click();
    expect(onShowSource).toHaveBeenCalledOnce();
  });
});
