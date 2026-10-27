import { describe, expect, it } from "vitest";
import { create } from "zustand";
import { immer } from "zustand/middleware/immer";
import type { RemoteNotificationOccurrence } from "@/lib/api/domains/remote-notifications-api";
import type { RemoteNotificationsSlice } from "./types";
import {
  createRemoteNotificationsSlice,
  defaultRemoteNotificationsState,
} from "./remote-notifications-slice";
import {
  selectRemoteNotificationUnreadByInstance,
  selectRemoteNotificationUnreadCount,
} from "./selectors";

function makeStore() {
  return create<RemoteNotificationsSlice>()(immer((set) => createRemoteNotificationsSlice(set)));
}

function occurrence(
  overrides: Partial<RemoteNotificationOccurrence> = {},
): RemoteNotificationOccurrence {
  return {
    id: "occ-row-1",
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

describe("remote notifications slice", () => {
  it("defaults to an empty module that has not loaded", () => {
    const store = makeStore();
    expect(store.getState().remoteNotifications).toEqual({
      entries: [],
      status: "idle",
    });
    expect(defaultRemoteNotificationsState.remoteNotifications).toEqual({
      entries: [],
      status: "idle",
    });
  });

  it("replaces the entries from a REST load and marks the module ready", () => {
    const store = makeStore();
    store.getState().beginRemoteNotificationsLoad();
    expect(store.getState().remoteNotifications.status).toBe("loading");

    store
      .getState()
      .setRemoteNotificationsLoaded([
        occurrence({ id: "row-1" }),
        occurrence({ id: "row-2", instance_id: "inst-2", read: true }),
      ]);

    expect(store.getState().remoteNotifications.status).toBe("ready");
    expect(store.getState().remoteNotifications.entries).toEqual([
      expect.objectContaining({ id: "row-1", instanceId: "inst-1", taskId: "task-9", read: false }),
      expect.objectContaining({ id: "row-2", instanceId: "inst-2", read: true }),
    ]);
  });

  it("keeps whatever rendered after a failed load so the next mount can retry", () => {
    const store = makeStore();
    store.getState().beginRemoteNotificationsLoad();
    store.getState().setRemoteNotificationsLoaded([occurrence()]);
    store.getState().beginRemoteNotificationsLoad();
    store.getState().setRemoteNotificationsLoadError();

    expect(store.getState().remoteNotifications.entries).toHaveLength(1);
    expect(store.getState().remoteNotifications.status).toBe("error");
  });

  it("receives a live occurrence and dedups repeated frames by local id", () => {
    const store = makeStore();
    store.getState().receiveRemoteNotification(occurrence({ id: "row-1" }));
    store.getState().receiveRemoteNotification(occurrence({ id: "row-1", read: true }));
    store.getState().receiveRemoteNotification(occurrence({ id: "row-2", instance_id: "inst-2" }));

    const entries = store.getState().remoteNotifications.entries;
    expect(entries).toHaveLength(2);
    // The replayed frame updates the existing row in place rather than
    // appending a duplicate.
    expect(entries[0]).toMatchObject({ id: "row-1", read: true });
    expect(entries[1]).toMatchObject({ id: "row-2", instanceId: "inst-2" });
  });

  it("marks one entry, all entries, and a dismissal", () => {
    const store = makeStore();
    store
      .getState()
      .setRemoteNotificationsLoaded([
        occurrence({ id: "row-1", instance_id: "inst-1" }),
        occurrence({ id: "row-2", instance_id: "inst-1" }),
        occurrence({ id: "row-3", instance_id: "inst-2" }),
      ]);

    store.getState().markRemoteNotificationRead("row-1");
    expect(store.getState().remoteNotifications.entries.map((entry) => entry.read)).toEqual([
      true,
      false,
      false,
    ]);

    store.getState().markRemoteNotificationDismissed("row-2");
    expect(store.getState().remoteNotifications.entries.map((entry) => entry.dismissed)).toEqual([
      false,
      true,
      false,
    ]);

    store.getState().markAllRemoteNotificationsRead();
    expect(store.getState().remoteNotifications.entries.every((entry) => entry.read)).toBe(true);
  });
});

describe("remote notification selectors", () => {
  function stateWith(rows: RemoteNotificationOccurrence[]) {
    const store = makeStore();
    store.getState().setRemoteNotificationsLoaded(rows);
    return store.getState();
  }

  it("counts unread entries per instance, excluding read and dismissed rows", () => {
    const state = stateWith([
      occurrence({ id: "row-1", instance_id: "inst-1" }),
      occurrence({ id: "row-2", instance_id: "inst-1", read: true }),
      occurrence({ id: "row-3", instance_id: "inst-1", dismissed: true }),
      occurrence({ id: "row-4", instance_id: "inst-2" }),
      occurrence({ id: "row-5", instance_id: "inst-2", read: true }),
    ]);

    expect(selectRemoteNotificationUnreadByInstance(state)).toEqual({
      "inst-1": 1,
      "inst-2": 1,
    });
    expect(selectRemoteNotificationUnreadCount(state)).toBe(2);
  });

  it("returns empty counts while no entry is unread", () => {
    const state = stateWith([occurrence({ read: true })]);

    expect(selectRemoteNotificationUnreadByInstance(state)).toEqual({});
    expect(selectRemoteNotificationUnreadCount(state)).toBe(0);
  });

  it("reuses the counts reference while the entries are unchanged", () => {
    const store = makeStore();
    store.getState().setRemoteNotificationsLoaded([occurrence()]);
    const state = store.getState();
    const counts = selectRemoteNotificationUnreadByInstance(state);
    expect(counts).toEqual({ "inst-1": 1 });
    // Subscribers bind the selector through useSyncExternalStore, which polls
    // getSnapshot between store updates: a fresh object per call re-renders
    // them in a loop (React error #185).
    expect(selectRemoteNotificationUnreadByInstance(state)).toBe(counts);

    store.getState().markRemoteNotificationRead("occ-row-1");
    const afterMark = selectRemoteNotificationUnreadByInstance(store.getState());
    expect(afterMark).toEqual({});
    expect(afterMark).not.toBe(counts);
  });
});
