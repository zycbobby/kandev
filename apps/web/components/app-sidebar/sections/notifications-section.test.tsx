import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { TooltipProvider } from "@kandev/ui/tooltip";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { InstanceListItem } from "@/lib/state/slices/instances/types";
import type { RemoteNotificationEntry } from "@/lib/state/slices/remote-notifications/types";
import type { RemoteNotificationsController } from "@/hooks/domains/remote-notifications/use-remote-notifications";

const mocks = vi.hoisted(() => ({
  controller: {
    entries: [] as RemoteNotificationEntry[],
    status: "ready",
    markRead: vi.fn(),
    dismiss: vi.fn(),
    markAllRead: vi.fn(),
  } satisfies RemoteNotificationsController as RemoteNotificationsController,
  featureOn: true,
  instanceItems: [] as InstanceListItem[],
  sectionExpanded: {} as Record<string, boolean>,
  toggleSection: vi.fn(),
  setCollapsed: vi.fn(),
  switchInstance: vi.fn(),
}));

vi.mock("@/hooks/domains/remote-notifications/use-remote-notifications", () => ({
  useRemoteNotifications: () => mocks.controller,
}));

vi.mock("@/hooks/domains/features/use-feature", () => ({
  useFeature: (name: string) => name === "multiInstance" && mocks.featureOn,
}));

vi.mock("@/components/state-provider", () => ({
  useAppStore: (selector: (state: unknown) => unknown) =>
    selector({
      instances: { items: mocks.instanceItems },
      appSidebar: { sectionExpanded: mocks.sectionExpanded },
      toggleAppSidebarSection: mocks.toggleSection,
      setAppSidebarCollapsed: mocks.setCollapsed,
    }),
}));

vi.mock("@/lib/instances/switch-instance", () => ({
  switchInstance: mocks.switchInstance,
}));

import { NotificationsSection } from "./notifications-section";

const STUDIO: InstanceListItem = { id: "inst-1", name: "Studio workstation", enabled: true };
const RACK: InstanceListItem = { id: "inst-2", name: "Rack server", enabled: true };

function entry(overrides: Partial<RemoteNotificationEntry> = {}): RemoteNotificationEntry {
  return {
    id: "e1",
    instanceId: "inst-1",
    event: "session.turn_finished",
    occurrenceId: "occ-1",
    taskId: "task-9",
    sessionId: "session-9",
    title: "Turn finished",
    body: "The agent finished a turn.",
    createdAt: "2026-10-28T00:00:00Z",
    read: false,
    dismissed: false,
    ...overrides,
  };
}

function renderSection(collapsed = false) {
  return render(
    <TooltipProvider>
      <NotificationsSection collapsed={collapsed} />
    </TooltipProvider>,
  );
}

beforeEach(() => {
  mocks.featureOn = true;
  mocks.instanceItems = [STUDIO, RACK];
  mocks.sectionExpanded = { notifications: true };
  mocks.controller = {
    entries: [],
    status: "ready",
    markRead: vi.fn(),
    dismiss: vi.fn(),
    markAllRead: vi.fn(),
  };
  vi.clearAllMocks();
});

afterEach(() => cleanup());

describe("NotificationsSection", () => {
  it("renders nothing while the feature is off, the list is not ready, or no entry exists", () => {
    const loaded: RemoteNotificationsController = {
      ...mocks.controller,
      status: "ready",
      entries: [entry()],
    };
    const cases: Array<{
      name: string;
      controller: RemoteNotificationsController;
      featureOn: boolean;
    }> = [
      { name: "feature off", controller: loaded, featureOn: false },
      { name: "list not ready", controller: { ...loaded, status: "loading" }, featureOn: true },
      { name: "no entries", controller: { ...loaded, entries: [] }, featureOn: true },
    ];
    for (const { controller, featureOn } of cases) {
      mocks.controller = controller;
      mocks.featureOn = featureOn;
      const { container, unmount } = renderSection();
      expect(container.firstChild).toBeNull();
      unmount();
    }
  });

  it("groups entries by instance in registry order with per-instance unread badges", () => {
    mocks.controller = {
      ...mocks.controller,
      entries: [
        entry({ id: "e1", instanceId: "inst-2" }),
        entry({ id: "e2", instanceId: "inst-1" }),
        entry({ id: "e3", instanceId: "inst-1", read: true }),
        entry({ id: "e4", instanceId: "inst-2", dismissed: true }),
      ],
    };

    renderSection();

    const groups = screen.getAllByTestId(/^sidebar-notification-group-/);
    expect(groups.map((group) => group.getAttribute("data-testid"))).toEqual([
      "sidebar-notification-group-inst-1",
      "sidebar-notification-group-inst-2",
    ]);
    expect(groups[0].textContent).toContain("Studio workstation");
    expect(groups[1].textContent).toContain("Rack server");
    expect(screen.getByTestId("sidebar-notification-unread-inst-1").textContent).toBe("1");
    expect(screen.getByTestId("sidebar-notification-unread-inst-2").textContent).toBe("1");
    // Dismissed entries never render.
    expect(screen.queryByTestId("sidebar-notification-row-e4")).toBeNull();
  });

  it("falls back to the raw instance id for a group the registry no longer lists", () => {
    mocks.controller = {
      ...mocks.controller,
      entries: [entry({ instanceId: "inst-gone" })],
    };

    renderSection();

    expect(screen.getByTestId("sidebar-notification-group-inst-gone").textContent).toContain(
      "inst-gone",
    );
  });

  it("keeps the newest entry first inside a group", () => {
    mocks.controller = {
      ...mocks.controller,
      entries: [
        entry({ id: "e-old", title: "Older turn" }),
        entry({ id: "e-new", title: "Newer turn" }),
      ],
    };

    renderSection();

    const group = screen.getByTestId("sidebar-notification-group-inst-1");
    const rows = group.querySelectorAll('[data-testid^="sidebar-notification-row-"]');
    expect(Array.from(rows).map((row) => row.getAttribute("data-testid"))).toEqual([
      "sidebar-notification-row-e-new",
      "sidebar-notification-row-e-old",
    ]);
  });

  it("renders the collapsed rail button while the sidebar is collapsed", () => {
    mocks.controller = { ...mocks.controller, entries: [entry()] };

    renderSection(true);

    expect(screen.queryByTestId("sidebar-notification-row-e1")).toBeNull();
    expect(screen.queryAllByRole("button", { name: "Remote notifications" })).toHaveLength(1);
  });
});

describe("NotificationsSection interactions", () => {
  it("switches to the entry's instance on the task route and marks the entry read", () => {
    mocks.controller = {
      ...mocks.controller,
      entries: [entry({ id: "e1", instanceId: "inst-1", taskId: "task-9" })],
    };

    renderSection();
    fireEvent.click(screen.getByTestId("sidebar-notification-row-e1"));

    expect(mocks.switchInstance).toHaveBeenCalledWith("inst-1", "/t/task-9");
    expect(mocks.controller.markRead).toHaveBeenCalledWith("e1");
  });

  it("switches without a task path when the entry carries no task", () => {
    mocks.controller = {
      ...mocks.controller,
      entries: [entry({ taskId: "" })],
    };

    renderSection();
    fireEvent.click(screen.getByTestId("sidebar-notification-row-e1"));

    expect(mocks.switchInstance).toHaveBeenCalledWith("inst-1", undefined);
    expect(mocks.controller.markRead).toHaveBeenCalledWith("e1");
  });

  it("dismisses one entry without switching instances", () => {
    mocks.controller = {
      ...mocks.controller,
      entries: [entry({ id: "e1" })],
    };

    renderSection();
    fireEvent.click(screen.getByTestId("sidebar-notification-dismiss-e1"));

    expect(mocks.controller.dismiss).toHaveBeenCalledWith("e1");
    expect(mocks.switchInstance).not.toHaveBeenCalled();
  });

  it("shows the total unread badge and clears it through read-all", () => {
    mocks.controller = {
      ...mocks.controller,
      entries: [
        entry({ id: "e1", instanceId: "inst-1" }),
        entry({ id: "e2", instanceId: "inst-2" }),
        entry({ id: "e3", instanceId: "inst-1", read: true }),
      ],
    };

    renderSection();
    expect(screen.getByTestId("sidebar-notifications-unread-total").textContent).toBe("2");

    fireEvent.click(screen.getByTestId("sidebar-notifications-read-all"));
    expect(mocks.controller.markAllRead).toHaveBeenCalledTimes(1);
  });
});
