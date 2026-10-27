import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

describe("isDebugUI", () => {
  beforeEach(() => {
    vi.resetModules();
  });

  afterEach(() => {
    vi.unstubAllEnvs();
    vi.unstubAllGlobals();
  });

  it("is true when window.__KANDEV_DEBUG is set at runtime", async () => {
    vi.stubEnv("VITE_KANDEV_DEBUG", "");
    vi.stubGlobal("window", { __KANDEV_DEBUG: true });
    const { isDebugUI } = await import("./config");
    expect(isDebugUI()).toBe(true);
  });

  it("is false in production with no flags", async () => {
    vi.stubEnv("VITE_KANDEV_DEBUG", "");
    vi.stubGlobal("window", {});
    const { isDebugUI } = await import("./config");
    expect(isDebugUI()).toBe(false);
  });

  it("is true when VITE_KANDEV_DEBUG=true", async () => {
    vi.stubEnv("VITE_KANDEV_DEBUG", "true");
    vi.stubGlobal("window", {});
    const { isDebugUI } = await import("./config");
    expect(isDebugUI()).toBe(true);
  });
});

describe("getLocalBackendConfig", () => {
  beforeEach(() => {
    vi.resetModules();
  });

  afterEach(() => {
    vi.unstubAllEnvs();
    vi.unstubAllGlobals();
  });

  it("ignores the injected remote-instance port and resolves same-origin", async () => {
    vi.stubEnv("VITE_KANDEV_API_PORT", "");
    vi.stubGlobal("window", {
      __KANDEV_API_PORT: "41234",
      location: {
        protocol: "https:",
        hostname: "panel.local",
        origin: "https://panel.local",
      },
    });
    const { getBackendConfig, getLocalBackendConfig } = await import("./config");
    expect(getBackendConfig().apiBaseUrl).toBe("https://panel.local:41234");
    expect(getLocalBackendConfig().apiBaseUrl).toBe("https://panel.local");
  });

  it("matches getBackendConfig when no instance port is injected", async () => {
    vi.stubEnv("VITE_KANDEV_API_PORT", "");
    vi.stubGlobal("window", {
      location: { protocol: "http:", hostname: "localhost", origin: "http://localhost:37429" },
    });
    const { getBackendConfig, getLocalBackendConfig } = await import("./config");
    expect(getLocalBackendConfig().apiBaseUrl).toBe(getBackendConfig().apiBaseUrl);
  });
});
