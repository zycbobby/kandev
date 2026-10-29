import type { NeedsYouInboxRow } from "@/lib/types/needs-you-inbox";
import type { RemoteInboxDegradedReason, RemoteNeedsYouRow } from "@/lib/types/remote-inbox";
import type { ClarificationAnswer } from "@/lib/types/http";

export type NeedsYouInboxReadStatus = "idle" | "loading" | "ready" | "error";

// The boot-hydration producer's wire shape (needs-you-inbox
// design-01#Data-and-contracts): carried on the boot payload's
// `initialState.needsYouInboxBoot`, distinct from the `needsYouInbox` slice
// shape itself, and consumed once via seedNeedsYouInboxBoot. The merged boot
// summary counts questions and waiting rows together.
export type NeedsYouInboxBootSeed = {
  workspaceId: string;
  count: number;
  hasMore: boolean;
  nextSnoozeExpiry: string | null;
};

export type NeedsYouInboxWorkspaceState = {
  rows: NeedsYouInboxRow[];
  count: number;
  hiddenCount: number;
  nextSnoozeExpiry: string | null;
  hasMore: boolean;
  status: NeedsYouInboxReadStatus;
  // The generation of the last response actually applied to this workspace's
  // rows, for the stale-response guard.
  appliedGeneration: number;
  // Whether the last applied response was a successful page, as opposed to an
  // error or no response ever having applied. `status` alone cannot answer
  // this during a refresh: `beginNeedsYouInboxRead` overwrites `status` to
  // "loading" without touching what preceded it, so a refresh that follows a
  // failed read and a refresh that follows a successful one are otherwise the
  // same tuple.
  lastAppliedOk: boolean;
};

/** One remote instance's aggregated merged needs-you page, under the same
 * stale-response discipline as the local workspace rows but keyed by
 * instance. Rows are kind-tagged exactly like the local read's rows
 * (question or waiting), each carrying instance and origin-workspace
 * identity. */
export type RemoteInboxInstanceState = {
  instanceId: string;
  status: NeedsYouInboxReadStatus;
  /** Present only on a degraded read (status "error"). */
  degradedReason?: RemoteInboxDegradedReason;
  rows: RemoteNeedsYouRow[];
  count: number;
  hiddenCount: number;
  nextSnoozeExpiry: string | null;
  hasMore: boolean;
  appliedGeneration: number;
  lastAppliedOk: boolean;
};

export type NeedsYouInboxSliceState = {
  needsYouInbox: {
    byWorkspaceId: Record<string, NeedsYouInboxWorkspaceState>;
    // Latest generation ISSUED per workspace (bumped once per read this
    // client starts). A response is applied only when its generation still
    // equals this counter -- anything older lost the race and is dropped.
    generationByWorkspaceId: Record<string, number>;
    // Bumped by the WS-action refresh triggers (design-02#Control-flow):
    // session.pending_action_changed, session.state_changed, and
    // task.manual_step_entered (a new waiting episode). Not per-workspace --
    // every trigger re-reads whichever workspace is active when it fires,
    // never the one the event happened to name.
    refreshTick: number;
    manualRetryTick: number;
    // Remote instance pages (cross-instance-inbox): same per-key generation
    // discipline as the local rows, keyed by instance id.
    remoteByInstanceId: Record<string, RemoteInboxInstanceState>;
    remoteDraftsByInstanceId: Record<string, Record<string, ClarificationAnswer[]>>;
    generationByInstanceId: Record<string, number>;
    // Global remote refresh trigger (mount, instance-list change, foreground
    // resume) and the per-instance variant a relay notification bumps.
    remoteRefreshTick: number;
    remoteRefreshTickByInstanceId: Record<string, number>;
    remoteRetryTickByInstanceId: Record<string, number>;
  };
};

export type NeedsYouInboxSliceActions = {
  /** Bumps and returns the new request generation for a workspace read. */
  beginNeedsYouInboxRead: (workspaceId: string) => number;
  /** Applies a successful merged page read if its generation is still current. */
  setNeedsYouInboxPage: (
    workspaceId: string,
    generation: number,
    page: {
      rows: NeedsYouInboxRow[];
      count: number;
      hiddenCount: number;
      nextSnoozeExpiry: string | null;
      hasMore: boolean;
    },
  ) => void;
  /** Applies a failed read if its generation is still current -- clears rows
   * and count in the same update (design-02#Failure-and-recovery). */
  setNeedsYouInboxError: (workspaceId: string, generation: number) => void;
  /** Seeds a workspace from boot hydration at generation 0, so the first real
   * read (generation >= 1) always supersedes it. */
  seedNeedsYouInboxBoot: (
    workspaceId: string,
    seed: { count: number; hasMore: boolean; nextSnoozeExpiry: string | null },
  ) => void;
  /** Bumps the WS-action refresh trigger tick (design-02#Control-flow). */
  bumpNeedsYouInboxRefreshTick: () => void;
  /** Requests an explicit retry even after automatic refreshes are suspended. */
  requestNeedsYouInboxRetry: () => void;

  // ---- remote instance pages (cross-instance-inbox) ----

  /** Bumps and returns the new request generation for one instance read. */
  beginRemoteInboxRead: (instanceId: string) => number;
  /** Remove pages outside the current enabled registry and fence older reads. */
  retainRemoteInboxInstances: (instanceIds: string[]) => void;
  setRemoteInboxDraft: (
    instanceId: string,
    pendingId: string,
    answers: ClarificationAnswer[],
  ) => void;
  /** Applies a successful instance page if its generation is still current. */
  setRemoteInboxPage: (
    instanceId: string,
    generation: number,
    page: {
      rows: RemoteNeedsYouRow[];
      count: number;
      hiddenCount: number;
      nextSnoozeExpiry: string | null;
      hasMore: boolean;
    },
  ) => void;
  /** Applies a degraded instance read if its generation is still current --
   * rows and count clear in the same update so the section renders the
   * degraded state, never stale rows over a zero badge. */
  setRemoteInboxDegraded: (
    instanceId: string,
    generation: number,
    reason: RemoteInboxDegradedReason,
  ) => void;
  /** Bumps the remote refresh trigger globally, or for one instance only. */
  bumpRemoteInboxRefreshTick: (instanceId?: string) => void;
  /** Records an explicit per-instance retry. */
  bumpRemoteInboxRetryTick: (instanceId: string) => void;
};

export type NeedsYouInboxSlice = NeedsYouInboxSliceState & NeedsYouInboxSliceActions;
