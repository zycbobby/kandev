export type AppConfig = {
  apiBaseUrl: string;
};

// Non-browser default used by route/action helpers running beside the backend.
const DEFAULT_API_BASE_URL = "http://localhost:38429";

let debugUiCached: boolean | undefined;

/** Whether debug-only UI (poll badges, debug overlay) should render. */
export function isDebugUI(): boolean {
  if (debugUiCached !== undefined) return debugUiCached;
  const env = getViteEnv();
  debugUiCached =
    env.VITE_KANDEV_DEBUG === "true" ||
    (typeof process !== "undefined" &&
      (process.env.KANDEV_DEBUG === "true" || process.env.VITE_KANDEV_DEBUG === "true")) ||
    (typeof window !== "undefined" && window.__KANDEV_DEBUG === true);
  return debugUiCached;
}

export function getBackendConfig(): AppConfig {
  // Server-side: use env vars or localhost defaults (SSR runs on same machine as backend)
  if (typeof window === "undefined") {
    return { apiBaseUrl: serverApiBaseUrl() };
  }

  // Client-side URL resolution:
  // 1. Port-based URL via __KANDEV_API_PORT (dev mode: browser on :37429, API on :38429; remote-instance view: that instance's proxy port)
  // 2. Vite env port (Vite dev server injects VITE_* env at build time)
  // 3. Same-origin (production: Go serves the SPA and API on one port)
  //    Works for any hosting scenario: localhost, custom domain, Tailscale, etc.
  const env = getViteEnv();
  const port = readPort(window.__KANDEV_API_PORT) ?? readPort(env.VITE_KANDEV_API_PORT);
  return { apiBaseUrl: clientApiBaseUrl(port) };
}

/**
 * The local backend's base URL: the same resolution as getBackendConfig but
 * IGNORING window.__KANDEV_API_PORT. While the panel views a remote instance
 * that global points every panel-data call at the instance's proxy port, but
 * the federation endpoints (instance registry, health, select, remote
 * notifications) exist only on the local backend, so their calls resolve here.
 */
export function getLocalBackendConfig(): AppConfig {
  if (typeof window === "undefined") {
    return { apiBaseUrl: serverApiBaseUrl() };
  }
  const env = getViteEnv();
  return { apiBaseUrl: clientApiBaseUrl(readPort(env.VITE_KANDEV_API_PORT)) };
}

function serverApiBaseUrl(): string {
  return process.env.KANDEV_API_BASE_URL ?? DEFAULT_API_BASE_URL;
}

function clientApiBaseUrl(port: number | null): string {
  if (port) {
    return `${window.location.protocol}//${window.location.hostname}:${port}`;
  }
  return window.location.origin;
}

function readPort(value: string | undefined): number | null {
  if (!value) return null;
  const port = parseInt(value, 10);
  return Number.isInteger(port) && port > 0 && port <= 65535 ? port : null;
}

function getViteEnv(): Record<string, string | undefined> {
  return (import.meta as unknown as { env?: Record<string, string | undefined> }).env ?? {};
}
