import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SettingsSaveProvider } from "@/components/settings/settings-save-provider";

const mocks = vi.hoisted(() => ({
  list: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
  remove: vi.fn(),
  health: vi.fn(),
  select: vi.fn(),
  toast: vi.fn(),
}));

vi.mock("@/hooks/domains/features/use-feature", () => ({
  useFeature: (name: string) => name === "multiInstance",
}));

vi.mock("@/components/toast-provider", () => ({
  useToast: () => ({ toast: mocks.toast }),
}));

vi.mock("@/lib/api/domains/instances-api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/domains/instances-api")>()),
  listInstances: mocks.list,
  createInstance: mocks.create,
  updateInstance: mocks.update,
  deleteInstance: mocks.remove,
  getInstanceHealth: mocks.health,
  selectInstance: mocks.select,
}));

import { InstancesSettings } from "./instances-settings";

// Single-test file, phone viewport: the responsive-breakpoint hook caches the
// first read per module load, so the 390px window must be in place before the
// first render (same shape as jira-action-bar.test.tsx).
afterEach(() => {
  cleanup();
  Object.defineProperty(window, "innerWidth", { configurable: true, value: 1024 });
});

it("removes an instance through a phone bottom sheet at 390px", async () => {
  Object.defineProperty(window, "innerWidth", { configurable: true, value: 390 });
  mocks.list.mockResolvedValue([
    {
      id: "inst-1",
      name: "Studio workstation",
      base_url: "http://192.168.1.20:43110",
      enabled: true,
      created_at: "2026-10-01T00:00:00Z",
      updated_at: "2026-10-01T00:00:00Z",
    },
  ]);
  mocks.health.mockResolvedValue({
    reachable: true,
    version: "1.2.3",
    supported: true,
    lastCheckedAt: "2026-10-20T10:00:00Z",
  });
  mocks.remove.mockResolvedValue({ success: true });

  render(
    <SettingsSaveProvider>
      <InstancesSettings />
    </SettingsSaveProvider>,
  );

  const trigger = await screen.findByTestId("instance-delete-button");
  fireEvent.click(trigger);

  const sheet = screen.getByRole("dialog");
  expect(sheet.getAttribute("data-slot")).toBe("drawer-content");

  // Cancel first: the sheet closes without deleting.
  fireEvent.click(within(sheet).getByRole("button", { name: "Cancel" }));
  await waitFor(() => expect(mocks.remove).not.toHaveBeenCalled());

  fireEvent.click(trigger);
  fireEvent.click(screen.getByTestId("instance-remove-confirm"));
  await waitFor(() => expect(mocks.remove).toHaveBeenCalledWith("inst-1", { cache: "no-store" }));
  await waitFor(() => expect(screen.queryByText("Studio workstation")).toBeNull());
});
