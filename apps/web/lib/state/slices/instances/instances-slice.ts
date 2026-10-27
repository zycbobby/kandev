import type { Draft } from "immer";
import type { RemoteInstance } from "@/lib/api/domains/instances-api";
import {
  LOCAL_INSTANCE_ID,
  type InstanceListItem,
  type InstancesSlice,
  type InstancesSliceState,
} from "./types";

/** The local instance's list entry; always the first row of `items`. */
export const localInstanceItem: InstanceListItem = {
  id: LOCAL_INSTANCE_ID,
  name: "",
  enabled: true,
};

/**
 * The active instance for this page load: the Go shell injects
 * window.__KANDEV_INSTANCE_ID before the SPA's modules evaluate, so reading
 * it while building the default state is correct. An absent value (local
 * boot) means 'local'.
 */
function initialActiveInstanceId(): string {
  if (typeof window !== "undefined" && window.__KANDEV_INSTANCE_ID) {
    return window.__KANDEV_INSTANCE_ID;
  }
  return LOCAL_INSTANCE_ID;
}

export const defaultInstancesState: InstancesSliceState = {
  instances: {
    items: [localInstanceItem],
    activeId: initialActiveInstanceId(),
    health: {},
    status: "idle",
  },
};

type ImmerSet = (updater: (draft: Draft<InstancesSlice>) => void) => void;

export const createInstancesSlice = (set: ImmerSet): InstancesSlice => ({
  ...defaultInstancesState,
  beginInstancesLoad: () =>
    set((draft) => {
      // A load already in flight (or a list already rendered) owns the state;
      // a second trigger, like a StrictMode double mount, must not regress it.
      if (draft.instances.status === "idle") draft.instances.status = "loading";
    }),
  setInstancesLoaded: (items) =>
    set((draft) => {
      draft.instances.items = [localInstanceItem, ...items.map(toListItem)];
      draft.instances.status = "ready";
    }),
  setInstancesLoadError: () =>
    set((draft) => {
      // Keep whatever list was last rendered; only the status regresses so
      // the next mount can retry.
      draft.instances.status = "error";
    }),
  setInstanceHealth: (id, health) =>
    set((draft) => {
      draft.instances.health[id] = health;
    }),
});

function toListItem(item: RemoteInstance): InstanceListItem {
  return { id: item.id, name: item.name, enabled: item.enabled };
}
