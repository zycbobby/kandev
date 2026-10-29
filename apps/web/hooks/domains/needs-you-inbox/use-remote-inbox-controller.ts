import { useCallback, useEffect, useMemo, useRef } from "react";
import { useAppStore, useAppStoreApi } from "@/components/state-provider";
import { useFeature } from "@/hooks/domains/features/use-feature";
import { useForegroundRefresh } from "@/hooks/use-foreground-refresh";
import { getWebSocketClient } from "@/lib/ws/connection";
import { listRemoteNeedsYouInbox } from "@/lib/api/domains/remote-inbox-api";
import { LOCAL_INSTANCE_ID } from "@/lib/state/slices/instances/types";
import {
  NOTIFICATION_EVENT_SESSION_CLARIFICATION_REQUESTED,
  NOTIFICATION_EVENT_TASK_MANUAL_STEP_ENTERED,
} from "@/lib/notifications/events";
import { NeedsYouInboxRefreshCoordinator } from "./needs-you-inbox-refresh-coordinator";

// Same bounded cadence and snooze-timer discipline as the local controller
// (needs-you-inbox design-02#Control-flow), applied per instance.
const PERIODIC_REFRESH_MS = 60_000;
const MIN_SNOOZE_RESCHEDULE_MS = 5_000;
const SNOOZE_RESCHEDULE_BUFFER_MS = 1_000;

/**
 * Owns every remote instance-page refresh trigger (cross-instance-inbox
 * design): mount / instance-registry change, relayed needs-you occurrences,
 * foreground resume, the bounded 60s re-read while visible, per-instance
 * manual retries, and each instance's snooze-expiry timer. One coordinator
 * per instance (scopeKey carries the instance id) keeps failure isolation
 * per remote.
 *
 * Gated to the local-instance view: while the panel views a remote instance,
 * that instance's own Inbox is the local page there, and this panel must not
 * fan out instance pages through a remote proxy base.
 */
export function useRemoteInboxController() {
  const needsYouInbox = useFeature("needsYouInbox");
  const multiInstance = useFeature("multiInstance");
  const activeId = useAppStore((s) => s.instances.activeId);
  const items = useAppStore((s) => s.instances.items);
  const remoteRefreshTick = useAppStore((s) => s.needsYouInbox.remoteRefreshTick);
  const perInstanceTicks = useAppStore((s) => s.needsYouInbox.remoteRefreshTickByInstanceId);
  const retryTicks = useAppStore((s) => s.needsYouInbox.remoteRetryTickByInstanceId);
  const remoteStates = useAppStore((s) => s.needsYouInbox.remoteByInstanceId);
  const storeApi = useAppStoreApi();

  const enabled = needsYouInbox && multiInstance && activeId === LOCAL_INSTANCE_ID;
  const instanceIds = useMemo(
    () =>
      enabled
        ? items.filter((item) => item.enabled && item.id !== LOCAL_INSTANCE_ID).map((i) => i.id)
        : [],
    [enabled, items],
  );
  const instanceIdsKey = instanceIds.join(",");

  const coordinatorsRef = useRef(new Map<string, NeedsYouInboxRefreshCoordinator>());
  const generationsRef = useRef(new Map<string, number>());

  const refreshInstance = useCallback((instanceId: string, manual = false) => {
    const coordinator = coordinatorsRef.current.get(instanceId);
    if (!coordinator) return Promise.resolve();
    return coordinator.refresh(manual);
  }, []);

  const refreshAll = useCallback(
    (manual = false) => {
      for (const instanceId of coordinatorsRef.current.keys()) {
        void refreshInstance(instanceId, manual);
      }
    },
    [refreshInstance],
  );

  // Coordinator reconciliation: build one coordinator per newly enabled
  // instance, dispose the removed ones, and read on mount / registry change.
  // Disposal on effect cleanup also aborts in-flight reads whose scope ended.
  useEffect(() => {
    storeApi.getState().retainRemoteInboxInstances(enabled ? instanceIds : []);
    if (!enabled) {
      disposeRemoteCoordinators(coordinatorsRef.current, generationsRef.current);
      return;
    }
    const coordinators = coordinatorsRef.current;
    const wanted = new Set(instanceIds);
    for (const [instanceId, coordinator] of coordinators) {
      if (wanted.has(instanceId)) continue;
      coordinator.dispose();
      coordinators.delete(instanceId);
      generationsRef.current.delete(instanceId);
    }
    for (const instanceId of instanceIds) {
      if (coordinators.has(instanceId)) continue;
      coordinators.set(
        instanceId,
        new NeedsYouInboxRefreshCoordinator(
          `remote-inbox|${instanceId}`,
          buildRemoteInstanceRead(instanceId, storeApi, generationsRef),
          buildRemoteInstanceErrorSink(instanceId, storeApi, generationsRef),
        ),
      );
    }
    refreshAll();
    return () => disposeRemoteCoordinators(coordinatorsRef.current, generationsRef.current);
  }, [enabled, instanceIdsKey, storeApi, refreshAll]);

  // Trigger: relayed needs-you occurrences (see useRelayedNeedsYouRefresh).
  useRelayedNeedsYouRefresh(enabled, storeApi);

  const lastGlobalTickRef = useRef(remoteRefreshTick);
  useEffect(() => {
    if (lastGlobalTickRef.current === remoteRefreshTick) return;
    lastGlobalTickRef.current = remoteRefreshTick;
    if (enabled) void refreshAll();
  }, [remoteRefreshTick, enabled, refreshAll]);

  // Per-instance tick followers: map identity changes on every bump, so each
  // effect run diffs the map against the last-seen tick per instance.
  useFollowRemoteTicks(perInstanceTicks, enabled, (instanceId) => refreshInstance(instanceId));
  useFollowRemoteTicks(retryTicks, enabled, (instanceId) => refreshInstance(instanceId, true));

  useForegroundRefresh(
    () => {
      refreshAll();
      return Promise.resolve();
    },
    enabled,
    `remote-inbox|${activeId}`,
  );

  // Trigger: bounded periodic re-read, only while the tab is visible.
  useEffect(() => {
    if (!enabled) return;
    const interval = window.setInterval(() => {
      if (document.visibilityState === "visible") void refreshAll();
    }, PERIODIC_REFRESH_MS);
    return () => window.clearInterval(interval);
  }, [enabled, refreshAll]);

  // Per-instance snooze-expiry timers, re-armed only when that instance's
  // carried expiry actually changes.
  useRemoteSnoozeTimers(enabled, remoteStates, refreshInstance);
}

function disposeRemoteCoordinators(
  coordinators: Map<string, NeedsYouInboxRefreshCoordinator>,
  generations: Map<string, number>,
) {
  for (const coordinator of coordinators.values()) coordinator.dispose();
  coordinators.clear();
  generations.clear();
}

/** Trigger: relayed needs-you occurrences arrive as shared notification
 *  frames carrying the origin instance; only a frame naming a registered
 *  instance bumps that instance's tick. The slice fence drops bumps for
 *  unknown instances naturally (no coordinator reads them), and a local
 *  event (no origin) bumps nothing here: the local controller owns that
 *  refresh. */
function useRelayedNeedsYouRefresh(enabled: boolean, storeApi: ReturnType<typeof useAppStoreApi>) {
  useEffect(() => {
    if (!enabled) return;
    const client = getWebSocketClient();
    if (!client) return;
    const bumpForOrigin = (message?: { payload?: { origin_instance_id?: string } }) => {
      const instanceId = message?.payload?.origin_instance_id;
      if (instanceId) storeApi.getState().bumpRemoteInboxRefreshTick(instanceId);
    };
    const offClarification = client.on(
      NOTIFICATION_EVENT_SESSION_CLARIFICATION_REQUESTED,
      bumpForOrigin,
    );
    const offManualStep = client.on(NOTIFICATION_EVENT_TASK_MANUAL_STEP_ENTERED, bumpForOrigin);
    return () => {
      offClarification();
      offManualStep();
    };
  }, [enabled, storeApi]);
}

function buildRemoteInstanceRead(
  instanceId: string,
  storeApi: ReturnType<typeof useAppStoreApi>,
  generationsRef: { current: Map<string, number> },
) {
  return async (signal: AbortSignal) => {
    const { beginRemoteInboxRead, setRemoteInboxPage, setRemoteInboxDegraded } =
      storeApi.getState();
    const generation = beginRemoteInboxRead(instanceId);
    generationsRef.current.set(instanceId, generation);
    // The merged needs-you forward read carries both row kinds, so one page
    // per instance covers questions and manual-step waiting rows alike.
    const page = await listRemoteNeedsYouInbox(instanceId, { init: { signal } });
    if (signal.aborted) return;
    if (page.status === "degraded") {
      // A degraded page is a successful read of instance-scoped state, not a
      // transport failure: it lands as state and never feeds the
      // coordinator's retry ladder.
      setRemoteInboxDegraded(instanceId, generation, page.degraded_reason ?? "error");
      return;
    }
    setRemoteInboxPage(instanceId, generation, {
      rows: page.rows,
      count: page.count,
      hiddenCount: page.hidden_count,
      nextSnoozeExpiry: page.next_snooze_expiry,
      hasMore: page.has_more,
    });
  };
}

function buildRemoteInstanceErrorSink(
  instanceId: string,
  storeApi: ReturnType<typeof useAppStoreApi>,
  generationsRef: { current: Map<string, number> },
) {
  return () => {
    storeApi
      .getState()
      .setRemoteInboxDegraded(instanceId, generationsRef.current.get(instanceId) ?? 0, "error");
  };
}

/** Follows one per-instance tick map, invoking `fire` for each moved tick. */
function useFollowRemoteTicks(
  ticks: Record<string, number>,
  enabled: boolean,
  fire: (instanceId: string) => void,
) {
  const lastTicksRef = useRef(new Map<string, number>());
  useEffect(() => {
    for (const [instanceId, tick] of Object.entries(ticks ?? {})) {
      if (lastTicksRef.current.get(instanceId) === tick) continue;
      lastTicksRef.current.set(instanceId, tick);
      if (enabled) fire(instanceId);
    }
  }, [ticks, enabled, fire]);
}

type SnoozeTimer = { expiry: string; timer: number };

/** Arms one re-read timer per instance whose page carries a snooze expiry,
 * re-arming only when that instance's expiry value changes. */
function useRemoteSnoozeTimers(
  enabled: boolean,
  remoteStates: Record<
    string,
    { status: string; instanceId: string; nextSnoozeExpiry: string | null }
  >,
  refreshInstance: (instanceId: string) => void,
) {
  const timersRef = useRef(new Map<string, SnoozeTimer>());
  useEffect(() => {
    if (!enabled) return;
    const timers = timersRef.current;
    const wanted = new Set<string>();
    for (const instance of Object.values(remoteStates ?? {})) {
      if (instance.status !== "ready" || !instance.nextSnoozeExpiry) continue;
      wanted.add(instance.instanceId);
      const existing = timers.get(instance.instanceId);
      if (existing && existing.expiry === instance.nextSnoozeExpiry) continue;
      if (existing) window.clearTimeout(existing.timer);
      const delay = Math.max(
        new Date(instance.nextSnoozeExpiry).getTime() - Date.now() + SNOOZE_RESCHEDULE_BUFFER_MS,
        MIN_SNOOZE_RESCHEDULE_MS,
      );
      const timer = window.setTimeout(() => {
        timers.delete(instance.instanceId);
        refreshInstance(instance.instanceId);
      }, delay);
      timers.set(instance.instanceId, { expiry: instance.nextSnoozeExpiry, timer });
    }
    for (const [instanceId, entry] of timers) {
      if (wanted.has(instanceId)) continue;
      window.clearTimeout(entry.timer);
      timers.delete(instanceId);
    }
    return () => {
      for (const entry of timers.values()) window.clearTimeout(entry.timer);
      timers.clear();
    };
  }, [enabled, remoteStates, refreshInstance]);
}
