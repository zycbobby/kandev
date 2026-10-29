import { fetchJson, type ApiRequestOptions } from "../client";
import type { NeedsYouInboxHiddenPage, NeedsYouInboxPage } from "@/lib/types/needs-you-inbox";

const BASE = "/api/v1/needs-you-inbox";

// One canonical parameter set: every caller passes only workspaceId. No
// cursor — the merged read reports has_more instead of forward pages, and v1
// never pages past the first bound.
export function listNeedsYouInbox(
  workspaceId: string,
  options?: ApiRequestOptions,
): Promise<NeedsYouInboxPage> {
  return fetchJson<NeedsYouInboxPage>(
    `${BASE}?workspace_id=${encodeURIComponent(workspaceId)}`,
    options,
  );
}

export function listHiddenNeedsYouInbox(
  workspaceId: string,
  options?: ApiRequestOptions,
): Promise<NeedsYouInboxHiddenPage> {
  return fetchJson<NeedsYouInboxHiddenPage>(
    `${BASE}/hidden?workspace_id=${encodeURIComponent(workspaceId)}`,
    options,
  );
}
