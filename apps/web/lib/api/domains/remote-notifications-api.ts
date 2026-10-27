import { fetchJson, type ApiRequestOptions } from "../client";
import { getLocalBackendConfig } from "@/lib/config";

/**
 * REST client for the local backend's relayed-notification surface
 * (`/api/v1/remote-notifications`).
 *
 * The routes exist only while `features.multiInstance` is on; with the flag
 * off every request 404s. The occurrence table lives on the LOCAL backend:
 * while the panel views a remote instance, `getBackendConfig().apiBaseUrl`
 * points at that instance's proxy port, which does not serve these routes.
 * Every call therefore resolves against the local base by default; an
 * explicit caller baseUrl still wins.
 */

/**
 * One relayed occurrence row, as returned by the REST routes and carried by
 * the `remote_notification` WebSocket frames (the same shape on both wires,
 * including the local row `id` shared with the read/dismiss actions).
 */
export type RemoteNotificationOccurrence = {
  id: string;
  instance_id: string;
  event: string;
  occurrence_id: string;
  task_id: string;
  session_id: string;
  title: string;
  body: string;
  created_at: string;
  read: boolean;
  dismissed: boolean;
};

const NOTIFICATIONS_PATH = "/api/v1/remote-notifications";

/** Resolves the request against the local backend unless the caller overrides the base. */
function localOptions(options?: ApiRequestOptions): ApiRequestOptions {
  return { ...options, baseUrl: options?.baseUrl ?? getLocalBackendConfig().apiBaseUrl };
}

function post(path: string, options?: ApiRequestOptions): Promise<{ success: boolean }> {
  return fetchJson<{ success: boolean }>(path, {
    ...localOptions(options),
    init: { ...options?.init, method: "POST" },
  });
}

/**
 * Lists the retained occurrences for this owner. The backend orders rows
 * oldest-first and caps the page, so the module renders the bounded recent
 * window without client-side pagination.
 */
export async function listRemoteNotifications(
  options?: ApiRequestOptions,
): Promise<RemoteNotificationOccurrence[]> {
  return fetchJson<RemoteNotificationOccurrence[]>(NOTIFICATIONS_PATH, localOptions(options));
}

/** Marks one occurrence read. A foreign or missing id returns one 404. */
export async function markRemoteNotificationRead(
  id: string,
  options?: ApiRequestOptions,
): Promise<{ success: boolean }> {
  return post(`${NOTIFICATIONS_PATH}/${encodeURIComponent(id)}/read`, options);
}

/** Dismisses one occurrence. A foreign or missing id returns one 404. */
export async function dismissRemoteNotification(
  id: string,
  options?: ApiRequestOptions,
): Promise<{ success: boolean }> {
  return post(`${NOTIFICATIONS_PATH}/${encodeURIComponent(id)}/dismiss`, options);
}

/** Marks every retained occurrence for this owner read. */
export async function markAllRemoteNotificationsRead(
  options?: ApiRequestOptions,
): Promise<{ success: boolean }> {
  return post(`${NOTIFICATIONS_PATH}/read-all`, options);
}
