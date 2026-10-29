import { fetchJson, type ApiRequestOptions } from "../client";
import { getLocalBackendConfig } from "@/lib/config";
import type { ClarificationInboxSnoozeDuration } from "@/lib/types/clarification-inbox";
import type { RemoteNeedsYouHiddenPage, RemoteNeedsYouPage } from "@/lib/types/remote-inbox";

/**
 * REST client for the cross-instance inbox forward routes
 * (`/api/v1/instances/:id/clarification-inbox...` and
 * `/api/v1/instances/:id/needs-you-inbox...`).
 *
 * These routes exist only while both `features.multiInstance` and
 * `features.needsYouInbox` are on; with either flag off every request 404s.
 *
 * The fan-out is a local-backend surface (instances-api's guard): while the
 * panel views a remote instance, `getBackendConfig().apiBaseUrl` points at
 * that instance's proxy port, which does not serve these routes. Every call
 * here therefore resolves against the local base by default; an explicit
 * caller baseUrl still wins.
 */

const INSTANCES_PATH = "/api/v1/instances";

/** Resolves the request against the local backend unless the caller overrides the base. */
function localOptions(options?: ApiRequestOptions): ApiRequestOptions {
  return { ...options, baseUrl: options?.baseUrl ?? getLocalBackendConfig().apiBaseUrl };
}

function sidecarPath(instanceId: string, pendingId: string): string {
  return `${INSTANCES_PATH}/${encodeURIComponent(instanceId)}/clarification-inbox/sidecar/${encodeURIComponent(pendingId)}`;
}

/**
 * Reads one instance's merged needs-you page (clarification questions plus
 * manual-step waiting rows) through the local backend's forward route.
 */
export function listRemoteNeedsYouInbox(
  instanceId: string,
  options?: ApiRequestOptions,
): Promise<RemoteNeedsYouPage> {
  return fetchJson<RemoteNeedsYouPage>(
    `${INSTANCES_PATH}/${encodeURIComponent(instanceId)}/needs-you-inbox`,
    localOptions(options),
  );
}

/** Reads one instance's merged hidden enumeration through the forward route. */
export function listRemoteHiddenNeedsYouInbox(
  instanceId: string,
  options?: ApiRequestOptions,
): Promise<RemoteNeedsYouHiddenPage> {
  return fetchJson<RemoteNeedsYouHiddenPage>(
    `${INSTANCES_PATH}/${encodeURIComponent(instanceId)}/needs-you-inbox/hidden`,
    localOptions(options),
  );
}

/** Body shape the shared answer chain posts (answer batch or rejection). */
export function respondRemoteClarification(
  instanceId: string,
  pendingId: string,
  body: Record<string, unknown>,
  options?: ApiRequestOptions,
): Promise<Record<string, unknown>> {
  return fetchJson<Record<string, unknown>>(
    `${INSTANCES_PATH}/${encodeURIComponent(instanceId)}/clarification/${encodeURIComponent(pendingId)}/respond`,
    {
      ...localOptions(options),
      init: {
        ...options?.init,
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      },
    },
  );
}

export function dismissRemoteClarificationBundle(
  instanceId: string,
  pendingId: string,
  options?: ApiRequestOptions,
): Promise<void> {
  return fetchJson<void>(sidecarPath(instanceId, pendingId), {
    ...localOptions(options),
    init: {
      ...options?.init,
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ state: "dismissed" }),
    },
  });
}

export function snoozeRemoteClarificationBundle(
  instanceId: string,
  pendingId: string,
  duration: ClarificationInboxSnoozeDuration,
  options?: ApiRequestOptions,
): Promise<void> {
  return fetchJson<void>(sidecarPath(instanceId, pendingId), {
    ...localOptions(options),
    init: {
      ...options?.init,
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ state: "snoozed", snooze_duration: duration }),
    },
  });
}

export function restoreRemoteClarificationBundle(
  instanceId: string,
  pendingId: string,
  options?: ApiRequestOptions,
): Promise<void> {
  return fetchJson<void>(sidecarPath(instanceId, pendingId), {
    ...localOptions(options),
    init: { ...options?.init, method: "DELETE" },
  });
}

/**
 * Builds the shared answer chain's `respondTarget` for one instance's rows:
 * the hook submits through the local backend's forward route. Aligned with
 * respondRemoteClarification's URL so the two callers cannot drift.
 */
export function remoteClarificationRespondTarget(
  instanceId: string,
): (pendingId: string) => string {
  return (pendingId) =>
    `${getLocalBackendConfig().apiBaseUrl}${INSTANCES_PATH}/${encodeURIComponent(instanceId)}/clarification/${encodeURIComponent(pendingId)}/respond`;
}
