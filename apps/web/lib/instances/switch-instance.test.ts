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

  it("switches back to the local instance without selecting a registry row", async () => {
    await switchInstance("local", "/t/task-7");

    expect(select).not.toHaveBeenCalled();
    expect(assign).toHaveBeenCalledWith("/t/task-7?kandev_instance=local");
  });

  it("defaults the local switch target to the app root", async () => {
    await switchInstance("local");

    expect(select).not.toHaveBeenCalled();
    expect(assign).toHaveBeenCalledWith("/?kandev_instance=local");
  });
});
