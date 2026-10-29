import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { StateProvider, useAppStoreApi } from "@/components/state-provider";
import { defaultFeatureFlags } from "@/lib/state/slices/features/types";
import type { HydrationState } from "@/lib/state/store";
import type { RemoteNeedsYouPage } from "@/lib/types/remote-inbox";

const listRemoteNeedsYouInboxMock = vi.fn();
vi.mock("@/lib/api/domains/remote-inbox-api", async (importOriginal) => {
  const original = await importOriginal<typeof import("@/lib/api/domains/remote-inbox-api")>();
  return {
    ...original,
    listRemoteNeedsYouInbox: (...args: unknown[]) => listRemoteNeedsYouInboxMock(...args),
  };
});

type WsHandler = (message?: { payload?: { origin_instance_id?: string } }) => void;
const wsMocks = vi.hoisted(() => ({
  handlers: new Map<string, Set<WsHandler>>(),
}));

/** Emits one shared notification frame, as the local backend's unified
 *  notification pipeline broadcasts it for a relayed occurrence. */
function emitNotificationFrame(action: string, originInstanceId?: string) {
  for (const handler of wsMocks.handlers.get(action) ?? []) {
    handler({ payload: { origin_instance_id: originInstanceId } });
  }
}

vi.mock("@/lib/ws/connection", () => ({
  getWebSocketClient: () => ({
    on: (type: string, handler: WsHandler) => {
      const handlers = wsMocks.handlers.get(type) ?? new Set<WsHandler>();
      handlers.add(handler);
      wsMocks.handlers.set(type, handlers);
      return () => handlers.delete(handler);
    },
  }),
}));

import { useRemoteInboxController } from "./use-remote-inbox-controller";

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((innerResolve) => {
    resolve = innerResolve;
  });
  return { promise, resolve };
}

function remotePage(overrides: Partial<RemoteNeedsYouPage> = {}): RemoteNeedsYouPage {
  return {
    instance_id: "i1",
    instance_name: "Remote A",
    status: "ok",
    rows: [],
    count: 0,
    hidden_count: 0,
    next_snooze_expiry: null,
    has_more: false,
    ...overrides,
  };
}

type RenderOptions = {
  needsYouInbox?: boolean;
  multiInstance?: boolean;
  activeInstanceId?: string;
  instances?: Array<{ id: string; enabled: boolean }>;
};

// The instances slice is not boot-hydrated: the registry loads through
// setInstancesLoaded exactly as the production REST flow does, and the
// active-instance view is overridden via setState where a test needs it.
function loadInstances(
  storeApi: ReturnType<typeof useAppStoreApi>,
  instances: Array<{ id: string; enabled: boolean }>,
) {
  storeApi.getState().setInstancesLoaded(
    instances.map((instance) => ({
      id: instance.id,
      name: `Instance ${instance.id}`,
      base_url: `http://${instance.id}.test`,
      enabled: instance.enabled,
      created_at: "2026-10-28T00:00:00Z",
      updated_at: "2026-10-28T00:00:00Z",
    })),
  );
}

function renderController(options: RenderOptions = {}) {
  const rendered = renderHook(
    () => {
      useRemoteInboxController();
      return useAppStoreApi();
    },
    {
      wrapper: ({ children }) => (
        <StateProvider
          initialState={
            {
              features: {
                ...defaultFeatureFlags,
                needsYouInbox: options.needsYouInbox ?? true,
                multiInstance: options.multiInstance ?? true,
              },
            } as unknown as HydrationState
          }
        >
          {children}
        </StateProvider>
      ),
    },
  );
  act(() => {
    if (options.activeInstanceId) {
      rendered.result.current.setState((state) => ({
        ...state,
        instances: { ...state.instances, activeId: options.activeInstanceId! },
      }));
    }
    loadInstances(
      rendered.result.current,
      options.instances ?? [
        { id: "i1", enabled: true },
        { id: "i2", enabled: true },
      ],
    );
  });
  return rendered;
}

beforeEach(() => {
  listRemoteNeedsYouInboxMock.mockReset();
  listRemoteNeedsYouInboxMock.mockResolvedValue(remotePage());
  wsMocks.handlers.clear();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe("useRemoteInboxController — gating", () => {
  it("reads one page per enabled instance on mount", async () => {
    renderController();

    await waitFor(() => expect(listRemoteNeedsYouInboxMock).toHaveBeenCalledTimes(2));
    expect(listRemoteNeedsYouInboxMock).toHaveBeenCalledWith("i1", expect.anything());
    expect(listRemoteNeedsYouInboxMock).toHaveBeenCalledWith("i2", expect.anything());
  });

  it("issues no reads while viewing a remote instance", async () => {
    renderController({ activeInstanceId: "i1" });

    await act(async () => {
      await Promise.resolve();
    });
    expect(listRemoteNeedsYouInboxMock).not.toHaveBeenCalled();
  });

  it("issues no reads when either flag is off", async () => {
    renderController({ needsYouInbox: false });
    await act(async () => {
      await Promise.resolve();
    });
    expect(listRemoteNeedsYouInboxMock).not.toHaveBeenCalled();

    cleanup();
    listRemoteNeedsYouInboxMock.mockClear();

    renderController({ multiInstance: false });
    await act(async () => {
      await Promise.resolve();
    });
    expect(listRemoteNeedsYouInboxMock).not.toHaveBeenCalled();
  });

  it("skips disabled instances", async () => {
    renderController({ instances: [{ id: "i1", enabled: false }] });

    await act(async () => {
      await Promise.resolve();
    });
    expect(listRemoteNeedsYouInboxMock).not.toHaveBeenCalled();
  });

  it("removes disabled or deleted instances and ignores their late responses", async () => {
    const slowRead = deferred<RemoteNeedsYouPage>();
    listRemoteNeedsYouInboxMock.mockImplementation((id: string) =>
      id === "i1" ? slowRead.promise : Promise.resolve(remotePage({ instance_id: id, count: 1 })),
    );
    const { result } = renderController();
    await waitFor(() =>
      expect(result.current.getState().needsYouInbox.remoteByInstanceId.i2?.status).toBe("ready"),
    );
    act(() => loadInstances(result.current, [{ id: "i1", enabled: false }]));
    await waitFor(() =>
      expect(Object.keys(result.current.getState().needsYouInbox.remoteByInstanceId)).toHaveLength(
        0,
      ),
    );
    await act(async () => {
      slowRead.resolve(remotePage({ count: 3 }));
      await slowRead.promise;
    });
    expect(Object.keys(result.current.getState().needsYouInbox.remoteByInstanceId)).toHaveLength(0);
  });
});

describe("useRemoteInboxController — trigger set", () => {
  it("applies an ok page and a degraded page into the slice", async () => {
    listRemoteNeedsYouInboxMock.mockImplementation(async (instanceId: string) => {
      if (instanceId === "i2") {
        return remotePage({
          instance_id: "i2",
          status: "degraded",
          degraded_reason: "unauthorized",
        });
      }
      return remotePage({ instance_id: instanceId, count: 2 });
    });
    const { result } = renderController({
      instances: [
        { id: "i1", enabled: true },
        { id: "i2", enabled: true },
      ],
    });

    await waitFor(() => {
      const remote = result.current.getState().needsYouInbox.remoteByInstanceId;
      expect(remote.i1?.status).toBe("ready");
      expect(remote.i1?.count).toBe(2);
      expect(remote.i2?.status).toBe("error");
      expect(remote.i2?.degradedReason).toBe("unauthorized");
      expect(remote.i2?.count).toBe(0);
    });
  });
});

describe("useRemoteInboxController — merged page shape", () => {
  it("applies a merged page's question and waiting rows kind-tagged", async () => {
    listRemoteNeedsYouInboxMock.mockResolvedValue(
      remotePage({
        rows: [
          {
            kind: "question",
            pending_id: "p1",
            task_id: "t1",
            session_id: "s1",
            session_state: "WAITING_FOR_INPUT",
            task_title: "Remote question",
            created_at: "2026-10-28T10:00:00Z",
            context: "",
            messages: [],
            instance_id: "i1",
            workspace_id: "ws-r",
          },
          {
            kind: "waiting",
            id: "mw:t2:step-gate:7",
            task_id: "t2",
            task_title: "Remote parked task",
            workspace_id: "ws-r",
            step_id: "step-gate",
            step_name: "Manual gate",
            reference_time: "2026-10-28T09:00:00Z",
            instance_id: "i1",
          },
        ],
        count: 2,
        hidden_count: 1,
      }),
    );
    const { result } = renderController({ instances: [{ id: "i1", enabled: true }] });

    await waitFor(() => {
      expect(result.current.getState().needsYouInbox.remoteByInstanceId.i1?.status).toBe("ready");
    });
    const instance = result.current.getState().needsYouInbox.remoteByInstanceId.i1;
    expect(instance?.rows).toHaveLength(2);
    expect(instance?.rows[0]?.kind).toBe("question");
    expect(instance?.rows[1]?.kind).toBe("waiting");
    expect(instance?.count).toBe(2);
    expect(instance?.hiddenCount).toBe(1);
  });
});

describe("useRemoteInboxController — relayed refresh triggers", () => {
  it("a relayed needs-you frame refreshes only the named instance", async () => {
    vi.useFakeTimers();
    renderController();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(listRemoteNeedsYouInboxMock).toHaveBeenCalledTimes(2);

    act(() => {
      emitNotificationFrame("session.clarification_requested", "i2");
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });

    expect(listRemoteNeedsYouInboxMock).toHaveBeenCalledTimes(3);
    expect(listRemoteNeedsYouInboxMock).toHaveBeenLastCalledWith("i2", expect.anything());

    // The other needs-you kind refreshes its named instance too.
    act(() => {
      emitNotificationFrame("task.manual_step_entered", "i1");
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(listRemoteNeedsYouInboxMock).toHaveBeenCalledTimes(4);
    expect(listRemoteNeedsYouInboxMock).toHaveBeenLastCalledWith("i1", expect.anything());
  });

  it("non-needs-you, local, and unregistered-origin frames bump nothing", async () => {
    vi.useFakeTimers();
    renderController();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(listRemoteNeedsYouInboxMock).toHaveBeenCalledTimes(2);

    // An informational relayed kind leaves the Inbox read alone.
    act(() => {
      emitNotificationFrame("session.turn_finished", "i1");
    });
    // A local needs-you event (no origin instance) bumps nothing here: the
    // local controller owns that refresh.
    act(() => {
      emitNotificationFrame("session.clarification_requested");
    });
    // A frame naming an unregistered instance bumps nothing: the slice
    // fence drops ticks for instances no coordinator reads.
    act(() => {
      emitNotificationFrame("session.clarification_requested", "i9");
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(listRemoteNeedsYouInboxMock).toHaveBeenCalledTimes(2);
  });

  it("a retry tick manually refreshes only that instance", async () => {
    vi.useFakeTimers();
    const { result } = renderController();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(listRemoteNeedsYouInboxMock).toHaveBeenCalledTimes(2);

    act(() => {
      result.current.getState().bumpRemoteInboxRetryTick("i1");
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(listRemoteNeedsYouInboxMock).toHaveBeenCalledTimes(3);
    expect(listRemoteNeedsYouInboxMock).toHaveBeenLastCalledWith("i1", expect.anything());
  });

  it("a slow read for one instance never writes another instance's state", async () => {
    const slowRead = deferred<RemoteNeedsYouPage>();
    listRemoteNeedsYouInboxMock.mockImplementation((instanceId: string) =>
      instanceId === "i1" ? slowRead.promise : Promise.resolve(remotePage({ count: 7 })),
    );
    const { result } = renderController();

    await waitFor(() => {
      expect(result.current.getState().needsYouInbox.remoteByInstanceId.i2?.count).toBe(7);
      expect(result.current.getState().needsYouInbox.remoteByInstanceId.i2?.status).toBe("ready");
    });

    await act(async () => {
      slowRead.resolve(remotePage({ count: 3 }));
      await slowRead.promise;
    });
    expect(result.current.getState().needsYouInbox.remoteByInstanceId.i1?.count).toBe(3);
    expect(result.current.getState().needsYouInbox.remoteByInstanceId.i2?.count).toBe(7);
  });
});
