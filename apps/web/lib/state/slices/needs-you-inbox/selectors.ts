import type { AppState } from "@/lib/state/store";
import type { ClarificationInboxBundle } from "@/lib/types/clarification-inbox";
import type { NeedsYouInboxWorkspaceState, RemoteInboxInstanceState } from "./types";

const EMPTY_BUNDLES: ClarificationInboxBundle[] = [];

const EMPTY_WORKSPACE_STATE: NeedsYouInboxWorkspaceState = {
  bundles: EMPTY_BUNDLES,
  count: 0,
  hiddenCount: 0,
  nextSnoozeExpiry: null,
  hasMore: false,
  status: "idle",
  appliedGeneration: 0,
  lastAppliedOk: false,
};

function selectActiveWorkspaceState(state: AppState): NeedsYouInboxWorkspaceState {
  const workspaceId = state.workspaces.activeId;
  if (!workspaceId) return EMPTY_WORKSPACE_STATE;
  return state.needsYouInbox.byWorkspaceId[workspaceId] ?? EMPTY_WORKSPACE_STATE;
}

/** The sidebar badge count for the active workspace. Absent workspace or
 * absent state renders no badge, not a zero -- distinguishing "not seeded"
 * from "confirmed empty" at the presentation layer (callers gate visibility
 * separately). */
export function selectNeedsYouInboxCount(state: AppState): number {
  return selectActiveWorkspaceState(state).count;
}

export function selectNeedsYouInboxBundles(state: AppState): ClarificationInboxBundle[] {
  return selectActiveWorkspaceState(state).bundles;
}

export function selectNeedsYouInboxStatus(state: AppState) {
  return selectActiveWorkspaceState(state).status;
}

export function selectNeedsYouInboxHiddenCount(state: AppState): number {
  return selectActiveWorkspaceState(state).hiddenCount;
}

export function selectNeedsYouInboxRevision(state: AppState): number {
  return selectActiveWorkspaceState(state).appliedGeneration;
}

/** Whether a successful page (as opposed to an error, or no response yet) is
 * the last thing actually applied to this workspace's rows -- see
 * `resolveViewMode`'s use of this for why `status`/`appliedGeneration` alone
 * cannot answer it during a refresh. */
export function selectNeedsYouInboxLastAppliedOk(state: AppState): boolean {
  return selectActiveWorkspaceState(state).lastAppliedOk;
}

export function selectNeedsYouInboxHasMore(state: AppState): boolean {
  return selectActiveWorkspaceState(state).hasMore;
}

export function selectNeedsYouInboxNextSnoozeExpiry(state: AppState): string | null {
  return selectActiveWorkspaceState(state).nextSnoozeExpiry;
}

// ---- composed cross-instance badge (cross-instance-inbox design) ----

/** Every remote instance state the controller has created, for section
 * rendering. Order follows insertion; sections key by instanceId. */
export function selectRemoteInboxInstances(state: AppState): RemoteInboxInstanceState[] {
  return Object.values(state.needsYouInbox.remoteByInstanceId ?? {});
}

function selectLandedRemoteInstances(state: AppState): RemoteInboxInstanceState[] {
  // count-known semantics (failed-inbox precedent): only an instance whose
  // page actually landed contributes rows, so the badge never reports rows
  // the composed view does not list.
  return selectRemoteInboxInstances(state).filter((instance) => instance.status === "ready");
}

/** The sidebar badge count for the active workspace plus every landed remote
 * instance page. Unloaded and degraded instances contribute zero. */
export function selectComposedNeedsYouCount(state: AppState): number {
  const remoteTotal = selectLandedRemoteInstances(state).reduce(
    (sum, instance) => sum + instance.count,
    0,
  );
  return selectNeedsYouInboxCount(state) + remoteTotal;
}

/** Truncation flag of the composed view: local or any landed remote page. */
export function selectComposedNeedsYouHasMore(state: AppState): boolean {
  if (selectNeedsYouInboxHasMore(state)) return true;
  return selectLandedRemoteInstances(state).some((instance) => instance.hasMore);
}
