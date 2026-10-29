import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  listRemoteNeedsYouInbox,
  listRemoteHiddenNeedsYouInbox,
  respondRemoteClarification,
  dismissRemoteClarificationBundle,
  snoozeRemoteClarificationBundle,
  restoreRemoteClarificationBundle,
  remoteClarificationRespondTarget,
} from "./remote-inbox-api";

vi.mock("@/lib/config", () => ({
  getBackendConfig: () => ({ apiBaseUrl: "https://active.test" }),
  getLocalBackendConfig: () => ({ apiBaseUrl: "https://local.test" }),
}));

const fetchMock = vi.fn();

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  fetchMock.mockReset();
  fetchMock.mockResolvedValue(jsonResponse({ instance_id: "i9", status: "ok", rows: [] }));
  globalThis.fetch = fetchMock as unknown as typeof globalThis.fetch;
});

describe("remote-inbox-api — local base resolution", () => {
  it("reads the merged instance page against the local backend base", async () => {
    await listRemoteNeedsYouInbox("i9");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url] = fetchMock.mock.calls[0];
    expect(String(url)).toBe("https://local.test/api/v1/instances/i9/needs-you-inbox");
  });

  it("reads the merged hidden page against the local backend base", async () => {
    await listRemoteHiddenNeedsYouInbox("i9");
    const [url] = fetchMock.mock.calls[0];
    expect(String(url)).toBe("https://local.test/api/v1/instances/i9/needs-you-inbox/hidden");
  });

  it("an explicit caller baseUrl still wins", async () => {
    await listRemoteNeedsYouInbox("i9", { baseUrl: "https://explicit.test" });
    const [url] = fetchMock.mock.calls[0];
    expect(String(url)).toBe("https://explicit.test/api/v1/instances/i9/needs-you-inbox");
  });

  it("encodes instance identifiers into the path", async () => {
    await listRemoteNeedsYouInbox("inst /1");
    const [url] = fetchMock.mock.calls[0];
    expect(String(url)).toBe("https://local.test/api/v1/instances/inst%20%2F1/needs-you-inbox");
  });
});

describe("remote-inbox-api — forwarding writes", () => {
  it("posts the respond body verbatim through the forward route", async () => {
    await respondRemoteClarification("i9", "p1", {
      answers: [{ question_id: "q1", selected_options: ["o1"] }],
      rejected: false,
    });
    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toBe("https://local.test/api/v1/instances/i9/clarification/p1/respond");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({
      answers: [{ question_id: "q1", selected_options: ["o1"] }],
      rejected: false,
    });
  });

  it("forwards a dismiss with the sidecar body", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await dismissRemoteClarificationBundle("i9", "p1");
    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toBe(
      "https://local.test/api/v1/instances/i9/clarification-inbox/sidecar/p1",
    );
    expect(init.method).toBe("PUT");
    expect(JSON.parse(String(init.body))).toEqual({ state: "dismissed" });
  });

  it("forwards a snooze with the chosen duration", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await snoozeRemoteClarificationBundle("i9", "p1", "1h");
    const [, init] = fetchMock.mock.calls[0];
    expect(init.method).toBe("PUT");
    expect(JSON.parse(String(init.body))).toEqual({ state: "snoozed", snooze_duration: "1h" });
  });

  it("forwards a restore as a sidecar delete", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await restoreRemoteClarificationBundle("i9", "p1");
    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toBe(
      "https://local.test/api/v1/instances/i9/clarification-inbox/sidecar/p1",
    );
    expect(init.method).toBe("DELETE");
  });
});

describe("remote-inbox-api — respond target builder", () => {
  it("builds the hook's respondTarget against the local base", () => {
    const target = remoteClarificationRespondTarget("i9");
    expect(target("p1")).toBe("https://local.test/api/v1/instances/i9/clarification/p1/respond");
    expect(target("p 2")).toBe(
      "https://local.test/api/v1/instances/i9/clarification/p%202/respond",
    );
  });
});
