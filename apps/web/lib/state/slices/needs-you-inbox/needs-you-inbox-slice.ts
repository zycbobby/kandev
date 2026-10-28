import type { StateCreator } from "zustand";
import type { AppState } from "../../app-state-types";
import type {
  NeedsYouInboxSlice,
  NeedsYouInboxSliceState,
  NeedsYouInboxWorkspaceState,
  RemoteInboxInstanceState,
} from "./types";
import type { RemoteInboxDegradedReason } from "@/lib/types/remote-inbox";

export const defaultNeedsYouInboxState: NeedsYouInboxSliceState = {
  needsYouInbox: {
    byWorkspaceId: {},
    generationByWorkspaceId: {},
    refreshTick: 0,
    manualRetryTick: 0,
    remoteByInstanceId: {},
    generationByInstanceId: {},
    remoteRefreshTick: 0,
    remoteRefreshTickByInstanceId: {},
    remoteRetryTickByInstanceId: {},
  },
};

const emptyWorkspaceState = (): NeedsYouInboxWorkspaceState => ({
  bundles: [],
  count: 0,
  hiddenCount: 0,
  nextSnoozeExpiry: null,
  hasMore: false,
  status: "idle",
  appliedGeneration: 0,
  lastAppliedOk: false,
});

const emptyRemoteInstanceState = (instanceId: string): RemoteInboxInstanceState => ({
  instanceId,
  bundles: [],
  count: 0,
  hiddenCount: 0,
  nextSnoozeExpiry: null,
  hasMore: false,
  status: "idle",
  appliedGeneration: 0,
  lastAppliedOk: false,
});

// Typed against the full `AppState` (the zustand slices-pattern shape), not
// this slice's own narrower type -- that keeps `set` structurally identical
// to the root store's `set`, so composing this slice in store.ts needs no
// `as any` escape (ARCH-FRONTEND-ROOT-STATE-CAST).
type ImmerSet = Parameters<
  StateCreator<AppState, [["zustand/immer", never]], [], NeedsYouInboxSlice>
>[0];

/**
 * Remote instance-page actions, extracted only to keep the composed slice
 * factory under the function-line limit; they share the single `set` seam
 * and the same per-key stale-response discipline as the local rows.
 */
function createRemoteInboxActions(set: ImmerSet) {
  return {
    beginRemoteInboxRead: (instanceId: string) => {
      let generation = 0;
      set((draft) => {
        const remote = draft.needsYouInbox;
        const next = (remote.generationByInstanceId[instanceId] ?? 0) + 1;
        remote.generationByInstanceId[instanceId] = next;
        const instance =
          remote.remoteByInstanceId[instanceId] ?? emptyRemoteInstanceState(instanceId);
        instance.status = "loading";
        delete instance.degradedReason;
        remote.remoteByInstanceId[instanceId] = instance;
        generation = next;
      });
      return generation;
    },
    setRemoteInboxPage: (
      instanceId: string,
      generation: number,
      page: Parameters<NeedsYouInboxSlice["setRemoteInboxPage"]>[2],
    ) =>
      set((draft) => {
        const remote = draft.needsYouInbox;
        if (remote.generationByInstanceId[instanceId] !== generation) return;
        remote.remoteByInstanceId[instanceId] = {
          instanceId,
          bundles: page.bundles,
          count: page.count,
          hiddenCount: page.hiddenCount,
          nextSnoozeExpiry: page.nextSnoozeExpiry,
          hasMore: page.hasMore,
          status: "ready",
          appliedGeneration: generation,
          lastAppliedOk: true,
        };
      }),
    setRemoteInboxDegraded: (
      instanceId: string,
      generation: number,
      reason: RemoteInboxDegradedReason,
    ) =>
      set((draft) => {
        const remote = draft.needsYouInbox;
        if (remote.generationByInstanceId[instanceId] !== generation) return;
        remote.remoteByInstanceId[instanceId] = {
          ...emptyRemoteInstanceState(instanceId),
          status: "error",
          degradedReason: reason,
          appliedGeneration: generation,
        };
      }),
    bumpRemoteInboxRefreshTick: (instanceId?: string) =>
      set((draft) => {
        const remote = draft.needsYouInbox;
        if (instanceId === undefined) {
          remote.remoteRefreshTick += 1;
          return;
        }
        remote.remoteRefreshTickByInstanceId[instanceId] =
          (remote.remoteRefreshTickByInstanceId[instanceId] ?? 0) + 1;
      }),
    bumpRemoteInboxRetryTick: (instanceId: string) =>
      set((draft) => {
        const remote = draft.needsYouInbox;
        remote.remoteRetryTickByInstanceId[instanceId] =
          (remote.remoteRetryTickByInstanceId[instanceId] ?? 0) + 1;
      }),
  };
}

/**
 * Typed as a plain factory over `set` (mirrors the review slice): every
 * action here only needs to write, never read prior state through `get`.
 */
export const createNeedsYouInboxSlice = (set: ImmerSet): NeedsYouInboxSlice => ({
  ...defaultNeedsYouInboxState,

  beginNeedsYouInboxRead: (workspaceId) => {
    let generation = 0;
    set((draft) => {
      const next = (draft.needsYouInbox.generationByWorkspaceId[workspaceId] ?? 0) + 1;
      draft.needsYouInbox.generationByWorkspaceId[workspaceId] = next;
      const workspace = draft.needsYouInbox.byWorkspaceId[workspaceId] ?? emptyWorkspaceState();
      workspace.status = "loading";
      draft.needsYouInbox.byWorkspaceId[workspaceId] = workspace;
      generation = next;
    });
    return generation;
  },

  setNeedsYouInboxPage: (workspaceId, generation, page) =>
    set((draft) => {
      if (draft.needsYouInbox.generationByWorkspaceId[workspaceId] !== generation) return;
      draft.needsYouInbox.byWorkspaceId[workspaceId] = {
        bundles: page.bundles,
        count: page.count,
        hiddenCount: page.hiddenCount,
        nextSnoozeExpiry: page.nextSnoozeExpiry,
        hasMore: page.hasMore,
        status: "ready",
        appliedGeneration: generation,
        lastAppliedOk: true,
      };
    }),

  setNeedsYouInboxError: (workspaceId, generation) =>
    set((draft) => {
      if (draft.needsYouInbox.generationByWorkspaceId[workspaceId] !== generation) return;
      // A failed read clears the rows it was replacing in the SAME update
      // (design-02#Failure-and-recovery) -- stale rows over an absent badge
      // is a disagreement the badge and the list must never show.
      draft.needsYouInbox.byWorkspaceId[workspaceId] = {
        ...emptyWorkspaceState(),
        status: "error",
        appliedGeneration: generation,
      };
    }),

  seedNeedsYouInboxBoot: (workspaceId, seed) =>
    set((draft) => {
      // Generation 0 seeds only; if a real read already applied (generation
      // >= 1 was issued), boot arriving late must not overwrite fresher rows.
      if (draft.needsYouInbox.generationByWorkspaceId[workspaceId]) return;
      if (draft.needsYouInbox.byWorkspaceId[workspaceId]) return;
      draft.needsYouInbox.byWorkspaceId[workspaceId] = {
        bundles: [],
        count: seed.count,
        hiddenCount: 0,
        nextSnoozeExpiry: seed.nextSnoozeExpiry,
        hasMore: seed.hasMore,
        status: "idle",
        appliedGeneration: 0,
        lastAppliedOk: false,
      };
    }),

  bumpNeedsYouInboxRefreshTick: () =>
    set((draft) => {
      draft.needsYouInbox.refreshTick += 1;
    }),

  requestNeedsYouInboxRetry: () =>
    set((draft) => {
      draft.needsYouInbox.manualRetryTick += 1;
    }),

  ...createRemoteInboxActions(set),
});
