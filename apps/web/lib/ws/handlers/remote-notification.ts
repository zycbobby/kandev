import type { StoreApi } from "zustand";
import type { AppState } from "@/lib/state/store";
import { LOCAL_INSTANCE_ID } from "@/lib/state/slices/instances/types";
import type { BackendMessageMap } from "@/lib/types/backend";
import type { WsHandlers } from "@/lib/ws/handlers/types";

/** The WS action the local backend's instance relays publish occurrences under. */
export const REMOTE_NOTIFICATION_ACTION = "remote_notification" as const;

type RemoteNotificationMessage = BackendMessageMap[typeof REMOTE_NOTIFICATION_ACTION];

/**
 * The cross-instance notifications module's only live input. Writes the
 * remote-notifications slice and nothing else: no toast, no browser
 * notification, no sound, so the local instance's notification behavior
 * stays byte-identical.
 *
 * The write is gated on the active instance: the single WS connection follows
 * the panel's selection, so while a remote instance is active the frames
 * arriving on it are that remote's own federation, not this panel's, and must
 * not populate the module. The REST load, which always resolves against the
 * local backend, keeps the module's data available in that view.
 */
export function registerRemoteNotificationHandlers(store: StoreApi<AppState>): WsHandlers {
  return {
    [REMOTE_NOTIFICATION_ACTION]: (message: RemoteNotificationMessage) => {
      if (store.getState().instances.activeId !== LOCAL_INSTANCE_ID) return;
      store.getState().receiveRemoteNotification(message.payload);
    },
  };
}
