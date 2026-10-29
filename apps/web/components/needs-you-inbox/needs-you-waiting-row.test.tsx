import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { NeedsYouInboxWaitingRow } from "@/lib/types/needs-you-inbox";

const mocks = vi.hoisted(() => ({
  dismiss: vi.fn(),
  snooze: vi.fn(),
  dismissRemote: vi.fn(),
  snoozeRemote: vi.fn(),
  bumpRefreshTick: vi.fn(),
  bumpRemoteRefreshTick: vi.fn(),
  switchInstance: vi.fn(),
  toastError: vi.fn(),
}));

vi.mock("@/lib/api/domains/clarification-inbox-api", () => ({
  dismissClarificationInboxBundle: (...args: unknown[]) => mocks.dismiss(...args),
  snoozeClarificationInboxBundle: (...args: unknown[]) => mocks.snooze(...args),
}));

vi.mock("@/lib/api/domains/remote-inbox-api", () => ({
  dismissRemoteClarificationBundle: (...args: unknown[]) => mocks.dismissRemote(...args),
  snoozeRemoteClarificationBundle: (...args: unknown[]) => mocks.snoozeRemote(...args),
}));

vi.mock("@/lib/instances/switch-instance", () => ({
  switchInstance: (...args: unknown[]) => mocks.switchInstance(...args),
}));

vi.mock("@/lib/toast/sonner", () => ({
  toast: Object.assign(vi.fn(), {
    error: (...args: unknown[]) => mocks.toastError(...args),
  }),
}));

vi.mock("@/components/state-provider", () => ({
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  useAppStore: (selector: (state: any) => unknown) =>
    selector({
      bumpNeedsYouInboxRefreshTick: mocks.bumpRefreshTick,
      bumpRemoteInboxRefreshTick: mocks.bumpRemoteRefreshTick,
    }),
}));

import { NeedsYouWaitingRow } from "./needs-you-waiting-row";

const ROW_ID = "mw:t1:step-manual:7";

function openRowActionsMenu() {
  const trigger = screen.getByRole("button", { name: "Row actions" });
  fireEvent.pointerDown(trigger, { button: 0, ctrlKey: false, pointerType: "mouse" });
  fireEvent.pointerUp(trigger, { button: 0, ctrlKey: false, pointerType: "mouse" });
  fireEvent.click(trigger);
}

function waitingRow(overrides: Partial<NeedsYouInboxWaitingRow> = {}): NeedsYouInboxWaitingRow {
  return {
    kind: "waiting",
    id: ROW_ID,
    task_id: "t1",
    task_title: "Fix the deploy",
    workspace_id: "w1",
    step_id: "step-manual",
    step_name: "Manual review",
    reference_time: new Date().toISOString(),
    ...overrides,
  };
}

beforeEach(() => {
  mocks.dismiss.mockReset().mockResolvedValue(undefined);
  mocks.snooze.mockReset().mockResolvedValue(undefined);
  mocks.dismissRemote.mockReset().mockResolvedValue(undefined);
  mocks.snoozeRemote.mockReset().mockResolvedValue(undefined);
  mocks.bumpRefreshTick.mockReset();
  mocks.bumpRemoteRefreshTick.mockReset();
  mocks.switchInstance.mockReset().mockResolvedValue(undefined);
  mocks.toastError.mockReset();
});

afterEach(() => cleanup());

describe("NeedsYouWaitingRow", () => {
  it("names the task and the step, with no inline answer surface", () => {
    render(<NeedsYouWaitingRow row={waitingRow()} />);

    expect(screen.getByText("Fix the deploy")).not.toBeNull();
    expect(screen.getByText("Manual review")).not.toBeNull();
    // No toggle: the row body is not an expandable answer panel.
    expect(screen.queryByTestId("needs-you-inbox-row-toggle")).toBeNull();
    expect(screen.queryByRole("button", { name: /question/i })).toBeNull();
  });

  it("falls back to the task id, then to generic copy, when the title is empty", () => {
    const { unmount } = render(<NeedsYouWaitingRow row={waitingRow({ task_title: "" })} />);
    expect(screen.getByText("t1")).not.toBeNull();
    unmount();

    render(<NeedsYouWaitingRow row={waitingRow({ task_title: "", task_id: "" })} />);
    expect(screen.getByText("A task is waiting on you")).not.toBeNull();
  });

  it("links to the task and keeps it reachable from the actions menu", () => {
    render(<NeedsYouWaitingRow row={waitingRow()} />);

    const link = screen.getByTestId("needs-you-waiting-open-task");
    expect(link.getAttribute("href")).toBe("/t/t1");
    expect(link.textContent).toBe("Open task");

    openRowActionsMenu();
    const item = screen.getByTestId("needs-you-inbox-open-task-menu-item");
    expect(item.getAttribute("href")).toBe("/t/t1");
  });

  it("dismisses through the shared sidecar endpoint addressed by the mw identity", async () => {
    render(<NeedsYouWaitingRow row={waitingRow()} />);
    openRowActionsMenu();

    fireEvent.click(screen.getByText("Dismiss"));
    await vi.waitFor(() => expect(mocks.dismiss).toHaveBeenCalledWith(ROW_ID));
    await vi.waitFor(() => expect(mocks.bumpRefreshTick).toHaveBeenCalled());
  });

  it("snoozes with the chosen duration through the same endpoint", async () => {
    render(<NeedsYouWaitingRow row={waitingRow()} />);
    openRowActionsMenu();

    fireEvent.click(screen.getByText("Snooze"));
    fireEvent.click(screen.getByText("1 hour"));
    await vi.waitFor(() => expect(mocks.snooze).toHaveBeenCalledWith(ROW_ID, "1h"));
    await vi.waitFor(() => expect(mocks.bumpRefreshTick).toHaveBeenCalled());
  });

  it("reports a failed write without bumping the refresh tick", async () => {
    mocks.dismiss.mockReset().mockRejectedValue(new Error("boom"));
    render(<NeedsYouWaitingRow row={waitingRow()} />);
    openRowActionsMenu();

    fireEvent.click(screen.getByText("Dismiss"));
    await vi.waitFor(() => expect(mocks.toastError).toHaveBeenCalled());
    expect(mocks.bumpRefreshTick).not.toHaveBeenCalled();
  });
});

describe("NeedsYouWaitingRow — remote source", () => {
  it("shows the origin label and switches the panel to the instance on open", () => {
    render(
      <NeedsYouWaitingRow
        row={waitingRow()}
        source={{ instanceId: "i1", instanceName: "GPU box" }}
      />,
    );
    expect(screen.getByTestId("needs-you-inbox-instance-chip").textContent).toBe("GPU box");

    fireEvent.click(screen.getByTestId("needs-you-waiting-open-task"));
    expect(mocks.switchInstance).toHaveBeenCalledWith("i1", "/t/t1");
  });

  it("dismisses and snoozes through the forward sidecar, bumping that instance", async () => {
    render(
      <NeedsYouWaitingRow
        row={waitingRow()}
        source={{ instanceId: "i1", instanceName: "GPU box" }}
      />,
    );
    openRowActionsMenu();

    fireEvent.click(screen.getByText("Dismiss"));
    await vi.waitFor(() => expect(mocks.dismissRemote).toHaveBeenCalledWith("i1", ROW_ID));
    await vi.waitFor(() => expect(mocks.bumpRemoteRefreshTick).toHaveBeenCalledWith("i1"));
    expect(mocks.dismiss).not.toHaveBeenCalled();
    expect(mocks.bumpRefreshTick).not.toHaveBeenCalled();

    openRowActionsMenu();
    fireEvent.click(screen.getByText("Snooze"));
    fireEvent.click(screen.getByText("1 hour"));
    await vi.waitFor(() => expect(mocks.snoozeRemote).toHaveBeenCalledWith("i1", ROW_ID, "1h"));
  });
});
