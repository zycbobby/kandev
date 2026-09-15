import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import type { OpenFileTab } from "@/lib/types/backend";
import { FileTabContent } from "./file-tab-content";

const FILE_EDITOR_CONTENT_TEST_ID = "file-editor-content";
const IMAGE_VIEWER_TEST_ID = "file-image-viewer";
const SOURCE_ACTION_TEST_ID = "svg-preview-toggle";

vi.mock("./file-editor-content", () => ({
  FileEditorContent: ({
    previewKind,
    renderedPreview,
    worktreePath,
    onTogglePreview,
    sourcePreviewAction,
  }: {
    previewKind?: string;
    renderedPreview?: boolean;
    worktreePath?: string;
    onTogglePreview?: () => void;
    sourcePreviewAction?: { testId: string; onClick: () => void };
  }) => (
    <div
      data-testid="file-editor-content"
      data-preview-kind={previewKind}
      data-rendered-preview={String(renderedPreview)}
      data-worktree-path={worktreePath}
      data-source-action-testid={sourcePreviewAction?.testId}
    >
      <button type="button" onClick={onTogglePreview}>
        Toggle preview
      </button>
      {sourcePreviewAction && (
        <button type="button" onClick={sourcePreviewAction.onClick}>
          {sourcePreviewAction.testId}
        </button>
      )}
    </div>
  ),
}));

vi.mock("@kandev/ui/tabs", () => ({
  TabsContent: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}));

vi.mock("./file-viewer-header", () => ({
  FileViewerExternalLink: () => null,
}));

vi.mock("./file-image-viewer", () => ({
  FileImageViewer: ({
    isBinary,
    onShowSource,
  }: {
    isBinary?: boolean;
    onShowSource?: () => void;
  }) => (
    <div data-testid="file-image-viewer" data-is-binary={String(isBinary)}>
      <button type="button" onClick={onShowSource}>
        Show code
      </button>
    </div>
  ),
}));
vi.mock("./file-binary-viewer", () => ({ FileBinaryViewer: () => null }));

const file: OpenFileTab = {
  path: "README.md",
  name: "README.md",
  content: "# README",
  originalContent: "# README",
  originalHash: "hash",
  isDirty: false,
  renderedPreview: true,
};

afterEach(cleanup);

describe("FileTabContent Markdown preview", () => {
  it("renders a Markdown tab in preview mode and forwards the toggle", () => {
    const onTogglePreview = vi.fn();

    render(
      <FileTabContent
        tab={file}
        activeSession={null}
        activeSessionId="session-1"
        taskId="task-1"
        isSaving={false}
        onFileChange={vi.fn()}
        onFileSave={vi.fn()}
        onFileDelete={vi.fn()}
        onTogglePreview={onTogglePreview}
      />,
    );

    expect(screen.getByTestId(FILE_EDITOR_CONTENT_TEST_ID).getAttribute("data-preview-kind")).toBe(
      "markdown",
    );
    expect(
      screen.getByTestId(FILE_EDITOR_CONTENT_TEST_ID).getAttribute("data-rendered-preview"),
    ).toBe("true");
    fireEvent.click(screen.getByRole("button", { name: "Toggle preview" }));
    expect(onTogglePreview).toHaveBeenCalledOnce();
  });

  it("uses the effective workspace path for desktop file viewers", () => {
    render(
      <FileTabContent
        tab={file}
        activeSession={{
          workspace_path: "/tmp/task-root",
          worktree_path: "/tmp/task-root/kandev",
        }}
        activeSessionId="session-1"
        taskId="task-1"
        isSaving={false}
        onFileChange={vi.fn()}
        onFileSave={vi.fn()}
        onFileDelete={vi.fn()}
      />,
    );

    expect(screen.getByTestId(FILE_EDITOR_CONTENT_TEST_ID).getAttribute("data-worktree-path")).toBe(
      "/tmp/task-root",
    );
  });
});

describe("FileTabContent HTML preview", () => {
  it("delegates HTML preview ownership to the file editor content", () => {
    render(
      <FileTabContent
        tab={{
          ...file,
          path: "reports/index.html",
          name: "index.html",
          content: "<h1>Report</h1>",
          renderedPreview: false,
        }}
        activeSession={null}
        activeSessionId="session-1"
        taskId="task-1"
        isSaving={false}
        onFileChange={vi.fn()}
        onFileSave={vi.fn()}
        onFileDelete={vi.fn()}
      />,
    );

    expect(screen.getByTestId(FILE_EDITOR_CONTENT_TEST_ID).getAttribute("data-preview-kind")).toBe(
      "html",
    );
  });
});

const svgTab: OpenFileTab = {
  path: "assets/logo.svg",
  name: "logo.svg",
  content: '<svg xmlns="http://www.w3.org/2000/svg"/>',
  originalContent: '<svg xmlns="http://www.w3.org/2000/svg"/>',
  originalHash: "hash",
  isDirty: false,
};

function renderSvgTab() {
  return render(
    <FileTabContent
      tab={svgTab}
      activeSession={null}
      activeSessionId="session-1"
      taskId="task-1"
      isSaving={false}
      onFileChange={vi.fn()}
      onFileSave={vi.fn()}
      onFileDelete={vi.fn()}
    />,
  );
}

describe("FileTabContent SVG source toggle", () => {
  it("renders the image viewer by default, without a source action on the editor", () => {
    renderSvgTab();

    expect(screen.getByTestId(IMAGE_VIEWER_TEST_ID)).toBeTruthy();
    expect(screen.queryByTestId(FILE_EDITOR_CONTENT_TEST_ID)).toBeNull();
  });

  it("switches to the source editor and offers a way back to the image", () => {
    renderSvgTab();

    fireEvent.click(screen.getByRole("button", { name: "Show code" }));

    const editor = screen.getByTestId(FILE_EDITOR_CONTENT_TEST_ID);
    expect(editor.getAttribute("data-preview-kind")).toBe("none");
    expect(editor.getAttribute("data-source-action-testid")).toBe(SOURCE_ACTION_TEST_ID);
    expect(screen.queryByTestId(IMAGE_VIEWER_TEST_ID)).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: SOURCE_ACTION_TEST_ID }));
    expect(screen.getByTestId(IMAGE_VIEWER_TEST_ID)).toBeTruthy();
  });

  it("keeps a text file whose extension is a binary image in the editor", () => {
    render(
      <FileTabContent
        tab={{ ...file, path: "assets/fake.png", name: "fake.png", content: "not an image" }}
        activeSession={null}
        activeSessionId="session-1"
        taskId="task-1"
        isSaving={false}
        onFileChange={vi.fn()}
        onFileSave={vi.fn()}
        onFileDelete={vi.fn()}
      />,
    );

    expect(screen.getByTestId(FILE_EDITOR_CONTENT_TEST_ID)).toBeTruthy();
    expect(screen.queryByTestId(IMAGE_VIEWER_TEST_ID)).toBeNull();
  });
});
