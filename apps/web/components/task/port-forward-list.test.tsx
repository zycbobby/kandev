import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { TooltipProvider } from "@kandev/ui/tooltip";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const remote = vi.hoisted(() => ({ value: false }));
const backend = vi.hoisted(() => ({ url: "" }));

vi.mock("@/hooks/domains/instances/use-is-remote-instance", () => ({
  useIsRemoteInstance: () => remote.value,
}));

vi.mock("@/lib/config", () => ({
  getBackendConfig: () => ({ apiBaseUrl: backend.url }),
}));

// Radix tooltips only render on pointer/focus; render the content inline so
// the degraded affordance's explanation is assertable directly.
vi.mock("@kandev/ui/tooltip", () => ({
  Tooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipTrigger: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipContent: ({ children }: { children: React.ReactNode }) => <span>{children}</span>,
  TooltipProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

import { PortListSection } from "./port-forward-list";

const TOGGLE_3000 = "port-forward-tunnel-toggle-3000";

function view(onTunnelStart: (port: number, requestedPort?: number) => void) {
  return (
    <PortListSection
      detectedPorts={[{ port: 3000, address: "0.0.0.0", process: "node" }]}
      manualPorts={[]}
      sessionId="session-1"
      loading={false}
      loaded={true}
      onRefresh={vi.fn()}
      activeTunnels={new Map()}
      pendingTunnels={new Set()}
      onTunnelStart={onTunnelStart}
      onTunnelStop={vi.fn()}
    />
  );
}

beforeEach(() => {
  remote.value = false;
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("PortListSection tunnel toggle host-bound degradation", () => {
  it("starts a tunnel for the local instance", () => {
    const onTunnelStart = vi.fn();
    render(view(onTunnelStart));

    const toggle = screen.getByTestId(TOGGLE_3000) as HTMLButtonElement;
    expect(toggle.disabled).toBe(false);
    fireEvent.click(toggle);
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    expect(onTunnelStart).toHaveBeenCalledWith(3000, undefined);
  });

  it("disables the tunnel toggle with an explanation for a remote instance", () => {
    remote.value = true;
    const onTunnelStart = vi.fn();
    render(view(onTunnelStart));

    const toggle = screen.getByTestId(TOGGLE_3000) as HTMLButtonElement;
    expect(toggle.disabled).toBe(true);
    fireEvent.click(toggle);
    // The tunnel form never opens, so no start command can be dispatched.
    expect(screen.queryByRole("button", { name: "Start" })).toBeNull();
    expect(onTunnelStart).not.toHaveBeenCalled();
    expect(screen.getAllByText("Unavailable for remote instances").length).toBeGreaterThan(0);
  });
});

// @covers AC-UI-PORT-FORWARDING-ACTIVE-FIRST-001.9
describe.each([
  ["HTTPS", "https://dashboard.example:8443/base"],
  ["HTTP", "http://dashboard.example:8443/base"],
])("forwarded port URLs with an %s backend", (_scheme, baseUrl) => {
  it("displays, opens and copies the dedicated and proxy URLs", async () => {
    backend.url = baseUrl;
    const copy = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue();
    const openBrowserPanel = vi.fn();
    render(
      <TooltipProvider>
        <PortListSection
          detectedPorts={[]}
          manualPorts={[]}
          sessionId="session-1"
          loading={false}
          loaded
          onRefresh={vi.fn()}
          activeTunnels={new Map([[3000, 49152]])}
          pendingTunnels={new Set()}
          onTunnelStart={vi.fn()}
          onTunnelStop={vi.fn()}
          onOpenBrowserPanel={openBrowserPanel}
        />
      </TooltipProvider>,
    );

    const tunnelUrl = "http://dashboard.example:49152/";
    const proxyUrl = `${baseUrl}/port-proxy/session-1/3000/`;
    const tunnelRow = screen.getByText(tunnelUrl).parentElement!;
    const proxyRow = screen.getByText(proxyUrl).parentElement!;

    expect(
      within(tunnelRow).getByRole("link", { name: "Open in new tab" }).getAttribute("href"),
    ).toBe(tunnelUrl);
    expect(
      within(proxyRow).getByRole("link", { name: "Open in new tab" }).getAttribute("href"),
    ).toBe(proxyUrl);
    fireEvent.click(within(tunnelRow).getByRole("button", { name: "Copy URL" }));
    await waitFor(() => expect(copy).toHaveBeenCalledWith(tunnelUrl));
    fireEvent.click(within(proxyRow).getByRole("button", { name: "Copy URL" }));
    await waitFor(() => expect(copy).toHaveBeenCalledWith(proxyUrl));
    expect(within(tunnelRow).queryByRole("button", { name: "Open in browser panel" })).toBeNull();
    fireEvent.click(within(proxyRow).getByRole("button", { name: "Open in browser panel" }));
    expect(openBrowserPanel).toHaveBeenCalledWith(proxyUrl);
  });
});
