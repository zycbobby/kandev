"use client";

import { useEffect } from "react";
import { useAppStore, useAppStoreApi } from "@/components/state-provider";
import { useLocalFeatureFlag } from "@/hooks/domains/features/use-local-feature-flag";
import { getInstanceHealth, listInstances } from "@/lib/api/domains/instances-api";
import type { InstancesSlice, InstancesSliceState } from "@/lib/state/slices/instances/types";

/**
 * Owns the instance registry read for the sidebar picker: one load per app
 * session (flag-gated), then per-instance health reports. The registry and
 * health routes live on the local backend, so the api layer resolves them
 * against the local base even while the panel views a remote instance. The
 * backend poller keeps the reports fresh; a reload re-reads them.
 */
export function useInstances(): InstancesSliceState["instances"] {
  const enabled = useLocalFeatureFlag("multiInstance");
  const instances = useAppStore((s) => s.instances);
  const storeApi = useAppStoreApi();

  useEffect(() => {
    if (!enabled) return;
    // Read the live status rather than the render snapshot: a StrictMode
    // double mount must not fire a second registry read.
    const status = storeApi.getState().instances.status;
    if (status === "loading" || status === "ready") return;
    void loadInstances(storeApi.getState);
  }, [enabled, storeApi]);

  return instances;
}

async function loadInstances(getState: () => InstancesSlice) {
  const { beginInstancesLoad, setInstancesLoaded, setInstancesLoadError, setInstanceHealth } =
    getState();
  beginInstancesLoad();
  try {
    const items = await listInstances();
    setInstancesLoaded(items);
    await Promise.allSettled(
      items.map(async (item) => {
        try {
          setInstanceHealth(item.id, await getInstanceHealth(item.id));
        } catch {
          // One missing report leaves that row's dot "unknown"; the registry
          // read itself succeeded.
        }
      }),
    );
  } catch {
    setInstancesLoadError();
  }
}
