import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// The panel's data base points at the selected instance's proxy port while
// the registry stays on the local backend, so the two resolutions diverge.
const bases = vi.hoisted(() => ({ proxy: "http://proxy.test:41234", local: "http://api.test" }));

vi.mock("@/lib/config", () => ({
  getBackendConfig: () => ({ apiBaseUrl: bases.proxy }),
  getLocalBackendConfig: () => ({ apiBaseUrl: bases.local }),
}));

import {
  createInstance,
  deleteInstance,
  getInstanceHealth,
  instanceHealthCheckedAt,
  listInstances,
  selectInstance,
  updateInstance,
} from "./instances-api";
import { ApiError } from "../client";

type FetchInput = Parameters<typeof fetch>[0];
type FetchInit = Parameters<typeof fetch>[1];

const fetchSpy = vi.fn<(...args: [FetchInput, FetchInit?]) => Promise<Response>>();

beforeEach(() => {
  fetchSpy.mockReset();
  vi.stubGlobal("fetch", fetchSpy);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function lastCall(): { url: string; init: FetchInit | undefined } {
  const call = fetchSpy.mock.calls.at(-1);
  if (!call) throw new Error("expected fetch to have been called");
  return { url: String(call[0]), init: call[1] };
}

const BASE = "http://api.test/api/v1/instances";
const CHECKED_AT = "2026-10-20T10:00:00Z";
const INSTANCE_BASE_URL = "http://192.168.1.20:43110";

function instanceRow(overrides: Record<string, unknown> = {}) {
  return {
    id: "inst-1",
    name: "Studio workstation",
    base_url: INSTANCE_BASE_URL,
    credential_secret_id: "sec-1",
    enabled: true,
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
    ...overrides,
  };
}

describe("listInstances", () => {
  it("GETs the registry and returns the parsed rows", async () => {
    fetchSpy.mockResolvedValueOnce(jsonResponse([instanceRow()]));

    const items = await listInstances();

    expect(lastCall().url).toBe(BASE);
    expect(lastCall().init?.method).toBeUndefined();
    expect(items).toHaveLength(1);
    expect(items[0]).toMatchObject({ id: "inst-1", base_url: INSTANCE_BASE_URL });
  });

  it("preserves an empty registry as an empty list", async () => {
    fetchSpy.mockResolvedValueOnce(jsonResponse([]));

    await expect(listInstances()).resolves.toEqual([]);
  });
});

describe("createInstance", () => {
  it("POSTs the payload to the registry", async () => {
    fetchSpy.mockResolvedValueOnce(jsonResponse(instanceRow(), 201));

    await createInstance({
      name: "Studio workstation",
      base_url: INSTANCE_BASE_URL,
      credential_secret_id: "sec-1",
      enabled: true,
    });

    const { url, init } = lastCall();
    expect(url).toBe(BASE);
    expect(init?.method).toBe("POST");
    expect(JSON.parse(String(init?.body))).toEqual({
      name: "Studio workstation",
      base_url: INSTANCE_BASE_URL,
      credential_secret_id: "sec-1",
      enabled: true,
    });
  });

  it("propagates a 400 blank-name rejection as an ApiError", async () => {
    fetchSpy.mockResolvedValueOnce(
      jsonResponse({ error: "instance name and base URL are required" }, 400),
    );

    const error = await createInstance({ name: " ", base_url: "http://x" }).catch(
      (thrown: unknown) => thrown,
    );

    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(400);
    expect((error as ApiError).message).toBe("instance name and base URL are required");
  });
});

describe("updateInstance", () => {
  it("PATCHes the partial body scoped to the instance id", async () => {
    fetchSpy.mockResolvedValueOnce(jsonResponse(instanceRow({ enabled: false })));

    await updateInstance("inst-1", { enabled: false });

    const { url, init } = lastCall();
    expect(url).toBe(`${BASE}/inst-1`);
    expect(init?.method).toBe("PATCH");
    expect(JSON.parse(String(init?.body))).toEqual({ enabled: false });
  });

  it("propagates a foreign/missing id 404 as an indistinguishable ApiError", async () => {
    fetchSpy.mockResolvedValueOnce(jsonResponse({ error: "instance not found" }, 404));

    const error = await updateInstance("gone", { name: "x" }).catch((thrown: unknown) => thrown);

    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(404);
    expect((error as ApiError).message).toBe("instance not found");
  });
});

describe("deleteInstance", () => {
  it("DELETEs the instance and returns the success envelope", async () => {
    fetchSpy.mockResolvedValueOnce(jsonResponse({ success: true }));

    await expect(deleteInstance("inst-1")).resolves.toEqual({ success: true });

    const { url, init } = lastCall();
    expect(url).toBe(`${BASE}/inst-1`);
    expect(init?.method).toBe("DELETE");
  });
});

describe("getInstanceHealth", () => {
  it("GETs the instance health report", async () => {
    fetchSpy.mockResolvedValueOnce(
      jsonResponse({
        reachable: true,
        version: "1.2.3",
        supported: true,
        lastCheckedAt: "2026-10-20T10:00:00Z",
      }),
    );

    const health = await getInstanceHealth("inst-1");

    expect(lastCall().url).toBe(`${BASE}/inst-1/health`);
    expect(health).toMatchObject({ reachable: true, version: "1.2.3", supported: true });
  });
});

describe("selectInstance", () => {
  it("POSTs the select action and returns the reload target", async () => {
    fetchSpy.mockResolvedValueOnce(jsonResponse({ href: "/" }));

    await expect(selectInstance("inst-1")).resolves.toEqual({ href: "/" });

    const { url, init } = lastCall();
    expect(url).toBe(`${BASE}/inst-1/select`);
    expect(init?.method).toBe("POST");
  });
});

describe("local-base resolution", () => {
  it("resolves registry reads against the local backend while the panel views a remote proxy", async () => {
    fetchSpy.mockResolvedValueOnce(jsonResponse([]));
    fetchSpy.mockResolvedValueOnce(jsonResponse({ href: "/" }));
    fetchSpy.mockResolvedValueOnce(
      jsonResponse({ reachable: true, supported: true, lastCheckedAt: CHECKED_AT }),
    );

    await listInstances();
    await selectInstance("inst-1");
    await getInstanceHealth("inst-1");

    expect(fetchSpy.mock.calls.map((call) => String(call[0]))).toEqual([
      `${bases.local}/api/v1/instances`,
      `${bases.local}/api/v1/instances/inst-1/select`,
      `${bases.local}/api/v1/instances/inst-1/health`,
    ]);
    expect(fetchSpy.mock.calls.some((call) => String(call[0]).startsWith(bases.proxy))).toBe(false);
  });

  it("lets an explicit caller baseUrl still win", async () => {
    fetchSpy.mockResolvedValueOnce(jsonResponse([]));

    await listInstances({ baseUrl: "http://explicit.test" });

    expect(lastCall().url).toBe("http://explicit.test/api/v1/instances");
  });
});

describe("instanceHealthCheckedAt", () => {
  it("treats the Go zero time as never polled", () => {
    expect(
      instanceHealthCheckedAt({
        reachable: false,
        supported: false,
        lastCheckedAt: "0001-01-01T00:00:00Z",
      }),
    ).toBeNull();
  });

  it("returns the checked instant for a real poll", () => {
    const checked = instanceHealthCheckedAt({
      reachable: true,
      supported: true,
      lastCheckedAt: "2026-10-20T10:00:00Z",
    });
    expect(checked?.toISOString()).toBe("2026-10-20T10:00:00.000Z");
  });

  it("returns null for missing or malformed values", () => {
    expect(
      instanceHealthCheckedAt({ reachable: false, supported: false, lastCheckedAt: "" }),
    ).toBeNull();
    expect(
      instanceHealthCheckedAt({ reachable: false, supported: false, lastCheckedAt: "not-a-date" }),
    ).toBeNull();
  });
});
