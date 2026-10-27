import { describe, expect, it, vi } from "vitest";
import { create } from "zustand";
import { immer } from "zustand/middleware/immer";
import type { InstanceHealth, RemoteInstance } from "@/lib/api/domains/instances-api";
import type { InstancesSlice } from "./types";
import { LOCAL_INSTANCE_ID } from "./types";
import { createInstancesSlice, defaultInstancesState, localInstanceItem } from "./instances-slice";

function makeStore() {
  return create<InstancesSlice>()(immer((set) => createInstancesSlice(set)));
}

function registryRow(overrides: Partial<RemoteInstance> = {}): RemoteInstance {
  return {
    id: "inst-1",
    name: "Studio workstation",
    base_url: "http://192.168.1.9:43110",
    enabled: true,
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
    ...overrides,
  };
}

const HEALTH: InstanceHealth = {
  reachable: true,
  version: "1.2.3",
  supported: true,
  lastCheckedAt: "2026-10-20T10:00:00Z",
};

describe("instances slice", () => {
  it("defaults to the local instance with no remote selection", () => {
    const store = makeStore();
    expect(store.getState().instances.items).toEqual([localInstanceItem]);
    expect(store.getState().instances.activeId).toBe(LOCAL_INSTANCE_ID);
    expect(store.getState().instances.health).toEqual({});
    expect(store.getState().instances.status).toBe("idle");
  });

  it("keeps the local instance first when the registry loads", () => {
    const store = makeStore();
    store
      .getState()
      .setInstancesLoaded([
        registryRow({ id: "inst-2", name: "Rack server" }),
        registryRow({ id: "inst-1" }),
      ]);
    const items = store.getState().instances.items;
    expect(items.map((item) => item.id)).toEqual([LOCAL_INSTANCE_ID, "inst-2", "inst-1"]);
    expect(items[1]).toMatchObject({ name: "Rack server", enabled: true });
    expect(store.getState().instances.status).toBe("ready");
  });

  it("records the last health report per instance", () => {
    const store = makeStore();
    store.getState().setInstanceHealth("inst-1", HEALTH);
    expect(store.getState().instances.health["inst-1"]).toEqual(HEALTH);
  });

  it("keeps the last rendered list when the registry read fails", () => {
    const store = makeStore();
    store.getState().setInstancesLoaded([registryRow()]);
    store.getState().setInstancesLoadError();
    expect(store.getState().instances.items).toHaveLength(2);
    expect(store.getState().instances.status).toBe("error");
  });

  it("does not reset a ready list when a load is triggered again", () => {
    const store = makeStore();
    store.getState().beginInstancesLoad();
    expect(store.getState().instances.status).toBe("loading");
    store.getState().setInstancesLoaded([registryRow()]);
    store.getState().beginInstancesLoad();
    expect(store.getState().instances.status).toBe("ready");
    expect(store.getState().instances.items).toHaveLength(2);
  });

  it("seeds the active instance from the shell-injected global", async () => {
    window.__KANDEV_INSTANCE_ID = "inst-9";
    try {
      vi.resetModules();
      const mod = await import("./instances-slice");
      const store = create<InstancesSlice>()(immer((set) => mod.createInstancesSlice(set)));
      expect(store.getState().instances.activeId).toBe("inst-9");
      expect(defaultInstancesState.instances.activeId).toBe(LOCAL_INSTANCE_ID);
    } finally {
      delete window.__KANDEV_INSTANCE_ID;
    }
  });
});
