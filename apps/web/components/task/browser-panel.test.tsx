import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { BrowserPanel } from "./browser-panel";

const usePreviewCapture = vi.hoisted(() => vi.fn());
const remote = vi.hoisted(() => ({ value: false }));
vi.mock("@/hooks/use-preview-capture", () => ({ usePreviewCapture }));
vi.mock("@/hooks/domains/instances/use-is-remote-instance", () => ({
  useIsRemoteInstance: () => remote.value,
}));
vi.mock("@/hooks/use-preview-console-forwarder", () => ({
  usePreviewConsoleForwarder: vi.fn(),
}));
vi.mock("@/components/state-provider", () => ({
  useAppStore: (selector: (state: unknown) => unknown) =>
    selector({
      tasks: { activeTaskId: "task-1", activeSessionId: "session-1" },
      processes: { devProcessBySessionId: {}, outputsByProcessId: {} },
    }),
}));

const PROXY_PREVIEW_URL = "http://localhost:3000/products";
const ANNOTATE_BUTTON = "Annotate (0)";

const capture = {
  items: [],
  snapshot: { task_id: "task-1", revision: 0, items: [] },
  mode: null,
  draft: null,
  draftComment: "",
  setDraftComment: vi.fn(),
  candidateLabel: null,
  captureError: null,
  isRasterizing: false,
  isUploading: false,
  isMutating: false,
  mutationError: null,
  startCapture: vi.fn(),
  cancelCapture: vi.fn(),
  discardDraft: vi.fn(),
  saveDraft: vi.fn(),
  update: vi.fn(),
  remove: vi.fn(),
  clear: vi.fn(),
  handleIframeLoad: vi.fn(),
};

beforeEach(() => {
  remote.value = false;
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("BrowserPanel preview feedback", () => {
  it("offers task-backed annotation for a localhost page through the injected proxy", () => {
    usePreviewCapture.mockReturnValue(capture);
    render(<BrowserPanel panelId="browser-1" params={{ url: PROXY_PREVIEW_URL }} />);

    expect(screen.getByRole("button", { name: ANNOTATE_BUTTON })).toBeTruthy();
    expect(usePreviewCapture).toHaveBeenCalledWith(
      expect.objectContaining({
        taskId: "task-1",
        enabled: true,
        source: {
          kind: "browser",
          sessionId: "session-1",
          label: "http://localhost:3000",
        },
      }),
    );
  });

  it("keeps task-backed annotation visible when secondary actions overflow", async () => {
    class ResizeObserverMock {
      constructor(private readonly callback: ResizeObserverCallback) {}

      observe(target: Element) {
        Object.defineProperty(target, "getBoundingClientRect", {
          configurable: true,
          value: () => ({ width: 240 }),
        });
        this.callback([{ target } as ResizeObserverEntry], this as unknown as ResizeObserver);
      }

      disconnect() {}
    }

    vi.stubGlobal("ResizeObserver", ResizeObserverMock);
    usePreviewCapture.mockReturnValue(capture);
    render(<BrowserPanel panelId="browser-1" params={{ url: PROXY_PREVIEW_URL }} />);

    await waitFor(() => expect(screen.getByTestId("panel-header-overflow")).toBeTruthy());
    expect(screen.getByRole("button", { name: ANNOTATE_BUTTON })).toBeTruthy();
  });

  it("allows the annotation popover to extend beyond the panel header", () => {
    usePreviewCapture.mockReturnValue(capture);
    render(<BrowserPanel panelId="browser-1" params={{ url: PROXY_PREVIEW_URL }} />);

    fireEvent.click(screen.getByRole("button", { name: ANNOTATE_BUTTON }));
    const actionSlot = screen.getByTestId("preview-feedback-popover").parentElement?.parentElement;
    expect(actionSlot?.className).toContain("overflow-visible");
    expect(actionSlot?.className).not.toContain("overflow-hidden");
  });

  it("replaces the host-bound preview with an explanation for a remote instance", () => {
    remote.value = true;
    usePreviewCapture.mockReturnValue(capture);
    render(<BrowserPanel panelId="browser-1" params={{ url: PROXY_PREVIEW_URL }} />);

    expect(screen.getByTestId("browser-panel-remote-unavailable").textContent).toContain(
      "Unavailable for remote instances",
    );
    // No iframe may render remote content, and the capture controls that ride
    // the same host-bound port-proxy disappear with it.
    expect(screen.queryByTitle("Browser Preview")).toBeNull();
    expect(screen.queryByRole("button", { name: ANNOTATE_BUTTON })).toBeNull();
    expect(usePreviewCapture).toHaveBeenCalledWith(expect.objectContaining({ enabled: false }));
  });
});
