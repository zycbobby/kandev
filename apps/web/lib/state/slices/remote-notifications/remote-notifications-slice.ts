import type { Draft } from "immer";
import type { RemoteNotificationOccurrence } from "@/lib/api/domains/remote-notifications-api";
import type {
  RemoteNotificationEntry,
  RemoteNotificationsSlice,
  RemoteNotificationsSliceState,
} from "./types";

export const defaultRemoteNotificationsState: RemoteNotificationsSliceState = {
  remoteNotifications: {
    entries: [],
    status: "idle",
  },
};

/** Converts one wire occurrence (REST row or WS frame payload) to an entry. */
export function toRemoteNotificationEntry(
  row: RemoteNotificationOccurrence,
): RemoteNotificationEntry {
  return {
    id: row.id,
    instanceId: row.instance_id,
    event: row.event,
    occurrenceId: row.occurrence_id,
    taskId: row.task_id,
    sessionId: row.session_id,
    title: row.title,
    body: row.body,
    createdAt: row.created_at,
    read: row.read,
    dismissed: row.dismissed,
  };
}

type ImmerSet = (updater: (draft: Draft<RemoteNotificationsSlice>) => void) => void;

export const createRemoteNotificationsSlice = (set: ImmerSet): RemoteNotificationsSlice => ({
  ...defaultRemoteNotificationsState,
  beginRemoteNotificationsLoad: () =>
    set((draft) => {
      // A load already in flight (or a list already rendered) owns the state;
      // a second trigger, like a StrictMode double mount, must not regress it.
      if (draft.remoteNotifications.status === "idle") draft.remoteNotifications.status = "loading";
    }),
  setRemoteNotificationsLoaded: (rows) =>
    set((draft) => {
      draft.remoteNotifications.entries = rows.map(toRemoteNotificationEntry);
      draft.remoteNotifications.status = "ready";
    }),
  setRemoteNotificationsLoadError: () =>
    set((draft) => {
      // Keep whatever was last rendered; only the status regresses so the
      // next mount can retry.
      draft.remoteNotifications.status = "error";
    }),
  receiveRemoteNotification: (row) =>
    set((draft) => {
      // Idempotent delivery keyed on the local row id: a replayed occurrence
      // (subscription replay plus reconciliation) updates the existing row
      // instead of appending a duplicate.
      const entry = toRemoteNotificationEntry(row);
      const existing = draft.remoteNotifications.entries.findIndex((item) => item.id === entry.id);
      if (existing === -1) {
        draft.remoteNotifications.entries.push(entry);
        return;
      }
      draft.remoteNotifications.entries[existing] = entry;
    }),
  markRemoteNotificationRead: (id) =>
    set((draft) => {
      const entry = draft.remoteNotifications.entries.find((item) => item.id === id);
      if (entry) entry.read = true;
    }),
  markRemoteNotificationDismissed: (id) =>
    set((draft) => {
      const entry = draft.remoteNotifications.entries.find((item) => item.id === id);
      if (entry) entry.dismissed = true;
    }),
  markAllRemoteNotificationsRead: () =>
    set((draft) => {
      for (const entry of draft.remoteNotifications.entries) entry.read = true;
    }),
});
