import type { AppState } from "@/lib/state/store";
import type { RemoteNotificationEntry } from "./types";

/** The slice state these selectors read; nothing else in AppState is consulted. */
type SelectorState = Pick<AppState, "remoteNotifications">;

const EMPTY_COUNTS: Record<string, number> = {};

function isUnread(entry: RemoteNotificationEntry): boolean {
  return !entry.read && !entry.dismissed;
}

/**
 * Unread entry counts keyed by instance id, so the instance picker and the
 * module's per-instance group headers badge the same number. Read and
 * dismissed rows never count.
 */
export function selectRemoteNotificationUnreadByInstance(
  state: SelectorState,
): Record<string, number> {
  const counts: Record<string, number> = {};
  let counted = false;
  for (const entry of state.remoteNotifications.entries) {
    if (!isUnread(entry)) continue;
    counts[entry.instanceId] = (counts[entry.instanceId] ?? 0) + 1;
    counted = true;
  }
  // A stable empty object keeps reference-equality consumers from re-rendering
  // on every store tick while nothing is unread.
  return counted ? counts : EMPTY_COUNTS;
}

/** The module-level badge count: total unread entries across every instance. */
export function selectRemoteNotificationUnreadCount(state: SelectorState): number {
  return state.remoteNotifications.entries.filter(isUnread).length;
}
