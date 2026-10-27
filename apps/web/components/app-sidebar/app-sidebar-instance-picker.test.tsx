import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { InstancesSliceState } from "@/lib/state/slices/instances/types";

const switchMock = vi.hoisted(() => vi.fn());
const feature = vi.hoisted(() => ({ enabled: true }));
const instances = vi.hoisted(() => ({ state: null as InstancesSliceState["instances"] | null }));
const unread = vi.hoisted(() => ({ byInstance: {} as Record<string, number> }));

vi.mock("@/hooks/domains/features/use-local-feature-flag", () => ({
  useLocalFeatureFlag: (name: string) => name === "multiInstance" && feature.enabled,
}));

vi.mock("@/hooks/domains/instances/use-instances", () => ({
  useInstances: () => instances.state,
}));

vi.mock("@/hooks/domains/remote-notifications/use-remote-notifications", () => ({
  useRemoteNotificationUnreadByInstance: () => unread.byInstance,
}));

vi.mock("@/lib/instances/switch-instance", () => ({
  switchInstance: switchMock,
}));

// Radix dropdown primitives rely on pointer/portal behaviour that jsdom does
// not model well. Render them as plain elements so the focus stays on the
// picker's row rendering and selection wiring: content is always mounted and
// `onSelect` fires on click of the item.
vi.mock("@kandev/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuItem: MockDropdownMenuItem,
  DropdownMenuSeparator: () => <hr />,
}));

// Radix dropdown primitives rely on pointer/portal behaviour that jsdom does
// not model well; a plain button keeps `onSelect` firing on click.
function MockDropdownMenuItem({
  children,
  onSelect,
  disabled,
  "data-testid": testId,
  "aria-current": ariaCurrent,
}: {
  children: React.ReactNode;
  onSelect?: () => void;
  disabled?: boolean;
  "data-testid"?: string;
  "aria-current"?: "true";
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      data-testid={testId}
      aria-current={ariaCurrent}
      onClick={() => onSelect?.()}
    >
      {children}
    </button>
  );
}

import { AppSidebarInstancePicker } from "./app-sidebar-instance-picker";

const LOCAL_ITEM = { id: "local", name: "", enabled: true };
const STUDIO_ITEM = { id: "inst-1", name: "Studio workstation", enabled: true };
const RACK_ITEM = { id: "inst-2", name: "Rack server", enabled: true };
const TRIGGER = "sidebar-instance-trigger";
const HEALTHY = {
  reachable: true,
  version: "1.2.3",
  supported: true,
  lastCheckedAt: "2026-10-20T10:00:00Z",
};
const DOWN = {
  reachable: false,
  error: "dial tcp: connection refused",
  supported: false,
  lastCheckedAt: "2026-10-20T10:00:00Z",
};

function setInstances(overrides: Partial<InstancesSliceState["instances"]> = {}) {
  instances.state = {
    items: [LOCAL_ITEM],
    activeId: "local",
    health: {},
    status: "ready",
    ...overrides,
  };
}

beforeEach(() => {
  feature.enabled = true;
  switchMock.mockReset();
  unread.byInstance = {};
  setInstances();
});

afterEach(cleanup);

describe("AppSidebarInstancePicker", () => {
  it("renders the local instance first plus the registry rows", () => {
    setInstances({
      items: [LOCAL_ITEM, STUDIO_ITEM, RACK_ITEM],
    });

    render(<AppSidebarInstancePicker />);

    expect(screen.getByTestId("sidebar-instance-item-local").textContent).toContain(
      "This computer",
    );
    expect(screen.getByTestId("sidebar-instance-item-inst-1").textContent).toContain(
      "Studio workstation",
    );
    expect(screen.getByTestId("sidebar-instance-item-inst-2").textContent).toContain("Rack server");
    expect(screen.getByTestId(TRIGGER).getAttribute("aria-label")).toBe("Switch instance");
  });

  it("highlights the active instance and carries its name on the trigger", () => {
    setInstances({
      items: [LOCAL_ITEM, STUDIO_ITEM],
      activeId: "inst-1",
    });

    render(<AppSidebarInstancePicker />);

    expect(screen.getByTestId("sidebar-instance-item-inst-1").getAttribute("aria-current")).toBe(
      "true",
    );
    expect(screen.getByTestId("sidebar-instance-item-local").getAttribute("aria-current")).toBe(
      null,
    );
    expect(screen.getByTestId(TRIGGER).textContent).toContain("Studio workstation");
  });

  it("renders one health dot per registry instance and none for the local one", () => {
    setInstances({
      items: [LOCAL_ITEM, STUDIO_ITEM, RACK_ITEM],
      health: { "inst-1": HEALTHY, "inst-2": DOWN },
    });

    render(<AppSidebarInstancePicker />);

    expect(screen.getByTestId("sidebar-instance-health-inst-1").getAttribute("data-state")).toBe(
      "healthy",
    );
    expect(screen.getByTestId("sidebar-instance-health-inst-2").getAttribute("data-state")).toBe(
      "unreachable",
    );
    expect(screen.queryByTestId("sidebar-instance-health-local")).toBeNull();
  });

  it("marks an instance without a report as health unknown", () => {
    setInstances({
      items: [LOCAL_ITEM, STUDIO_ITEM],
      health: {},
    });

    render(<AppSidebarInstancePicker />);

    expect(screen.getByTestId("sidebar-instance-health-inst-1").getAttribute("data-state")).toBe(
      "unknown",
    );
  });

  it("disables unenabled instances with an explanatory hint", () => {
    setInstances({
      items: [LOCAL_ITEM, { ...RACK_ITEM, enabled: false }],
    });

    render(<AppSidebarInstancePicker />);

    const item = screen.getByTestId("sidebar-instance-item-inst-2") as HTMLButtonElement;
    expect(item.disabled).toBe(true);
    expect(item.textContent).toContain("Enable this instance in settings to switch to it.");
    fireEvent.click(item);
    expect(switchMock).not.toHaveBeenCalled();
  });

  it("hides the picker when no remote instance is registered", () => {
    setInstances({ items: [LOCAL_ITEM] });

    const { container } = render(<AppSidebarInstancePicker />);

    expect(screen.queryByTestId(TRIGGER)).toBeNull();
    expect(container.textContent).toBe("");
  });

  it("hides the picker while the feature flag is off", () => {
    feature.enabled = false;
    setInstances({
      items: [LOCAL_ITEM, STUDIO_ITEM],
    });

    render(<AppSidebarInstancePicker />);

    expect(screen.queryByTestId(TRIGGER)).toBeNull();
  });

  it("switches to a selected instance but not to the active one", () => {
    setInstances({
      items: [LOCAL_ITEM, STUDIO_ITEM, RACK_ITEM],
      activeId: "inst-1",
    });

    render(<AppSidebarInstancePicker />);

    fireEvent.click(screen.getByTestId("sidebar-instance-item-inst-2"));
    expect(switchMock).toHaveBeenCalledWith("inst-2");

    fireEvent.click(screen.getByTestId("sidebar-instance-item-inst-1"));
    expect(switchMock).toHaveBeenCalledTimes(1);
  });
});

describe("AppSidebarInstancePicker unread badges", () => {
  it("renders one remote-notification unread badge per instance with unread entries", () => {
    setInstances({
      items: [LOCAL_ITEM, STUDIO_ITEM, RACK_ITEM],
    });
    unread.byInstance = { "inst-1": 3 };

    render(<AppSidebarInstancePicker />);

    expect(screen.getByTestId("sidebar-instance-unread-inst-1").textContent).toBe("3");
    // Zero counts and the local row stay badge-free.
    expect(screen.queryByTestId("sidebar-instance-unread-inst-2")).toBeNull();
    expect(screen.queryByTestId("sidebar-instance-unread-local")).toBeNull();
  });

  it("keeps the unread badge out of the row's accessible name", () => {
    setInstances({
      items: [LOCAL_ITEM, STUDIO_ITEM],
    });
    unread.byInstance = { "inst-1": 3 };

    render(<AppSidebarInstancePicker />);

    const badge = screen.getByTestId("sidebar-instance-unread-inst-1");
    expect(badge.getAttribute("aria-hidden")).toBe("true");
  });
});
