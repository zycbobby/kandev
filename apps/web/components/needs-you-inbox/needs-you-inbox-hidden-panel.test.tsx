import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ClarificationInboxHiddenBundle } from "@/lib/types/clarification-inbox";
import type { NeedsYouInboxHiddenRow } from "@/lib/types/needs-you-inbox";

const mocks = vi.hoisted(() => ({
  listHidden: vi.fn(),
  restore: vi.fn(),
  listRemoteHidden: vi.fn(),
  restoreRemote: vi.fn(),
  bumpRefreshTick: vi.fn(),
  bumpRemoteRefreshTick: vi.fn(),
  toastError: vi.fn(),
  activeWorkspaceId: "w1",
}));

vi.mock("@/lib/api/domains/clarification-inbox-api", () => ({
  restoreClarificationInboxBundle: (...args: unknown[]) => mocks.restore(...args),
}));

vi.mock("@/lib/api/domains/needs-you-inbox-api", () => ({
  listHiddenNeedsYouInbox: (...args: unknown[]) => mocks.listHidden(...args),
}));

vi.mock("@/lib/api/domains/remote-inbox-api", () => ({
  listRemoteHiddenNeedsYouInbox: (...args: unknown[]) => mocks.listRemoteHidden(...args),
  restoreRemoteClarificationBundle: (...args: unknown[]) => mocks.restoreRemote(...args),
}));

vi.mock("@/lib/toast/sonner", () => ({
  toast: Object.assign(vi.fn(), { error: (...args: unknown[]) => mocks.toastError(...args) }),
}));

vi.mock("@/components/state-provider", () => ({
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  useAppStore: (selector: (s: any) => unknown) =>
    selector({
      workspaces: { activeId: mocks.activeWorkspaceId },
      bumpNeedsYouInboxRefreshTick: mocks.bumpRefreshTick,
      bumpRemoteInboxRefreshTick: mocks.bumpRemoteRefreshTick,
    }),
}));

import { NeedsYouInboxHiddenPanel } from "./needs-you-inbox-hidden-panel";

const SHOW_HIDDEN = "Show hidden";
const WORKSPACE_ONE_CONTEXT = "From workspace one";
const WORKSPACE_TWO_CONTEXT = "From workspace two";

function hiddenBundle(
  overrides: Partial<ClarificationInboxHiddenBundle> = {},
): NeedsYouInboxHiddenRow {
  return {
    kind: "question",
    pending_id: "p1",
    task_id: "t1",
    session_id: "s1",
    session_state: "WAITING_FOR_INPUT",
    task_title: "Task",
    created_at: "2026-09-03T04:54:02Z",
    context: "Deploying",
    messages: [],
    state: "dismissed",
    snooze_until: null,
    ...overrides,
  } as NeedsYouInboxHiddenRow;
}

function hiddenWaitingRow(
  overrides: Partial<Extract<NeedsYouInboxHiddenRow, { kind: "waiting" }>> = {},
): NeedsYouInboxHiddenRow {
  return {
    kind: "waiting",
    id: "mw:t-wait:step-manual:7",
    task_id: "t-wait",
    task_title: "Waiting task",
    workspace_id: "w1",
    step_id: "step-manual",
    step_name: "Manual review",
    reference_time: "2026-09-03T05:00:00Z",
    state: "dismissed",
    snooze_until: null,
    ...overrides,
  };
}

beforeEach(() => {
  mocks.listHidden.mockReset();
  mocks.restore.mockReset().mockResolvedValue(undefined);
  mocks.bumpRefreshTick.mockReset();
  mocks.toastError.mockReset();
  mocks.activeWorkspaceId = "w1";
});

afterEach(() => cleanup());

describe("NeedsYouInboxHiddenPanel", () => {
  it("discloses the hidden count without fetching until expanded", () => {
    render(<NeedsYouInboxHiddenPanel hiddenCount={3} />);

    expect(screen.getByText("3 rows are hidden by your own dismiss or snooze.")).not.toBeNull();
    expect(mocks.listHidden).not.toHaveBeenCalled();
  });

  it("fetches and lists hidden bundles when expanded", async () => {
    mocks.listHidden.mockResolvedValue({ rows: [hiddenBundle()], count: 1, total: 1 });
    render(<NeedsYouInboxHiddenPanel hiddenCount={1} />);

    fireEvent.click(screen.getByText(SHOW_HIDDEN));
    expect(await screen.findByTestId("needs-you-inbox-hidden-row")).not.toBeNull();
    expect(mocks.listHidden).toHaveBeenCalledWith("w1");
  });

  it("lists hidden waiting rows alongside hidden questions, and restores by the mw identity", async () => {
    mocks.listHidden.mockResolvedValue({
      rows: [
        hiddenBundle({ pending_id: "p1", context: "A question" }),
        hiddenWaitingRow({
          task_title: "Deploy awaiting review",
          state: "snoozed",
          snooze_until: "2026-09-03T09:00:00Z",
        }),
      ],
      count: 2,
      total: 2,
    });
    render(<NeedsYouInboxHiddenPanel hiddenCount={2} />);

    fireEvent.click(screen.getByText(SHOW_HIDDEN));
    expect(await screen.findByText("A question")).not.toBeNull();
    expect(screen.getByText("Deploy awaiting review")).not.toBeNull();
    expect(screen.getByText("Manual review")).not.toBeNull();
    expect(screen.getByText(/Snoozed, reappears/)).not.toBeNull();

    fireEvent.click(screen.getAllByText("Restore")[1]);
    await act(async () => {
      await Promise.resolve();
    });
    expect(mocks.restore).toHaveBeenCalledWith("mw:t-wait:step-manual:7");
    expect(mocks.bumpRefreshTick).toHaveBeenCalledTimes(1);
  });

  it("shows an empty message when nothing is currently hidden despite a stale count", async () => {
    mocks.listHidden.mockResolvedValue({ rows: [], count: 0, total: 0 });
    render(<NeedsYouInboxHiddenPanel hiddenCount={1} />);

    fireEvent.click(screen.getByText(SHOW_HIDDEN));
    expect(await screen.findByText("Nothing is currently hidden.")).not.toBeNull();
  });

  it("restores a hidden bundle and re-reads the main list (AC .33)", async () => {
    mocks.listHidden.mockResolvedValue({ rows: [hiddenBundle()], count: 1, total: 1 });
    render(<NeedsYouInboxHiddenPanel hiddenCount={1} />);

    fireEvent.click(screen.getByText(SHOW_HIDDEN));
    fireEvent.click(await screen.findByText("Restore"));

    await act(async () => {
      await Promise.resolve();
    });
    expect(mocks.restore).toHaveBeenCalledWith("p1");
    expect(mocks.bumpRefreshTick).toHaveBeenCalledTimes(1);
  });

  it("shows a retryable error when the hidden read fails", async () => {
    mocks.listHidden.mockRejectedValue(new Error("boom"));
    render(<NeedsYouInboxHiddenPanel hiddenCount={1} />);

    fireEvent.click(screen.getByText(SHOW_HIDDEN));
    expect(await screen.findByText("Could not load hidden rows. Try again.")).not.toBeNull();
  });

  it("prefers the fetched total over a stale hiddenCount prop once loaded", async () => {
    mocks.listHidden.mockResolvedValue({ rows: [hiddenBundle()], count: 1, total: 5 });
    render(<NeedsYouInboxHiddenPanel hiddenCount={1} />);

    expect(screen.getByText("1 row is hidden by your own dismiss or snooze.")).not.toBeNull();

    fireEvent.click(screen.getByText(SHOW_HIDDEN));
    await screen.findByTestId("needs-you-inbox-hidden-row");

    expect(screen.getByText("5 rows are hidden by your own dismiss or snooze.")).not.toBeNull();
  });
});

describe("NeedsYouInboxHiddenPanel workspace changes", () => {
  it("clears the stale list and re-fetches when the active workspace changes", async () => {
    mocks.listHidden.mockResolvedValue({
      rows: [hiddenBundle({ pending_id: "w1-p1", context: WORKSPACE_ONE_CONTEXT })],
      count: 1,
      total: 1,
    });
    const { rerender } = render(<NeedsYouInboxHiddenPanel hiddenCount={1} />);

    fireEvent.click(screen.getByText(SHOW_HIDDEN));
    await screen.findByText(WORKSPACE_ONE_CONTEXT);
    expect(mocks.listHidden).toHaveBeenCalledWith("w1");

    mocks.activeWorkspaceId = "w2";
    mocks.listHidden.mockResolvedValue({
      rows: [hiddenBundle({ pending_id: "w2-p1", context: WORKSPACE_TWO_CONTEXT })],
      count: 1,
      total: 1,
    });
    rerender(<NeedsYouInboxHiddenPanel hiddenCount={1} />);

    expect(await screen.findByText(WORKSPACE_TWO_CONTEXT)).not.toBeNull();
    expect(screen.queryByText(WORKSPACE_ONE_CONTEXT)).toBeNull();
    expect(mocks.listHidden).toHaveBeenLastCalledWith("w2");
  });

  it("discards a stale in-flight response from the previous workspace", async () => {
    let resolveFirst: ((value: unknown) => void) | undefined;
    mocks.listHidden.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveFirst = resolve;
        }),
    );
    const { rerender } = render(<NeedsYouInboxHiddenPanel hiddenCount={1} />);
    fireEvent.click(screen.getByText(SHOW_HIDDEN));

    mocks.activeWorkspaceId = "w2";
    mocks.listHidden.mockResolvedValue({
      rows: [hiddenBundle({ pending_id: "w2-p1", context: WORKSPACE_TWO_CONTEXT })],
      count: 1,
      total: 1,
    });
    rerender(<NeedsYouInboxHiddenPanel hiddenCount={1} />);
    await screen.findByText(WORKSPACE_TWO_CONTEXT);

    await act(async () => {
      resolveFirst?.({
        rows: [hiddenBundle({ pending_id: "w1-p1", context: WORKSPACE_ONE_CONTEXT })],
        count: 1,
        total: 1,
      });
      await Promise.resolve();
    });

    expect(screen.queryByText(WORKSPACE_ONE_CONTEXT)).toBeNull();
    expect(screen.getByText(WORKSPACE_TWO_CONTEXT)).not.toBeNull();
  });
});

describe("NeedsYouInboxHiddenPanel staleness guards", () => {
  it("does not let a restore begun before a workspace switch overwrite the new workspace's panel", async () => {
    mocks.listHidden.mockResolvedValueOnce({
      rows: [hiddenBundle({ pending_id: "w1-p1", context: WORKSPACE_ONE_CONTEXT })],
      count: 1,
      total: 1,
    });
    const { rerender } = render(<NeedsYouInboxHiddenPanel hiddenCount={1} />);
    fireEvent.click(screen.getByText(SHOW_HIDDEN));
    await screen.findByText(WORKSPACE_ONE_CONTEXT);

    let resolveRestore: (() => void) | undefined;
    mocks.restore.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          resolveRestore = resolve;
        }),
    );
    fireEvent.click(screen.getByText("Restore"));

    mocks.activeWorkspaceId = "w2";
    // Both the workspace-change and the hiddenCount-change effects can fire
    // on this rerender; give every call the same answer rather than pinning
    // an exact call count that isn't this test's concern.
    mocks.listHidden.mockResolvedValue({ rows: [], count: 0, total: 0 });
    rerender(<NeedsYouInboxHiddenPanel hiddenCount={0} />);
    await screen.findByText("Nothing is currently hidden.");

    const callsBeforeRestoreSettles = mocks.listHidden.mock.calls.length;
    await act(async () => {
      resolveRestore?.();
      await Promise.resolve();
      await Promise.resolve();
    });

    // The stale w1 continuation must not re-fetch w1 nor reintroduce its row.
    expect(mocks.listHidden.mock.calls.length).toBe(callsBeforeRestoreSettles);
    expect(screen.getByText("Nothing is currently hidden.")).not.toBeNull();
    expect(screen.queryByText(WORKSPACE_ONE_CONTEXT)).toBeNull();
  });

  it("re-enumerates while open when the main list's hidden count changes elsewhere", async () => {
    mocks.listHidden.mockResolvedValueOnce({
      rows: [
        hiddenBundle({ pending_id: "p1", context: "First" }),
        hiddenBundle({ pending_id: "p2", context: "Second" }),
      ],
      count: 2,
      total: 2,
    });
    const { rerender } = render(<NeedsYouInboxHiddenPanel hiddenCount={2} />);
    fireEvent.click(screen.getByText(SHOW_HIDDEN));
    await screen.findByText("First");
    await screen.findByText("Second");

    // Another row was dismissed elsewhere: the main list's hiddenCount grew
    // without any action inside this panel.
    mocks.listHidden.mockResolvedValueOnce({
      rows: [
        hiddenBundle({ pending_id: "p1", context: "First" }),
        hiddenBundle({ pending_id: "p2", context: "Second" }),
        hiddenBundle({ pending_id: "p3", context: "Third" }),
      ],
      count: 3,
      total: 3,
    });
    rerender(<NeedsYouInboxHiddenPanel hiddenCount={3} />);

    expect(await screen.findByText("Third")).not.toBeNull();
    expect(mocks.listHidden).toHaveBeenCalledTimes(2);
    expect(screen.getByText("3 rows are hidden by your own dismiss or snooze.")).not.toBeNull();
  });

  it("re-enumerates while open when a newer main-list response keeps the same hidden count", async () => {
    mocks.listHidden.mockResolvedValueOnce({
      rows: [hiddenBundle({ pending_id: "p1", context: "First" })],
      count: 1,
      total: 1,
    });
    const { rerender } = render(<NeedsYouInboxHiddenPanel hiddenCount={1} listRevision={1} />);
    fireEvent.click(screen.getByText(SHOW_HIDDEN));
    await screen.findByText("First");

    // A newer main-list response replaced one hidden row with another, so the
    // count stayed at one while the disclosed set changed.
    mocks.listHidden.mockResolvedValueOnce({
      rows: [hiddenBundle({ pending_id: "p2", context: "Second" })],
      count: 1,
      total: 1,
    });
    rerender(<NeedsYouInboxHiddenPanel hiddenCount={1} listRevision={2} />);

    expect(await screen.findByText("Second")).not.toBeNull();
    expect(screen.queryByText("First")).toBeNull();
    expect(mocks.listHidden).toHaveBeenCalledTimes(2);
  });
});

// ---- remote source routing (cross-instance-inbox) ----

describe("NeedsYouInboxHiddenPanel — remote source", () => {
  beforeEach(() => {
    mocks.listRemoteHidden.mockReset();
    mocks.restoreRemote.mockReset().mockResolvedValue(undefined);
    mocks.bumpRemoteRefreshTick.mockReset();
  });

  it("enumerates and restores through the forward routes", async () => {
    mocks.listRemoteHidden.mockResolvedValue({
      rows: [hiddenBundle({ pending_id: "p9", context: "Remote hidden" })],
      count: 1,
      total: 1,
    });
    render(<NeedsYouInboxHiddenPanel hiddenCount={1} source={{ instanceId: "i1" }} />);

    fireEvent.click(screen.getByText(SHOW_HIDDEN));
    expect(await screen.findByText("Remote hidden")).not.toBeNull();
    expect(mocks.listRemoteHidden).toHaveBeenCalledWith("i1");
    expect(mocks.listHidden).not.toHaveBeenCalled();

    await act(async () => {
      fireEvent.click(screen.getByText("Restore"));
    });
    expect(mocks.restoreRemote).toHaveBeenCalledWith("i1", "p9");
    expect(mocks.bumpRemoteRefreshTick).toHaveBeenCalledWith("i1");
    expect(mocks.bumpRefreshTick).not.toHaveBeenCalled();
  });

  it("enumerates a remote hidden waiting row with its task and step", async () => {
    mocks.listRemoteHidden.mockResolvedValue({
      rows: [
        {
          kind: "waiting",
          id: "mw:t7:step-gate:3",
          task_id: "t7",
          task_title: "Remote parked task",
          workspace_id: "ws-r",
          step_id: "step-gate",
          step_name: "Manual gate",
          reference_time: "2026-10-28T09:00:00Z",
          instance_id: "i1",
          state: "dismissed",
          snooze_until: null,
        },
      ],
      count: 1,
      total: 1,
    });
    render(<NeedsYouInboxHiddenPanel hiddenCount={1} source={{ instanceId: "i1" }} />);

    fireEvent.click(screen.getByText(SHOW_HIDDEN));
    expect(await screen.findByText("Remote parked task")).not.toBeNull();
    expect(screen.getByText("Manual gate")).not.toBeNull();
    expect(screen.getByText("Dismissed")).not.toBeNull();

    await act(async () => {
      fireEvent.click(screen.getByText("Restore"));
    });
    expect(mocks.restoreRemote).toHaveBeenCalledWith("i1", "mw:t7:step-gate:3");
  });
});
