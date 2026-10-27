import { fetchJson, type ApiRequestOptions } from "../client";
import { parseStrictRfc3339Timestamp } from "@/lib/utils/strict-timestamp";

/**
 * REST client for the remote-instance registry (`/api/v1/instances`).
 *
 * The routes exist only while `features.multiInstance` is on; with the flag
 * off every request 404s. Foreign and missing ids are one indistinguishable
 * 404, so callers surface a single "not found" outcome.
 */

/** One registered remote instance, as returned by the registry REST routes. */
export type RemoteInstance = {
  id: string;
  name: string;
  base_url: string;
  credential_secret_id?: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

/** The instance registry's last health report for one remote instance. */
export type InstanceHealth = {
  reachable: boolean;
  version?: string;
  error?: string;
  supported: boolean;
  /** RFC3339; the Go zero time means the poller has not run yet. */
  lastCheckedAt: string;
};

export type CreateInstanceInput = {
  name: string;
  base_url: string;
  credential_secret_id?: string;
  enabled?: boolean;
};

export type UpdateInstanceInput = {
  name?: string;
  base_url?: string;
  credential_secret_id?: string;
  enabled?: boolean;
};

const INSTANCES_PATH = "/api/v1/instances";

function instancePath(id: string): string {
  return `${INSTANCES_PATH}/${encodeURIComponent(id)}`;
}

export async function listInstances(options?: ApiRequestOptions): Promise<RemoteInstance[]> {
  return fetchJson<RemoteInstance[]>(INSTANCES_PATH, options);
}

export async function createInstance(
  payload: CreateInstanceInput,
  options?: ApiRequestOptions,
): Promise<RemoteInstance> {
  return fetchJson<RemoteInstance>(INSTANCES_PATH, {
    ...options,
    init: { ...options?.init, method: "POST", body: JSON.stringify(payload) },
  });
}

export async function updateInstance(
  id: string,
  payload: UpdateInstanceInput,
  options?: ApiRequestOptions,
): Promise<RemoteInstance> {
  return fetchJson<RemoteInstance>(instancePath(id), {
    ...options,
    init: { ...options?.init, method: "PATCH", body: JSON.stringify(payload) },
  });
}

export async function deleteInstance(
  id: string,
  options?: ApiRequestOptions,
): Promise<{ success: boolean }> {
  return fetchJson<{ success: boolean }>(instancePath(id), {
    ...options,
    init: { ...options?.init, method: "DELETE" },
  });
}

export async function getInstanceHealth(
  id: string,
  options?: ApiRequestOptions,
): Promise<InstanceHealth> {
  return fetchJson<InstanceHealth>(`${instancePath(id)}/health`, options);
}

/** POST select: records the instance cookie and returns the reload target. */
export async function selectInstance(
  id: string,
  options?: ApiRequestOptions,
): Promise<{ href: string }> {
  return fetchJson<{ href: string }>(`${instancePath(id)}/select`, {
    ...options,
    init: { ...options?.init, method: "POST" },
  });
}

// Go's time.Time zero value serializes as 0001-01-01T00:00:00Z; the health
// poller has not produced a report for an instance until this is surpassed.
const GO_ZERO_TIME_NANOS = parseStrictRfc3339Timestamp("0001-01-01T00:00:00Z") ?? BigInt(-1);

/**
 * The instant the health poller last checked this instance, or null when it
 * has not run yet (or the wire value is malformed). Malformed values are
 * rejected rather than Date-normalized, per the shared strict parser.
 */
export function instanceHealthCheckedAt(health: InstanceHealth): Date | null {
  const nanos = parseStrictRfc3339Timestamp(health.lastCheckedAt);
  if (nanos === null || nanos <= GO_ZERO_TIME_NANOS) return null;
  return new Date(health.lastCheckedAt);
}
