import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { create } from "zustand";
import { immer } from "zustand/middleware/immer";
import type { StoreApi } from "zustand";
import type { InstanceHealth, RemoteInstance } from "@/lib/api/domains/instances-api";
import { createInstancesSlice } from "@/lib/state/slices/instances/instances-slice";
import type { InstancesSlice } from "@/lib/state/slices/instances/types";
import { useInstances } from "./use-instances";

const api = vi.hoisted(() => ({
  list: vi.fn(),
  health: vi.fn(),
}));

vi.mock("@/lib/api/domains/instances-api", () => ({
  listInstances: api.list,
  getInstanceHealth: api.health,
}));

const feature = vi.hoisted(() => ({ enabled: true }));

vi.mock("@/hooks/domains/features/use-feature", () => ({
  useFeature: (name: string) => name === "multiInstance" && feature.enabled,
}));

const holder: { store: StoreApi<InstancesSlice> | null } = vi.hoisted(() => ({ store: null }));

vi.mock("@/components/state-provider", () => ({
  useAppStore: (selector: (state: InstancesSlice) => unknown) => selector(holder.store!.getState()),
  useAppStoreApi: () => holder.store,
}));

function makeStore() {
  return create<InstancesSlice>()(immer((set) => createInstancesSlice(set)));
}

function row(id: string, overrides: Partial<RemoteInstance> = {}): RemoteInstance {
  return {
    id,
    name: `Instance ${id}`,
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

describe("useInstances", () => {
  beforeEach(() => {
    feature.enabled = true;
    holder.store = makeStore();
    api.list.mockReset();
    api.health.mockReset();
  });

  it("loads the registry and per-instance health once per session", async () => {
    api.list.mockResolvedValue([row("inst-1"), row("inst-2")]);
    api.health.mockResolvedValue(HEALTH);

    const { result } = renderHook(() => useInstances());
    expect(result.current.items).toEqual([{ id: "local", name: "", enabled: true }]);
    expect(result.current.activeId).toBe("local");

    await waitFor(() => expect(holder.store!.getState().instances.status).toBe("ready"));
    expect(api.list).toHaveBeenCalledTimes(1);
    expect(api.health).toHaveBeenCalledTimes(2);
    expect(holder.store!.getState().instances.items.map((item) => item.id)).toEqual([
      "local",
      "inst-1",
      "inst-2",
    ]);
    expect(holder.store!.getState().instances.health["inst-1"]).toEqual(HEALTH);
  });

  it("keeps the local-only list when the registry read fails", async () => {
    api.list.mockRejectedValue(new Error("offline"));

    renderHook(() => useInstances());

    await waitFor(() => expect(holder.store!.getState().instances.status).toBe("error"));
    expect(holder.store!.getState().instances.items).toHaveLength(1);
    expect(api.health).not.toHaveBeenCalled();
  });

  it("does not read the registry while the feature flag is off", () => {
    feature.enabled = false;

    renderHook(() => useInstances());

    expect(api.list).not.toHaveBeenCalled();
  });

  it("does not issue a second load for a second mounted consumer", () => {
    api.list.mockReturnValue(new Promise(() => {}));

    renderHook(() => useInstances());
    renderHook(() => useInstances());

    expect(api.list).toHaveBeenCalledTimes(1);
    expect(holder.store!.getState().instances.status).toBe("loading");
  });

  it("survives a failing health report without regressing the loaded list", async () => {
    api.list.mockResolvedValue([row("inst-1")]);
    api.health.mockRejectedValue(new Error("report unavailable"));

    renderHook(() => useInstances());

    await waitFor(() => expect(holder.store!.getState().instances.status).toBe("ready"));
    expect(holder.store!.getState().instances.items).toHaveLength(2);
    expect(holder.store!.getState().instances.health).toEqual({});
  });
});
