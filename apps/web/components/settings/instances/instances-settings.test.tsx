import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
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

let multiInstanceFeature = true;

vi.mock("@/hooks/domains/features/use-feature", () => ({
  useFeature: (name: string) => name === "multiInstance" && multiInstanceFeature,
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

const TIMESTAMP = "2026-10-01T00:00:00Z";
const CHECKED_AT = "2026-10-20T10:00:00Z";
const OK_NAME = "Studio workstation";
const NAME_LABEL = "Name";
const EDIT_OK_BUTTON = `Edit instance ${OK_NAME}`;
const REMOVE_OK_BUTTON = `Remove instance ${OK_NAME}`;
const CREATED_BASE_URL = "http://192.168.1.9:43110";
const CREATED_NAME = "Rack server";

function healthState(id: string): string | null {
  return screen.getByTestId(`instance-health-${id}`).getAttribute("data-state");
}

function row(id: string, overrides: Record<string, unknown> = {}) {
  return {
    id,
    name: `Instance ${id}`,
    base_url: `http://192.168.1.${id.length}:43110`,
    enabled: true,
    created_at: TIMESTAMP,
    updated_at: TIMESTAMP,
    ...overrides,
  };
}

const OK_ROW = row("inst-ok", { name: OK_NAME });
const DOWN_ROW = row("inst-down", { name: "Renderer" });
const OLD_ROW = row("inst-old", { name: "Lab box", enabled: false });
const NEW_ROW = row("inst-new", { name: "Fresh install" });

const HEALTH_BY_ID: Record<string, unknown> = {
  "inst-ok": { reachable: true, version: "1.2.3", supported: true, lastCheckedAt: CHECKED_AT },
  "inst-down": {
    reachable: false,
    error: "dial tcp 192.168.1.30:43110: connection refused",
    supported: false,
    lastCheckedAt: CHECKED_AT,
  },
  "inst-old": {
    reachable: true,
    version: "0.9.0",
    error: "remote version 0.9.0 is below the minimum 1.2.0",
    supported: false,
    lastCheckedAt: CHECKED_AT,
  },
  // Zero lastCheckedAt: the poller has not run for this instance yet.
  "inst-new": { reachable: false, supported: false, lastCheckedAt: "0001-01-01T00:00:00Z" },
};

function renderSettings() {
  return render(
    <SettingsSaveProvider>
      <InstancesSettings />
    </SettingsSaveProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  multiInstanceFeature = true;
  mocks.list.mockResolvedValue([OK_ROW, DOWN_ROW, OLD_ROW, NEW_ROW]);
  mocks.health.mockImplementation((id: string) =>
    Promise.resolve(
      HEALTH_BY_ID[id] ?? {
        reachable: false,
        supported: false,
        lastCheckedAt: "0001-01-01T00:00:00Z",
      },
    ),
  );
  mocks.update.mockResolvedValue(OK_ROW);
  mocks.create.mockResolvedValue(NEW_ROW);
  mocks.remove.mockResolvedValue({ success: true });
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe("InstancesSettings feature gate", () => {
  it("renders nothing when the multi-instance feature is off", async () => {
    multiInstanceFeature = false;
    const { container } = renderSettings();

    expect(container.textContent).toBe("");
    await waitFor(() => expect(mocks.list).not.toHaveBeenCalled());
  });
});

describe("InstancesSettings health banners", () => {
  it("renders the registered instances with their health states", async () => {
    renderSettings();

    await screen.findByText(OK_NAME);
    expect(screen.getByText("Renderer")).toBeTruthy();
    expect(screen.getByText("Lab box")).toBeTruthy();

    expect(healthState("inst-ok")).toBe("ok");
    expect(screen.getByTestId("instance-health-inst-ok").textContent).toContain("version 1.2.3");

    expect(healthState("inst-down")).toBe("degraded");
    expect(screen.getByTestId("instance-health-inst-down").textContent).toContain(
      "connection refused",
    );

    expect(healthState("inst-old")).toBe("unsupported");
    expect(screen.getByTestId("instance-health-inst-old").textContent).toContain("0.9.0");

    expect(healthState("inst-new")).toBe("waiting");
  });

  it("marks disabled instances in the list", async () => {
    renderSettings();

    await screen.findByTestId("instance-row-inst-old");
    expect(screen.getByTestId("instance-row-inst-old").textContent).toContain("Disabled");
    expect(screen.getByTestId("instance-row-inst-ok").textContent).not.toContain("Disabled");
  });
});

describe("InstancesSettings save flow", () => {
  it("stages an edit and saves it through the shared save contributor", async () => {
    mocks.update.mockResolvedValue({ ...OK_ROW, name: "Studio workstation 2" });
    renderSettings();

    fireEvent.click(await screen.findByRole("button", { name: EDIT_OK_BUTTON }));

    const nameInput = screen.getByLabelText(NAME_LABEL);
    expect((nameInput as HTMLInputElement).value).toBe(OK_NAME);
    fireEvent.change(nameInput, { target: { value: "Studio workstation 2" } });

    // The draft is staged: the shared floating bar owns the save action.
    expect(mocks.update).not.toHaveBeenCalled();
    await waitFor(() =>
      expect(screen.getByTestId("settings-floating-save").getAttribute("data-status")).toBe(
        "dirty",
      ),
    );

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(mocks.update).toHaveBeenCalledTimes(1));
    expect(mocks.update).toHaveBeenCalledWith(
      "inst-ok",
      {
        name: "Studio workstation 2",
        base_url: OK_ROW.base_url,
        credential_secret_id: "",
        enabled: true,
      },
      { cache: "no-store" },
    );
    await waitFor(() => expect(screen.queryByLabelText("Name")).toBeNull());
    await screen.findByText(`${OK_NAME} 2`);
  });

  it("discards a staged edit back to the saved baseline", async () => {
    renderSettings();

    fireEvent.click(await screen.findByRole("button", { name: EDIT_OK_BUTTON }));
    fireEvent.change(screen.getByLabelText(NAME_LABEL), {
      target: { value: "Renamed draft" },
    });

    fireEvent.click(screen.getByRole("button", { name: "Reset" }));

    const nameInput = await waitFor(() => screen.getByLabelText(NAME_LABEL));
    expect((nameInput as HTMLInputElement).value).toBe(OK_NAME);
    expect(mocks.update).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByTestId("settings-floating-save")).toBeNull());
  });

  it("creates a new instance through the shared save contributor", async () => {
    mocks.create.mockResolvedValue(
      row("inst-created", { name: CREATED_NAME, base_url: CREATED_BASE_URL }),
    );
    renderSettings();

    fireEvent.click(await screen.findByRole("button", { name: "Add instance" }));
    fireEvent.change(screen.getByLabelText(NAME_LABEL), { target: { value: CREATED_NAME } });
    fireEvent.change(screen.getByLabelText("Base URL"), {
      target: { value: CREATED_BASE_URL },
    });

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(mocks.create).toHaveBeenCalledTimes(1));
    expect(mocks.create).toHaveBeenCalledWith(
      {
        name: CREATED_NAME,
        base_url: CREATED_BASE_URL,
        credential_secret_id: "",
        enabled: true,
      },
      { cache: "no-store" },
    );
    await screen.findByText(CREATED_NAME);
    await waitFor(() => expect(screen.queryByLabelText("Name")).toBeNull());
  });
});

describe("InstancesSettings removal", () => {
  it("deletes the instance after confirmation", async () => {
    renderSettings();

    fireEvent.click(await screen.findByRole("button", { name: REMOVE_OK_BUTTON }));

    const confirm = await screen.findByTestId("instance-remove-confirm");
    fireEvent.click(confirm);

    await waitFor(() =>
      expect(mocks.remove).toHaveBeenCalledWith("inst-ok", { cache: "no-store" }),
    );
    await waitFor(() => expect(screen.queryByText(OK_NAME)).toBeNull());
    expect(screen.getByText("Renderer")).toBeTruthy();
  });

  it("keeps the instance when removal is cancelled", async () => {
    renderSettings();

    fireEvent.click(await screen.findByRole("button", { name: REMOVE_OK_BUTTON }));
    fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));

    await waitFor(() => expect(mocks.remove).not.toHaveBeenCalled());
    expect(screen.getByText(OK_NAME)).toBeTruthy();
  });
});
