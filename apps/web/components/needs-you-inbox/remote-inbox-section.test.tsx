import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { i18n } from "@/lib/i18n";
import type { RemoteInboxInstanceState } from "@/lib/state/slices/needs-you-inbox/types";
import type { RemoteNeedsYouQuestionRow, RemoteNeedsYouWaitingRow } from "@/lib/types/remote-inbox";

const mocks = vi.hoisted(() => ({
  bumpRetryTick: vi.fn(),
  switchInstance: vi.fn(),
  dismissRemote: vi.fn(),
  snoozeRemote: vi.fn(),
  restoreRemote: vi.fn(),
  listRemoteHidden: vi.fn(),
}));

vi.mock("@/components/state-provider", () => ({
  useAppStore: (selector: (state: Record<string, unknown>) => unknown) =>
    selector({
      bumpRemoteInboxRefreshTick: vi.fn(),
      bumpRemoteInboxRetryTick: mocks.bumpRetryTick,
      instances: {
        items: [
          { id: "local", name: "", enabled: true },
          { id: "i1", name: "GPU box", enabled: true },
        ],
      },
    }),
}));

vi.mock("@/lib/instances/switch-instance", () => ({
  switchInstance: (...args: unknown[]) => mocks.switchInstance(...args),
}));

vi.mock("@/lib/api/domains/remote-inbox-api", () => ({
  listRemoteNeedsYouInbox: vi.fn(),
  listRemoteHiddenNeedsYouInbox: (...args: unknown[]) => mocks.listRemoteHidden(...args),
  dismissRemoteClarificationBundle: (...args: unknown[]) => mocks.dismissRemote(...args),
  snoozeRemoteClarificationBundle: (...args: unknown[]) => mocks.snoozeRemote(...args),
  restoreRemoteClarificationBundle: (...args: unknown[]) => mocks.restoreRemote(...args),
  remoteClarificationRespondTarget: (instanceId: string) =>
    `https://fwd.test/${instanceId}/respond`,
}));

vi.mock("@/lib/toast/sonner", () => ({
  toast: Object.assign(vi.fn(), { error: vi.fn() }),
}));

vi.mock("@/components/task/chat/clarification-panel-section", () => ({
  ClarificationPanelSection: () => <div data-testid="mock-clarification-panel-section" />,
}));

import { RemoteInboxSection } from "./remote-inbox-section";

const WAITING_ROW_TESTID = "needs-you-waiting-row";

function remoteBundle(
  overrides: Partial<RemoteNeedsYouQuestionRow> = {},
): RemoteNeedsYouQuestionRow {
  return {
    kind: "question",
    pending_id: "p1",
    task_id: "t1",
    session_id: "s1",
    session_state: "WAITING_FOR_INPUT",
    task_title: "Fix the deploy",
    created_at: "2026-10-28T10:00:00Z",
    context: "",
    messages: [],
    instance_id: "i1",
    workspace_id: "ws-remote",
    ...overrides,
  };
}

function remoteWaitingRow(
  overrides: Partial<RemoteNeedsYouWaitingRow> = {},
): RemoteNeedsYouWaitingRow {
  return {
    kind: "waiting",
    id: "mw:t2:step-gate:7",
    task_id: "t2",
    task_title: "Parked release",
    workspace_id: "ws-remote",
    step_id: "step-gate",
    step_name: "Manual gate",
    reference_time: "2026-10-28T09:00:00Z",
    instance_id: "i1",
    ...overrides,
  };
}

function instanceState(
  overrides: Partial<RemoteInboxInstanceState> = {},
): RemoteInboxInstanceState {
  return {
    instanceId: "i1",
    status: "ready",
    rows: [],
    count: 0,
    hiddenCount: 0,
    nextSnoozeExpiry: null,
    hasMore: false,
    appliedGeneration: 1,
    lastAppliedOk: true,
    ...overrides,
  };
}

function openRowActionsMenu() {
  const trigger = screen.getByRole("button", { name: "Row actions" });
  fireEvent.pointerDown(trigger, { button: 0, ctrlKey: false, pointerType: "mouse" });
  fireEvent.pointerUp(trigger, { button: 0, ctrlKey: false, pointerType: "mouse" });
  fireEvent.click(trigger);
}

beforeEach(() => {
  mocks.bumpRetryTick.mockReset();
  mocks.switchInstance.mockReset().mockResolvedValue(undefined);
  mocks.dismissRemote.mockReset().mockResolvedValue(undefined);
  mocks.snoozeRemote.mockReset().mockResolvedValue(undefined);
  mocks.restoreRemote.mockReset().mockResolvedValue(undefined);
  mocks.listRemoteHidden.mockReset().mockResolvedValue({ rows: [], count: 0, total: 0 });
});

afterEach(async () => {
  cleanup();
  await i18n.changeLanguage("en");
});

describe("RemoteInboxSection", () => {
  it("lists rows under the registry instance name", () => {
    render(
      <RemoteInboxSection
        instance={instanceState({
          rows: [remoteBundle()],
          count: 1,
        })}
      />,
    );
    expect(screen.getByTestId("remote-inbox-section-title").textContent).toBe("GPU box");
    expect(screen.getByTestId("needs-you-inbox-row")).not.toBeNull();
    expect(screen.getByTestId("needs-you-inbox-instance-chip").textContent).toBe("GPU box");
  });

  it("renders one empty sentence for a ready instance with no rows", () => {
    render(<RemoteInboxSection instance={instanceState()} />);
    expect(screen.getByTestId("remote-inbox-empty")).not.toBeNull();
    expect(screen.queryByTestId("needs-you-inbox-row")).toBeNull();
    expect(screen.queryByTestId(WAITING_ROW_TESTID)).toBeNull();
  });

  it("renders a remote waiting row with the origin label, the task, and the step", () => {
    render(
      <RemoteInboxSection instance={instanceState({ rows: [remoteWaitingRow()], count: 1 })} />,
    );
    const row = screen.getByTestId(WAITING_ROW_TESTID);
    expect(row.textContent).toContain("Parked release");
    expect(row.textContent).toContain("Manual gate");
    expect(screen.getByTestId("needs-you-inbox-instance-chip").textContent).toBe("GPU box");
    // A waiting row has no answer surface, in the remote section either.
    expect(screen.queryByTestId("needs-you-inbox-row-toggle")).toBeNull();
    expect(screen.queryByTestId("mock-clarification-panel-section")).toBeNull();
  });

  it("opens a remote waiting row's task by switching the panel to its instance", () => {
    render(
      <RemoteInboxSection instance={instanceState({ rows: [remoteWaitingRow()], count: 1 })} />,
    );
    fireEvent.click(screen.getByTestId("needs-you-waiting-open-task"));
    expect(mocks.switchInstance).toHaveBeenCalledWith("i1", expect.stringContaining("/t/t2"));
  });

  it("dismisses and snoozes a remote waiting row through the forward sidecar", async () => {
    render(
      <RemoteInboxSection instance={instanceState({ rows: [remoteWaitingRow()], count: 1 })} />,
    );
    openRowActionsMenu();
    fireEvent.click(screen.getByText("Dismiss"));
    await vi.waitFor(() =>
      expect(mocks.dismissRemote).toHaveBeenCalledWith("i1", "mw:t2:step-gate:7"),
    );

    openRowActionsMenu();
    fireEvent.click(screen.getByText("Snooze"));
    fireEvent.click(screen.getByText("1 hour"));
    await vi.waitFor(() =>
      expect(mocks.snoozeRemote).toHaveBeenCalledWith("i1", "mw:t2:step-gate:7", "1h"),
    );
  });

  it("degraded state names the instance, offers retry, and renders no answer form", () => {
    render(
      <RemoteInboxSection
        instance={instanceState({
          status: "error",
          degradedReason: "unreachable",
          rows: [remoteBundle()],
        })}
      />,
    );
    expect(screen.getByTestId("remote-inbox-degraded").textContent).toContain("GPU box");
    expect(screen.queryByTestId("needs-you-inbox-row")).toBeNull();
    expect(screen.queryByTestId(WAITING_ROW_TESTID)).toBeNull();
    expect(screen.queryByTestId("mock-clarification-panel-section")).toBeNull();

    fireEvent.click(screen.getByTestId("remote-inbox-retry"));
    expect(mocks.bumpRetryTick).toHaveBeenCalledWith("i1");
  });

  it("open-task on a remote row switches the panel to the row's instance", () => {
    render(<RemoteInboxSection instance={instanceState({ rows: [remoteBundle()] })} />);
    fireEvent.click(screen.getByTestId("needs-you-inbox-open-task"));
    expect(mocks.switchInstance).toHaveBeenCalledWith("i1", expect.stringContaining("/t/"));
  });
});
