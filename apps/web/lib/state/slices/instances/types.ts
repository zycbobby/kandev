import type { InstanceHealth, RemoteInstance } from "@/lib/api/domains/instances-api";

/**
 * The id of the local (serving) instance. Registry ids are UUIDs, so the
 * sentinel never collides with a real row.
 */
export const LOCAL_INSTANCE_ID = "local";

/**
 * One picker row. The local instance is always first; its label renders via
 * i18n, so `name` stays empty for it.
 */
export type InstanceListItem = {
  id: string;
  name: string;
  enabled: boolean;
};

export type InstancesStatus = "idle" | "loading" | "ready" | "error";

export type InstancesSliceState = {
  instances: {
    /** The local instance first, then the owner's registered instances. */
    items: InstanceListItem[];
    /**
     * The instance this page load serves: 'local', or the shell-injected
     * remote id. Switching instances is a full page reload, so the value
     * never changes during a session.
     */
    activeId: string;
    /** Last health report per registry instance id. */
    health: Record<string, InstanceHealth>;
    status: InstancesStatus;
  };
};

export type InstancesSliceActions = {
  beginInstancesLoad: () => void;
  setInstancesLoaded: (items: RemoteInstance[]) => void;
  upsertInstance: (item: RemoteInstance) => void;
  removeInstance: (id: string) => void;
  setInstancesLoadError: () => void;
  setInstanceHealth: (id: string, health: InstanceHealth) => void;
};

export type InstancesSlice = InstancesSliceState & InstancesSliceActions;
