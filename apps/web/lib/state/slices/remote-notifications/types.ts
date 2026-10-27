import type { RemoteNotificationOccurrence } from "@/lib/api/domains/remote-notifications-api";

/**
 * One module entry: a relayed occurrence converted to the slice's own shape.
 * `id` is the local backend's row id, shared by the REST read/dismiss
 * actions and the WebSocket frames, so deduplication and persistence address
 * the same row without a lookup.
 */
export type RemoteNotificationEntry = {
  id: string;
  instanceId: string;
  event: string;
  occurrenceId: string;
  taskId: string;
  sessionId: string;
  title: string;
  body: string;
  createdAt: string;
  read: boolean;
  dismissed: boolean;
};

export type RemoteNotificationsStatus = "idle" | "loading" | "ready" | "error";

export type RemoteNotificationsSliceState = {
  remoteNotifications: {
    /**
     * Occurrences in arrival order (oldest first): the REST load returns
     * oldest-first rows and live frames append. The module renders each
     * group newest-first by reversing this order.
     */
    entries: RemoteNotificationEntry[];
    status: RemoteNotificationsStatus;
  };
};

export type RemoteNotificationsSliceActions = {
  beginRemoteNotificationsLoad: () => void;
  setRemoteNotificationsLoaded: (rows: RemoteNotificationOccurrence[]) => void;
  setRemoteNotificationsLoadError: () => void;
  /** Upserts one live occurrence; a repeated local id updates the row in place. */
  receiveRemoteNotification: (row: RemoteNotificationOccurrence) => void;
  markRemoteNotificationRead: (id: string) => void;
  markRemoteNotificationDismissed: (id: string) => void;
  markAllRemoteNotificationsRead: () => void;
};

export type RemoteNotificationsSlice = RemoteNotificationsSliceState &
  RemoteNotificationsSliceActions;
