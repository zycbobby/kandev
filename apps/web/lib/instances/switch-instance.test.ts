import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const select = vi.hoisted(() => vi.fn());
vi.mock("@/lib/api/domains/instances-api", () => ({
  selectInstance: select,
}));

import { switchInstance } from "./switch-instance";

const assign = vi.fn();

beforeEach(() => {
  vi.stubGlobal("location", { assign });
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

describe("switchInstance", () => {
  it("selects the instance and reloads to the returned href without a target path", async () => {
    select.mockResolvedValue({ href: "/" });

    await switchInstance("inst-1");

    expect(select).toHaveBeenCalledWith("inst-1");
    expect(assign).toHaveBeenCalledWith("/");
  });

  it("selects the instance and reloads to the target path carrying the instance query", async () => {
    select.mockResolvedValue({ href: "/" });

    await switchInstance("inst-1", "/t/task-7");

    expect(select).toHaveBeenCalledWith("inst-1");
    expect(assign).toHaveBeenCalledWith("/t/task-7?kandev_instance=inst-1");
  });

  it("clears the selection when switching back to the local instance", async () => {
    select.mockResolvedValue({ href: "/" });

    await switchInstance("local");

    expect(select).toHaveBeenCalledWith("local");
    expect(assign).toHaveBeenCalledWith("/");
  });

  it("reloads a local switch to the target path without an instance query pin", async () => {
    select.mockResolvedValue({ href: "/" });

    await switchInstance("local", "/t/task-7");

    expect(select).toHaveBeenCalledWith("local");
    expect(assign).toHaveBeenCalledWith("/t/task-7");
  });
});

// ---- remote deep-link query joining (cross-instance inbox) ----

it("appends the instance pin with & when the target path already carries a query", async () => {
  select.mockResolvedValue({ href: "/" });

  await switchInstance("inst-1", "/t/task-7?sessionId=s1");

  expect(assign).toHaveBeenCalledWith("/t/task-7?sessionId=s1&kandev_instance=inst-1");
});
