import type { AppState } from "@/lib/state/store";
import type { RemoteNotificationEntry } from "./types";

/** The slice state these selectors read; nothing else in AppState is consulted. */
type SelectorState = Pick<AppState, "remoteNotifications">;

const EMPTY_COUNTS: Record<string, number> = {};

// Snapshot cache for the derived counts. Subscribers bind this selector
// directly through useSyncExternalStore, which requires getSnapshot to keep
// returning the same reference until the underlying state changes — a fresh
// object per call re-renders subscribers in a loop (React error #185). The
// cache is keyed on the entries array identity, which immer replaces on every
// mutation, so it can never outlive its input.
let cachedEntries: readonly RemoteNotificationEntry[] | null = null;
let cachedCounts: Record<string, number> = EMPTY_COUNTS;

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
  const entries = state.remoteNotifications.entries;
  if (entries === cachedEntries) return cachedCounts;
  const counts: Record<string, number> = {};
  let counted = false;
  for (const entry of entries) {
    if (!isUnread(entry)) continue;
    counts[entry.instanceId] = (counts[entry.instanceId] ?? 0) + 1;
    counted = true;
  }
  cachedEntries = entries;
  cachedCounts = counted ? counts : EMPTY_COUNTS;
  return cachedCounts;
}

/** The module-level badge count: total unread entries across every instance. */
export function selectRemoteNotificationUnreadCount(state: SelectorState): number {
  return state.remoteNotifications.entries.filter(isUnread).length;
}
