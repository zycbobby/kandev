import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { StateProvider, useAppStoreApi } from "@/components/state-provider";
import { defaultFeatureFlags } from "@/lib/state/slices/features/types";
import type { HydrationState } from "@/lib/state/store";
import type { RemoteInboxPage } from "@/lib/types/remote-inbox";

const listRemoteClarificationInboxMock = vi.fn();
vi.mock("@/lib/api/domains/remote-inbox-api", async (importOriginal) => {
  const original = await importOriginal<typeof import("@/lib/api/domains/remote-inbox-api")>();
  return {
    ...original,
    listRemoteClarificationInbox: (...args: unknown[]) => listRemoteClarificationInboxMock(...args),
  };
});

type WsHandler = (message?: { payload?: { instance_id: string; event: string } }) => void;
const wsMocks = vi.hoisted(() => ({
  handlers: new Map<string, Set<WsHandler>>(),
}));

function emitWsRemoteNotification(instanceId: string, event: string) {
  for (const handler of wsMocks.handlers.get("remote_notification") ?? []) {
    handler({ payload: { instance_id: instanceId, event } });
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

function remotePage(overrides: Partial<RemoteInboxPage> = {}): RemoteInboxPage {
  return {
    instance_id: "i1",
    instance_name: "Remote A",
    status: "ok",
    bundles: [],
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
  listRemoteClarificationInboxMock.mockReset();
  listRemoteClarificationInboxMock.mockResolvedValue(remotePage());
  wsMocks.handlers.clear();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe("useRemoteInboxController — gating", () => {
  it("reads one page per enabled instance on mount", async () => {
    renderController();

    await waitFor(() => expect(listRemoteClarificationInboxMock).toHaveBeenCalledTimes(2));
    expect(listRemoteClarificationInboxMock).toHaveBeenCalledWith("i1", expect.anything());
    expect(listRemoteClarificationInboxMock).toHaveBeenCalledWith("i2", expect.anything());
  });

  it("issues no reads while viewing a remote instance", async () => {
    renderController({ activeInstanceId: "i1" });

    await act(async () => {
      await Promise.resolve();
    });
    expect(listRemoteClarificationInboxMock).not.toHaveBeenCalled();
  });

  it("issues no reads when either flag is off", async () => {
    renderController({ needsYouInbox: false });
    await act(async () => {
      await Promise.resolve();
    });
    expect(listRemoteClarificationInboxMock).not.toHaveBeenCalled();

    cleanup();
    listRemoteClarificationInboxMock.mockClear();

    renderController({ multiInstance: false });
    await act(async () => {
      await Promise.resolve();
    });
    expect(listRemoteClarificationInboxMock).not.toHaveBeenCalled();
  });

  it("skips disabled instances", async () => {
    renderController({ instances: [{ id: "i1", enabled: false }] });

    await act(async () => {
      await Promise.resolve();
    });
    expect(listRemoteClarificationInboxMock).not.toHaveBeenCalled();
  });
});

describe("useRemoteInboxController — trigger set", () => {
  it("applies an ok page and a degraded page into the slice", async () => {
    listRemoteClarificationInboxMock.mockImplementation(async (instanceId: string) => {
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

  it("a clarification relay bump refreshes only the named instance", async () => {
    vi.useFakeTimers();
    const { result } = renderController();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(listRemoteClarificationInboxMock).toHaveBeenCalledTimes(2);

    act(() => {
      emitWsRemoteNotification("i2", "session.clarification_requested");
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });

    expect(listRemoteClarificationInboxMock).toHaveBeenCalledTimes(3);
    expect(listRemoteClarificationInboxMock).toHaveBeenLastCalledWith("i2", expect.anything());

    // A non-clarification relay event bumps nothing.
    act(() => {
      emitWsRemoteNotification("i1", "session.turn_finished");
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(listRemoteClarificationInboxMock).toHaveBeenCalledTimes(3);
    void result;
  });

  it("a retry tick manually refreshes only that instance", async () => {
    vi.useFakeTimers();
    const { result } = renderController();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(listRemoteClarificationInboxMock).toHaveBeenCalledTimes(2);

    act(() => {
      result.current.getState().bumpRemoteInboxRetryTick("i1");
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(listRemoteClarificationInboxMock).toHaveBeenCalledTimes(3);
    expect(listRemoteClarificationInboxMock).toHaveBeenLastCalledWith("i1", expect.anything());
  });

  it("a slow read for one instance never writes another instance's state", async () => {
    const slowRead = deferred<RemoteInboxPage>();
    listRemoteClarificationInboxMock.mockImplementation((instanceId: string) =>
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
