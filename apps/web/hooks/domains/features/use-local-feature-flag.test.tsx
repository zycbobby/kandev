import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { defaultFeatureFlags, type FeatureFlags } from "@/lib/state/slices/features/types";

const flags = vi.hoisted(() => ({
  local: null as FeatureFlags | null,
  hydrated: false,
  fetch: vi.fn(),
}));

vi.mock("@/lib/api/domains/features-api", () => ({
  fetchFeatureFlags: flags.fetch,
}));

flags.fetch.mockImplementation(async () => {
  if (flags.local === null) throw new Error("local flags not configured for this test");
  return flags.local;
});

vi.mock("@/hooks/domains/features/use-feature", () => ({
  useFeature: () => flags.hydrated,
}));

import {
  resetLocalFeatureFlagsForTests,
  useLocalFeatureFlag,
} from "@/hooks/domains/features/use-local-feature-flag";

const PROBE = "flag-probe";

function FlagProbe() {
  const enabled = useLocalFeatureFlag("multiInstance");
  return <div data-testid={PROBE}>{enabled ? "on" : "off"}</div>;
}

beforeEach(() => {
  flags.local = null;
  flags.hydrated = false;
  flags.fetch.mockClear();
  resetLocalFeatureFlagsForTests();
});

afterEach(cleanup);

describe("useLocalFeatureFlag", () => {
  it("renders the hydrated flag first and then the local backend's value", async () => {
    // The remote view's app-state says the flag is off; the local backend
    // says it is on. The local value must win so a remote view cannot hide
    // local-only surfaces behind the remote's own flag state.
    flags.hydrated = false;
    flags.local = { ...defaultFeatureFlags, multiInstance: true };

    render(<FlagProbe />);
    expect(screen.getByTestId(PROBE).textContent).toBe("off");

    await waitFor(() => expect(screen.getByTestId(PROBE).textContent).toBe("on"));
  });

  it("reports the flag off when the local backend has it off", async () => {
    flags.hydrated = true;
    flags.local = { ...defaultFeatureFlags, multiInstance: false };

    render(<FlagProbe />);

    await waitFor(() => expect(screen.getByTestId(PROBE).textContent).toBe("off"));
  });

  it("fails closed when the local features read fails", async () => {
    flags.hydrated = true;
    flags.local = null; // the mocked fetchFeatureFlags rejects

    render(<FlagProbe />);

    await waitFor(() => expect(screen.getByTestId(PROBE).textContent).toBe("off"));
  });

  it("reads the local features once per app session", async () => {
    flags.local = { ...defaultFeatureFlags, multiInstance: true };

    const first = render(<FlagProbe />);
    await waitFor(() => expect(screen.getByTestId(PROBE).textContent).toBe("on"));
    first.unmount();

    const second = render(<FlagProbe />);
    await waitFor(() => expect(screen.getByTestId(PROBE).textContent).toBe("on"));
    second.unmount();

    expect(flags.fetch).toHaveBeenCalledTimes(1);
  });
});
