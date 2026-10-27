import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const remote = vi.hoisted(() => ({ value: false }));

vi.mock("@/hooks/domains/instances/use-is-remote-instance", () => ({
  useIsRemoteInstance: () => remote.value,
}));

// Radix tooltips only render on pointer/focus; render the content inline so
// the degraded affordance's explanation is assertable directly.
vi.mock("@kandev/ui/tooltip", () => ({
  Tooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipTrigger: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipContent: ({ children }: { children: React.ReactNode }) => <span>{children}</span>,
}));

import { PortUrlActions } from "./port-forward-dialog-actions";

const PROXY_URL = "http://localhost:38429/port-proxy/s1/3000/";
const BROWSER_TEST_ID = "port-forward-open-browser-3000";

beforeEach(() => {
  remote.value = false;
});

afterEach(cleanup);

describe("PortUrlActions host-bound degradation", () => {
  it("keeps the open actions enabled for the local instance", () => {
    const onOpenBrowserPanel = vi.fn();
    render(
      <PortUrlActions
        url={PROXY_URL}
        onOpenBrowserPanel={onOpenBrowserPanel}
        browserActionTestId={BROWSER_TEST_ID}
      />,
    );

    const browserButton = screen.getByTestId(BROWSER_TEST_ID) as HTMLButtonElement;
    expect(browserButton.disabled).toBe(false);
    fireEvent.click(browserButton);
    expect(onOpenBrowserPanel).toHaveBeenCalledWith(PROXY_URL);
    expect(screen.getByRole("link", { name: "Open in new tab" })).toBeTruthy();
  });

  it("disables the open actions with an explanation for a remote instance", () => {
    remote.value = true;
    const onOpenBrowserPanel = vi.fn();
    render(
      <PortUrlActions
        url={PROXY_URL}
        onOpenBrowserPanel={onOpenBrowserPanel}
        browserActionTestId={BROWSER_TEST_ID}
      />,
    );

    const browserButton = screen.getByTestId(BROWSER_TEST_ID) as HTMLButtonElement;
    expect(browserButton.disabled).toBe(true);
    fireEvent.click(browserButton);
    expect(onOpenBrowserPanel).not.toHaveBeenCalled();

    // No anchor means the new-tab action cannot navigate at all.
    expect(screen.queryByRole("link", { name: "Open in new tab" })).toBeNull();
    const newTabButton = screen.getByRole("button", { name: "Open in new tab" });
    expect((newTabButton as HTMLButtonElement).disabled).toBe(true);

    expect(screen.getAllByText("Unavailable for remote instances").length).toBeGreaterThan(0);
    // Copying the URL stays available: it is not a host-bound action.
    expect(screen.getByRole("button", { name: "Copy URL" }).getAttribute("disabled")).toBe(null);
  });
});
