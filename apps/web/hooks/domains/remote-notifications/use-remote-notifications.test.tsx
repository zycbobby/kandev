import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { create } from "zustand";
import { immer } from "zustand/middleware/immer";
import type { StoreApi } from "zustand";
import type { RemoteNotificationOccurrence } from "@/lib/api/domains/remote-notifications-api";
import { createRemoteNotificationsSlice } from "@/lib/state/slices/remote-notifications/remote-notifications-slice";
import type { RemoteNotificationsSlice } from "@/lib/state/slices/remote-notifications/types";
import {
  useRemoteNotificationUnreadByInstance,
  useRemoteNotifications,
} from "./use-remote-notifications";

const api = vi.hoisted(() => ({
  list: vi.fn(),
  markRead: vi.fn(),
  dismiss: vi.fn(),
  readAll: vi.fn(),
}));

vi.mock("@/lib/api/domains/remote-notifications-api", () => ({
  listRemoteNotifications: api.list,
  markRemoteNotificationRead: api.markRead,
  dismissRemoteNotification: api.dismiss,
  markAllRemoteNotificationsRead: api.readAll,
}));

const feature = vi.hoisted(() => ({ enabled: true }));

vi.mock("@/hooks/domains/features/use-feature", () => ({
  useFeature: (name: string) => name === "multiInstance" && feature.enabled,
}));

const holder: { store: StoreApi<RemoteNotificationsSlice> | null } = vi.hoisted(() => ({
  store: null,
}));

vi.mock("@/components/state-provider", () => ({
  useAppStore: (selector: (state: RemoteNotificationsSlice) => unknown) =>
    selector(holder.store!.getState()),
  useAppStoreApi: () => holder.store,
}));

function makeStore() {
  return create<RemoteNotificationsSlice>()(immer((set) => createRemoteNotificationsSlice(set)));
}

function occurrence(
  overrides: Partial<RemoteNotificationOccurrence> = {},
): RemoteNotificationOccurrence {
  return {
    id: "row-1",
    instance_id: "inst-1",
    event: "session.turn_finished",
    occurrence_id: "occ-1",
    task_id: "task-9",
    session_id: "session-9",
    title: "Turn finished",
    body: "The agent finished a turn.",
    created_at: "2026-10-28T00:00:00Z",
    read: false,
    dismissed: false,
    ...overrides,
  };
}

beforeEach(() => {
  feature.enabled = true;
  holder.store = makeStore();
  api.list.mockReset();
  api.markRead.mockReset();
  api.dismiss.mockReset();
  api.readAll.mockReset();
});

describe("useRemoteNotifications", () => {
  it("loads the occurrence list once per session", async () => {
    api.list.mockResolvedValue([occurrence()]);

    const { result } = renderHook(() => useRemoteNotifications());
    expect(result.current.entries).toEqual([]);

    await waitFor(() => expect(holder.store!.getState().remoteNotifications.status).toBe("ready"));
    expect(api.list).toHaveBeenCalledTimes(1);
    expect(holder.store!.getState().remoteNotifications.entries).toEqual([
      expect.objectContaining({ id: "row-1", instanceId: "inst-1" }),
    ]);

    // A second mounted consumer (or a StrictMode remount) must not re-read.
    renderHook(() => useRemoteNotifications());
    await waitFor(() => expect(api.list).toHaveBeenCalledTimes(1));
  });

  it("records a load error without inventing entries", async () => {
    api.list.mockRejectedValue(new Error("offline"));

    renderHook(() => useRemoteNotifications());

    await waitFor(() => expect(holder.store!.getState().remoteNotifications.status).toBe("error"));
    expect(holder.store!.getState().remoteNotifications.entries).toEqual([]);
  });

  it("does not read while the feature flag is off", () => {
    feature.enabled = false;

    renderHook(() => useRemoteNotifications());

    expect(api.list).not.toHaveBeenCalled();
  });

  it("marks an entry read locally and persists it through the REST action", async () => {
    api.list.mockResolvedValue([occurrence()]);
    api.markRead.mockResolvedValue({ success: true });
    const { result } = renderHook(() => useRemoteNotifications());
    await waitFor(() => expect(holder.store!.getState().remoteNotifications.status).toBe("ready"));

    result.current.markRead("row-1");

    expect(api.markRead).toHaveBeenCalledWith("row-1");
    expect(holder.store!.getState().remoteNotifications.entries[0].read).toBe(true);
  });

  it("dismisses an entry locally and through the REST action", async () => {
    api.list.mockResolvedValue([occurrence()]);
    api.dismiss.mockResolvedValue({ success: true });
    const { result } = renderHook(() => useRemoteNotifications());
    await waitFor(() => expect(holder.store!.getState().remoteNotifications.status).toBe("ready"));

    result.current.dismiss("row-1");

    expect(api.dismiss).toHaveBeenCalledWith("row-1");
    expect(holder.store!.getState().remoteNotifications.entries[0].dismissed).toBe(true);
  });

  it("clears every unread badge through the read-all action", async () => {
    api.list.mockResolvedValue([
      occurrence({ id: "row-1" }),
      occurrence({ id: "row-2", instance_id: "inst-2" }),
    ]);
    api.readAll.mockResolvedValue({ success: true });
    const { result } = renderHook(() => useRemoteNotifications());
    await waitFor(() => expect(holder.store!.getState().remoteNotifications.status).toBe("ready"));

    result.current.markAllRead();

    expect(api.readAll).toHaveBeenCalledTimes(1);
    expect(holder.store!.getState().remoteNotifications.entries.every((entry) => entry.read)).toBe(
      true,
    );
  });

  it("keeps read state across a reload that re-fetches read rows", async () => {
    api.list.mockResolvedValue([occurrence()]);
    api.markRead.mockResolvedValue({ success: true });
    const first = renderHook(() => useRemoteNotifications());
    await waitFor(() => expect(holder.store!.getState().remoteNotifications.status).toBe("ready"));
    first.result.current.markRead("row-1");

    // A reload rebuilds the store and re-reads; the backend now returns the
    // occurrence with its persisted read state.
    holder.store = makeStore();
    api.list.mockResolvedValue([occurrence({ read: true })]);
    renderHook(() => useRemoteNotifications());

    await waitFor(() => expect(holder.store!.getState().remoteNotifications.status).toBe("ready"));
    expect(holder.store!.getState().remoteNotifications.entries[0].read).toBe(true);
    expect(renderHook(() => useRemoteNotificationUnreadByInstance()).result.current).toEqual({});
  });
});

describe("useRemoteNotificationUnreadByInstance", () => {
  it("maps each instance to its unread entry count", async () => {
    api.list.mockResolvedValue([
      occurrence({ id: "row-1" }),
      occurrence({ id: "row-2", instance_id: "inst-2" }),
      occurrence({ id: "row-3", instance_id: "inst-2", read: true }),
    ]);

    renderHook(() => useRemoteNotifications());
    await waitFor(() => expect(holder.store!.getState().remoteNotifications.status).toBe("ready"));

    const unread = renderHook(() => useRemoteNotificationUnreadByInstance()).result.current;
    expect(unread).toEqual({ "inst-1": 1, "inst-2": 1 });
  });
});
