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

afterEach(cleanup);

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
