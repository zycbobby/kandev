import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// The panel's data base points at the selected instance's proxy port while
// the remote-notification surface stays on the local backend, so the two
// resolutions diverge.
const bases = vi.hoisted(() => ({ proxy: "http://proxy.test:41234", local: "http://api.test" }));

vi.mock("@/lib/config", () => ({
  getBackendConfig: () => ({ apiBaseUrl: bases.proxy }),
  getLocalBackendConfig: () => ({ apiBaseUrl: bases.local }),
}));

import {
  dismissRemoteNotification,
  listRemoteNotifications,
  markAllRemoteNotificationsRead,
  markRemoteNotificationRead,
} from "./remote-notifications-api";
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

const BASE = "http://api.test/api/v1/remote-notifications";

function occurrenceRow(overrides: Record<string, unknown> = {}) {
  return {
    id: "6f9c6a20-9654-5a1e-9e11-43ad6bee6e12",
    instance_id: "inst-1",
    event: "session.turn_finished",
    occurrence_id: "occ-1",
    task_id: "task-9",
    session_id: "session-9",
    title: "Turn finished",
    body: "The agent finished a turn.",
    created_at: "2026-10-28T00:00:00Z",
    read: false,
    dismissed: false,
    ...overrides,
  };
}

describe("listRemoteNotifications", () => {
  it("GETs the occurrence list against the local backend base", async () => {
    fetchSpy.mockResolvedValueOnce(jsonResponse([occurrenceRow()]));
    await listRemoteNotifications();

    expect(lastCall().url).toBe(BASE);
    expect(lastCall().init?.method).toBeUndefined();
  });

  it("returns the parsed occurrence rows unchanged", async () => {
    const row = occurrenceRow({ read: true });
    fetchSpy.mockResolvedValueOnce(jsonResponse([row]));

    const rows = await listRemoteNotifications();

    expect(rows).toEqual([row]);
  });

  it("honors an explicit caller baseUrl over the local base", async () => {
    fetchSpy.mockResolvedValueOnce(jsonResponse([]));

    await listRemoteNotifications({ baseUrl: bases.proxy });

    expect(lastCall().url).toBe(`${bases.proxy}/api/v1/remote-notifications`);
  });

  it("propagates a 404 as an ApiError", async () => {
    fetchSpy.mockResolvedValueOnce(
      jsonResponse({ error: "remote notification operation failed" }, 404),
    );

    const error = await listRemoteNotifications().catch((thrown: unknown) => thrown);

    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(404);
  });
});

describe("markRemoteNotificationRead", () => {
  it("POSTs the read action scoped to the occurrence id", async () => {
    fetchSpy.mockResolvedValueOnce(jsonResponse({ success: true }));

    await markRemoteNotificationRead("6f9c6a20");

    expect(lastCall().url).toBe(`${BASE}/6f9c6a20/read`);
    expect(lastCall().init?.method).toBe("POST");
  });
});

describe("dismissRemoteNotification", () => {
  it("POSTs the dismiss action scoped to the occurrence id", async () => {
    fetchSpy.mockResolvedValueOnce(jsonResponse({ success: true }));

    await dismissRemoteNotification("6f9c6a20");

    expect(lastCall().url).toBe(`${BASE}/6f9c6a20/dismiss`);
    expect(lastCall().init?.method).toBe("POST");
  });
});

describe("markAllRemoteNotificationsRead", () => {
  it("POSTs the read-all action against the collection root", async () => {
    fetchSpy.mockResolvedValueOnce(jsonResponse({ success: true }));

    await markAllRemoteNotificationsRead();

    expect(lastCall().url).toBe(`${BASE}/read-all`);
    expect(lastCall().init?.method).toBe("POST");
  });
});
