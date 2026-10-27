"use client";

import { useCallback, useEffect } from "react";
import { useAppStore, useAppStoreApi } from "@/components/state-provider";
import { useLocalFeatureFlag } from "@/hooks/domains/features/use-local-feature-flag";
import {
  dismissRemoteNotification,
  listRemoteNotifications,
  markAllRemoteNotificationsRead,
  markRemoteNotificationRead,
} from "@/lib/api/domains/remote-notifications-api";
import { selectRemoteNotificationUnreadByInstance } from "@/lib/state/slices/remote-notifications/selectors";
import type {
  RemoteNotificationEntry,
  RemoteNotificationsSlice,
  RemoteNotificationsStatus,
} from "@/lib/state/slices/remote-notifications/types";

/** The module's UI-facing controller: current rows plus the write actions. */
export type RemoteNotificationsController = {
  entries: RemoteNotificationEntry[];
  status: RemoteNotificationsStatus;
  /** Marks one entry read locally and persists it on the local backend. */
  markRead: (id: string) => void;
  /** Dismisses one entry locally and persists it on the local backend. */
  dismiss: (id: string) => void;
  /** Clears every unread badge locally and persists the batch read. */
  markAllRead: () => void;
};

/**
 * Owns the module's REST read and writes: one load per app session
 * (flag-gated) against the local backend, then local-state-first mutations
 * whose REST persistence keeps the read/dismiss state across a reload. Live
 * frames arrive through the `remote_notification` WS handler, not this hook.
 */
export function useRemoteNotifications(): RemoteNotificationsController {
  const enabled = useLocalFeatureFlag("multiInstance");
  const entries = useAppStore((s) => s.remoteNotifications.entries);
  const status = useAppStore((s) => s.remoteNotifications.status);
  const storeApi = useAppStoreApi();

  useEffect(() => {
    if (!enabled) return;
    // Read the live status rather than the render snapshot: a StrictMode
    // double mount must not fire a second occurrence read.
    if (storeApi.getState().remoteNotifications.status !== "idle") return;
    void loadRemoteNotifications(storeApi.getState);
  }, [enabled, storeApi]);

  const markRead = useCallback(
    (id: string) => {
      storeApi.getState().markRemoteNotificationRead(id);
      void markRemoteNotificationRead(id).catch(() => undefined);
    },
    [storeApi],
  );

  const dismiss = useCallback(
    (id: string) => {
      storeApi.getState().markRemoteNotificationDismissed(id);
      void dismissRemoteNotification(id).catch(() => undefined);
    },
    [storeApi],
  );

  const markAllRead = useCallback(() => {
    storeApi.getState().markAllRemoteNotificationsRead();
    void markAllRemoteNotificationsRead().catch(() => undefined);
  }, [storeApi]);

  return { entries, status, markRead, dismiss, markAllRead };
}

/**
 * Per-instance unread counts for the instance picker's reserved badge slot.
 * Reads the same slice the module renders, so both surfaces badge the same
 * number.
 */
export function useRemoteNotificationUnreadByInstance(): Record<string, number> {
  return useAppStore(selectRemoteNotificationUnreadByInstance);
}

async function loadRemoteNotifications(getState: () => RemoteNotificationsSlice): Promise<void> {
  const {
    beginRemoteNotificationsLoad,
    setRemoteNotificationsLoaded,
    setRemoteNotificationsLoadError,
  } = getState();
  beginRemoteNotificationsLoad();
  try {
    setRemoteNotificationsLoaded(await listRemoteNotifications());
  } catch {
    // The module hides itself without rows; the regressed status lets the
    // next mount retry the read.
    setRemoteNotificationsLoadError();
  }
}
