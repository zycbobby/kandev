import { describe, it, expect, vi, beforeEach } from "vitest";
import { listNeedsYouInbox, listHiddenNeedsYouInbox } from "./needs-you-inbox-api";

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
  fetchMock.mockResolvedValue(
    jsonResponse({
      rows: [],
      count: 0,
      hidden_count: 0,
      has_more: false,
      next_snooze_expiry: null,
    }),
  );
  globalThis.fetch = fetchMock as unknown as typeof globalThis.fetch;
});

describe("needs-you-inbox-api — merged read endpoints", () => {
  it("reads the merged page against the active backend base", async () => {
    await listNeedsYouInbox("w1");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url] = fetchMock.mock.calls[0];
    expect(String(url)).toBe("https://active.test/api/v1/needs-you-inbox?workspace_id=w1");
  });

  it("encodes the workspace id into the query", async () => {
    await listNeedsYouInbox("ws /1");
    const [url] = fetchMock.mock.calls[0];
    expect(String(url)).toBe("https://active.test/api/v1/needs-you-inbox?workspace_id=ws%20%2F1");
  });

  it("reads the merged hidden page against the hidden endpoint", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ rows: [], count: 0, total: 0 }));
    await listHiddenNeedsYouInbox("w1");
    const [url] = fetchMock.mock.calls[0];
    expect(String(url)).toBe("https://active.test/api/v1/needs-you-inbox/hidden?workspace_id=w1");
  });

  it("returns the merged envelope's rows union with kind discriminants", async () => {
    fetchMock.mockResolvedValue(
      jsonResponse({
        rows: [
          {
            kind: "question",
            pending_id: "p1",
            task_id: "t1",
            session_id: "s1",
            session_state: "WAITING_FOR_INPUT",
            task_title: "Task",
            created_at: "2026-09-03T04:54:02Z",
            context: "",
            messages: [],
          },
          {
            kind: "waiting",
            id: "mw:t2:step-manual:7",
            task_id: "t2",
            task_title: "Waiting task",
            workspace_id: "w1",
            step_id: "step-manual",
            step_name: "Manual review",
            reference_time: "2026-09-03T05:00:00Z",
          },
        ],
        count: 2,
        hidden_count: 1,
        has_more: false,
        next_snooze_expiry: null,
      }),
    );
    const page = await listNeedsYouInbox("w1");
    expect(page.rows).toHaveLength(2);
    expect(page.rows[0].kind).toBe("question");
    expect(page.rows[1].kind).toBe("waiting");
    expect(page.count).toBe(2);
    expect(page.hidden_count).toBe(1);
    expect(page.has_more).toBe(false);
    expect(page.next_snooze_expiry).toBeNull();
  });
});
