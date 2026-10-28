import { describe, expect, it } from "vitest";
import { create } from "zustand";
import { immer } from "zustand/middleware/immer";
import type { ClarificationInboxBundle } from "@/lib/types/clarification-inbox";
import { createNeedsYouInboxSlice } from "./needs-you-inbox-slice";
import type { NeedsYouInboxSlice } from "./types";

function newStore() {
  // `createNeedsYouInboxSlice` types `set` against the full `AppState` (the
  // zustand slices pattern, so composing it in store.ts needs no cast); this
  // isolated test store only has `NeedsYouInboxSlice`, so the two `set`
  // shapes need a cast here, in test-only code the store.ts architecture
  // rule does not cover.
  return create<NeedsYouInboxSlice>()(
    immer((set) =>
      createNeedsYouInboxSlice(set as unknown as Parameters<typeof createNeedsYouInboxSlice>[0]),
    ),
  );
}

function bundle(overrides: Partial<ClarificationInboxBundle> = {}): ClarificationInboxBundle {
  return {
    pending_id: "p1",
    task_id: "t1",
    session_id: "s1",
    session_state: "WAITING_FOR_INPUT",
    task_title: "Task",
    created_at: "2026-09-03T04:54:02Z",
    context: "",
    messages: [],
    ...overrides,
  };
}

describe("needs-you-inbox slice retry signals", () => {
  it("keeps explicit retry requests separate from WebSocket refresh ticks", () => {
    const store = newStore();

    store.getState().bumpNeedsYouInboxRefreshTick();
    store.getState().requestNeedsYouInboxRetry();

    expect(store.getState().needsYouInbox.refreshTick).toBe(1);
    expect(store.getState().needsYouInbox.manualRetryTick).toBe(1);
  });
});

describe("needs-you-inbox slice", () => {
  it("has no rows or count for a workspace before any read (AC .13: absent is not zero)", () => {
    const store = newStore();
    expect(store.getState().needsYouInbox.byWorkspaceId.w1).toBeUndefined();
  });

  it("applies a page read tagged with the current generation", () => {
    const store = newStore();
    const generation = store.getState().beginNeedsYouInboxRead("w1");

    store.getState().setNeedsYouInboxPage("w1", generation, {
      bundles: [bundle()],
      count: 1,
      hiddenCount: 2,
      nextSnoozeExpiry: "2026-09-03T05:00:00Z",
      hasMore: true,
    });

    const state = store.getState().needsYouInbox.byWorkspaceId.w1;
    expect(state.status).toBe("ready");
    expect(state.count).toBe(1);
    expect(state.bundles).toHaveLength(1);
    expect(state.hiddenCount).toBe(2);
    expect(state.hasMore).toBe(true);
    expect(state.nextSnoozeExpiry).toBe("2026-09-03T05:00:00Z");
  });

  // AC .38: a fast workspace switch or overlapping refresh trigger must not
  // let a slower, older response overwrite what a newer one already wrote.
  it("drops a page response whose generation was superseded by a newer read", () => {
    const store = newStore();
    const staleGeneration = store.getState().beginNeedsYouInboxRead("w1");
    const freshGeneration = store.getState().beginNeedsYouInboxRead("w1");
    expect(freshGeneration).not.toBe(staleGeneration);

    store.getState().setNeedsYouInboxPage("w1", freshGeneration, {
      bundles: [bundle({ pending_id: "fresh" })],
      count: 1,
      hiddenCount: 0,
      nextSnoozeExpiry: null,
      hasMore: false,
    });
    // The stale response lands after the fresh one and must be dropped.
    store.getState().setNeedsYouInboxPage("w1", staleGeneration, {
      bundles: [bundle({ pending_id: "stale" })],
      count: 1,
      hiddenCount: 0,
      nextSnoozeExpiry: null,
      hasMore: false,
    });

    const state = store.getState().needsYouInbox.byWorkspaceId.w1;
    expect(state.bundles[0].pending_id).toBe("fresh");
  });

  it("clears rows and count in the same update on a failed read that follows a successful one", () => {
    const store = newStore();
    const okGeneration = store.getState().beginNeedsYouInboxRead("w1");
    store.getState().setNeedsYouInboxPage("w1", okGeneration, {
      bundles: [bundle()],
      count: 1,
      hiddenCount: 0,
      nextSnoozeExpiry: null,
      hasMore: false,
    });

    const failGeneration = store.getState().beginNeedsYouInboxRead("w1");
    store.getState().setNeedsYouInboxError("w1", failGeneration);

    const state = store.getState().needsYouInbox.byWorkspaceId.w1;
    expect(state.status).toBe("error");
    expect(state.bundles).toHaveLength(0);
    expect(state.count).toBe(0);
  });

  // R2-F1: `resolveViewMode` needs to tell a refresh following success apart
  // from one following a failure, and `status` alone can't -- a refresh in
  // flight is "loading" either way, since `beginNeedsYouInboxRead` only ever
  // writes `status`. `lastAppliedOk` is the field that survives that
  // overwrite untouched.
  it("marks lastAppliedOk true after a successful page and false after a failed read, unaffected by the loading transition between them", () => {
    const store = newStore();
    const okGeneration = store.getState().beginNeedsYouInboxRead("w1");
    store.getState().setNeedsYouInboxPage("w1", okGeneration, {
      bundles: [bundle()],
      count: 1,
      hiddenCount: 0,
      nextSnoozeExpiry: null,
      hasMore: false,
    });
    expect(store.getState().needsYouInbox.byWorkspaceId.w1.lastAppliedOk).toBe(true);

    const failGeneration = store.getState().beginNeedsYouInboxRead("w1");
    // Mid-refresh: status flips to "loading" but the prior success must still
    // be visible until the read actually settles one way or the other.
    expect(store.getState().needsYouInbox.byWorkspaceId.w1.status).toBe("loading");
    expect(store.getState().needsYouInbox.byWorkspaceId.w1.lastAppliedOk).toBe(true);

    store.getState().setNeedsYouInboxError("w1", failGeneration);
    expect(store.getState().needsYouInbox.byWorkspaceId.w1.lastAppliedOk).toBe(false);

    // A refresh started after the failure is "loading" again, but now with no
    // successful page behind it -- this is the exact tuple `resolveViewMode`
    // must not render as a false "empty".
    store.getState().beginNeedsYouInboxRead("w1");
    const state = store.getState().needsYouInbox.byWorkspaceId.w1;
    expect(state.status).toBe("loading");
    expect(state.lastAppliedOk).toBe(false);
  });

  it("drops a stale error response the same way it drops a stale page response", () => {
    const store = newStore();
    const staleGeneration = store.getState().beginNeedsYouInboxRead("w1");
    const freshGeneration = store.getState().beginNeedsYouInboxRead("w1");
    store.getState().setNeedsYouInboxPage("w1", freshGeneration, {
      bundles: [bundle()],
      count: 1,
      hiddenCount: 0,
      nextSnoozeExpiry: null,
      hasMore: false,
    });

    store.getState().setNeedsYouInboxError("w1", staleGeneration);

    const state = store.getState().needsYouInbox.byWorkspaceId.w1;
    expect(state.status).toBe("ready");
    expect(state.count).toBe(1);
  });
});

describe("needs-you-inbox slice — boot seeding", () => {
  it("seeds boot state at generation 0 only when no real read has started yet", () => {
    const store = newStore();
    store.getState().seedNeedsYouInboxBoot("w1", {
      count: 3,
      hasMore: false,
      nextSnoozeExpiry: null,
    });

    expect(store.getState().needsYouInbox.byWorkspaceId.w1.count).toBe(3);
  });

  it("lets the first real read supersede a boot seed", () => {
    const store = newStore();
    store.getState().seedNeedsYouInboxBoot("w1", {
      count: 3,
      hasMore: false,
      nextSnoozeExpiry: null,
    });

    const generation = store.getState().beginNeedsYouInboxRead("w1");
    store.getState().setNeedsYouInboxPage("w1", generation, {
      bundles: [],
      count: 0,
      hiddenCount: 0,
      nextSnoozeExpiry: null,
      hasMore: false,
    });

    expect(store.getState().needsYouInbox.byWorkspaceId.w1.count).toBe(0);
    expect(store.getState().needsYouInbox.byWorkspaceId.w1.status).toBe("ready");
  });

  it("never lets a boot seed overwrite a read already in flight", () => {
    const store = newStore();
    store.getState().beginNeedsYouInboxRead("w1");
    store.getState().seedNeedsYouInboxBoot("w1", {
      count: 99,
      hasMore: false,
      nextSnoozeExpiry: null,
    });

    // The seed must not have written anything once a read generation exists.
    expect(store.getState().needsYouInbox.byWorkspaceId.w1.count).toBe(0);
    expect(store.getState().needsYouInbox.byWorkspaceId.w1.status).toBe("loading");
  });
});

// ---- remote instance pages (cross-instance-inbox) ----

import type { RemoteInboxBundle } from "@/lib/types/remote-inbox";
import type { AppState } from "@/lib/state/store";
import {
  selectComposedNeedsYouCount,
  selectComposedNeedsYouHasMore,
  selectRemoteInboxInstances,
} from "./selectors";

function remoteBundle(overrides: Partial<RemoteInboxBundle> = {}): RemoteInboxBundle {
  return {
    ...bundle(),
    instance_id: "i1",
    workspace_id: "ws-r",
    ...overrides,
  };
}

function composedView(store: ReturnType<typeof newStore>, activeWorkspaceId?: string): AppState {
  return {
    workspaces: { activeId: activeWorkspaceId },
    needsYouInbox: store.getState().needsYouInbox,
  } as unknown as AppState;
}

describe("needs-you-inbox slice — remote instance pages", () => {
  it("applies a remote page with per-instance generation fencing", () => {
    const store = newStore();
    const generation = store.getState().beginRemoteInboxRead("i1");

    store.getState().setRemoteInboxPage("i1", generation, {
      bundles: [remoteBundle()],
      count: 1,
      hiddenCount: 1,
      nextSnoozeExpiry: null,
      hasMore: false,
    });

    const instance = store.getState().needsYouInbox.remoteByInstanceId.i1;
    expect(instance.status).toBe("ready");
    expect(instance.count).toBe(1);
    expect(instance.bundles).toHaveLength(1);
    expect(instance.bundles[0]?.instance_id).toBe("i1");
  });

  it("drops a stale remote response; instance generations never cross", () => {
    const store = newStore();
    const stale = store.getState().beginRemoteInboxRead("i1");
    const fresh = store.getState().beginRemoteInboxRead("i1");
    expect(fresh).not.toBe(stale);

    store.getState().setRemoteInboxPage("i1", stale, {
      bundles: [remoteBundle()],
      count: 1,
      hiddenCount: 0,
      nextSnoozeExpiry: null,
      hasMore: false,
    });
    const dropped = store.getState().needsYouInbox.remoteByInstanceId.i1;
    expect(dropped?.count).toBe(0);
    expect(dropped?.bundles).toHaveLength(0);
    expect(dropped?.status).not.toBe("ready");

    // A read of another instance does not fence i1's fresh generation.
    store.getState().beginRemoteInboxRead("i2");
    store.getState().setRemoteInboxPage("i1", fresh, {
      bundles: [],
      count: 0,
      hiddenCount: 0,
      nextSnoozeExpiry: null,
      hasMore: false,
    });
    expect(store.getState().needsYouInbox.remoteByInstanceId.i1?.status).toBe("ready");
  });

  it("applies a degraded read by clearing rows in the same update", () => {
    const store = newStore();
    const generation = store.getState().beginRemoteInboxRead("i1");
    store.getState().setRemoteInboxPage("i1", generation, {
      bundles: [remoteBundle()],
      count: 1,
      hiddenCount: 0,
      nextSnoozeExpiry: null,
      hasMore: false,
    });

    const next = store.getState().beginRemoteInboxRead("i1");
    store.getState().setRemoteInboxDegraded("i1", next, "unreachable");

    const instance = store.getState().needsYouInbox.remoteByInstanceId.i1;
    expect(instance.status).toBe("error");
    expect(instance.degradedReason).toBe("unreachable");
    expect(instance.bundles).toHaveLength(0);
    expect(instance.count).toBe(0);
    expect(instance.lastAppliedOk).toBe(false);
  });

  it("bumps refresh ticks globally or per instance, and retries per instance", () => {
    const store = newStore();
    store.getState().bumpRemoteInboxRefreshTick();
    store.getState().bumpRemoteInboxRefreshTick("i1");
    store.getState().bumpRemoteInboxRetryTick("i1");

    const remote = store.getState().needsYouInbox;
    expect(remote.remoteRefreshTick).toBe(1);
    expect(remote.remoteRefreshTickByInstanceId.i1).toBe(1);
    expect(remote.remoteRetryTickByInstanceId.i1).toBe(1);
    expect(remote.remoteRetryTickByInstanceId.i2).toBeUndefined();
  });
});

const DEGRADED_INSTANCE = "i-degraded";

describe("needs-you-inbox composed badge selectors", () => {
  function readyRemote(
    store: ReturnType<typeof newStore>,
    instanceId: string,
    count: number,
    hasMore = false,
  ) {
    const generation = store.getState().beginRemoteInboxRead(instanceId);
    store.getState().setRemoteInboxPage(instanceId, generation, {
      bundles: [],
      count,
      hiddenCount: 0,
      nextSnoozeExpiry: null,
      hasMore,
    });
  }

  it("local only: the composed count is the local page count", () => {
    const store = newStore();
    const generation = store.getState().beginNeedsYouInboxRead("w1");
    store.getState().setNeedsYouInboxPage("w1", generation, {
      bundles: [],
      count: 2,
      hiddenCount: 0,
      nextSnoozeExpiry: null,
      hasMore: false,
    });
    expect(selectComposedNeedsYouCount(composedView(store, "w1"))).toBe(2);
  });

  it("local plus landed remote pages sum; loading and degraded instances contribute zero", () => {
    const store = newStore();
    const generation = store.getState().beginNeedsYouInboxRead("w1");
    store.getState().setNeedsYouInboxPage("w1", generation, {
      bundles: [],
      count: 2,
      hiddenCount: 0,
      nextSnoozeExpiry: null,
      hasMore: false,
    });
    readyRemote(store, "i-ready", 3);

    store.getState().beginRemoteInboxRead("i-loading");

    const degradedGeneration = store.getState().beginRemoteInboxRead(DEGRADED_INSTANCE);
    store.getState().setRemoteInboxPage(DEGRADED_INSTANCE, degradedGeneration, {
      bundles: [],
      count: 7,
      hiddenCount: 0,
      nextSnoozeExpiry: null,
      hasMore: false,
    });
    const nextDegraded = store.getState().beginRemoteInboxRead(DEGRADED_INSTANCE);
    store.getState().setRemoteInboxDegraded(DEGRADED_INSTANCE, nextDegraded, "error");

    expect(selectComposedNeedsYouCount(composedView(store, "w1"))).toBe(5);
  });

  it("composed has_more is the disjunction of local and landed remote pages", () => {
    const store = newStore();
    readyRemote(store, "i-ready", 1, true);
    expect(selectComposedNeedsYouHasMore(composedView(store))).toBe(true);

    const fresh = newStore();
    const degradedGeneration = fresh.getState().beginRemoteInboxRead("i-x");
    fresh.getState().setRemoteInboxDegraded("i-x", degradedGeneration, "unreachable");
    expect(selectComposedNeedsYouHasMore(composedView(fresh))).toBe(false);
  });

  it("enumerates remote instance states for section rendering", () => {
    const store = newStore();
    readyRemote(store, "i-1", 1);
    const instances = selectRemoteInboxInstances(composedView(store));
    expect(instances).toHaveLength(1);
    expect(instances[0]?.instanceId).toBe("i-1");
  });
});
