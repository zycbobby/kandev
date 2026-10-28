// Remote instance-page wire types (cross-instance-inbox design, "Contracts").
// Mirrors apps/backend/internal/backendapp/instance_inbox_routes.go's
// instanceInboxPageEnvelope and
// internal/instances/remoteinbox/types.go's Bundle shapes.
import type {
  ClarificationInboxBundle,
  ClarificationInboxSidecarState,
} from "@/lib/types/clarification-inbox";

/** Closed set of degraded reasons one instance page can carry. */
export type RemoteInboxDegradedReason = "unreachable" | "unauthorized" | "inbox_disabled" | "error";

/** One remote answerable row: the local bundle shape plus origin identity. */
export type RemoteInboxBundle = ClarificationInboxBundle & {
  instance_id: string;
  workspace_id: string;
};

/** One remote hidden row (hidden enumeration only). */
export type RemoteInboxHiddenBundle = RemoteInboxBundle & {
  state: ClarificationInboxSidecarState;
  snooze_until: string | null;
};

/**
 * One instance's aggregated page. A degraded read is still HTTP 200: the
 * degradation is instance-scoped state the Inbox renders, never a transport
 * failure of the read itself.
 */
export type RemoteInboxPage = {
  instance_id: string;
  instance_name: string;
  status: "ok" | "degraded";
  degraded_reason?: RemoteInboxDegradedReason;
  bundles: RemoteInboxBundle[];
  count: number;
  hidden_count: number;
  /** Hidden enumeration only: the instance-wide hidden total. */
  total?: number;
  next_snooze_expiry: string | null;
  has_more: boolean;
};

export type RemoteInboxHiddenPage = Omit<RemoteInboxPage, "bundles" | "total"> & {
  bundles: RemoteInboxHiddenBundle[];
  total: number;
};
