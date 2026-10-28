import { act, render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { StateProvider, useAppStoreApi } from "@/components/state-provider";
import type { HydrationState } from "@/lib/state/store";

vi.mock("@/hooks/domains/failed-inbox/use-failed-inbox-controller", () => ({
  useFailedInboxController: () => {},
}));

import { NeedsYouInboxPageClient } from "@/app/needs-you-inbox/needs-you-inbox-page-client";
import { NeedsYouInboxBridge } from "@/components/needs-you-inbox/needs-you-inbox-bridge";
function Probe() {
  const api = useAppStoreApi();
  return (
    <>
      <NeedsYouInboxBridge />
      <button
        type="button"
        onClick={() => {
          const state = api.getState();
          state.setInstancesLoaded([
            {
              id: "i1",
              name: "R",
              base_url: "http://r",
              enabled: true,
              created_at: "",
              updated_at: "",
            },
          ]);
          const generation = state.beginRemoteInboxRead("i1");
          state.setRemoteInboxDegraded("i1", generation, "inbox_disabled");
        }}
      >
        seed
      </button>
      <NeedsYouInboxPageClient />
    </>
  );
}

/** Regression: a zustand selector returning Object.values() fed straight into
 * useAppStore made the route re-render forever (React error #185, surfaced in
 * e2e as the route error boundary). The page must render with the bridge
 * mounted and a degraded remote instance page landed without looping. */
describe("Needs-you Inbox route update-loop regression", () => {
  it("renders with a degraded remote instance without an update loop", async () => {
    const { getByText, getByTestId } = render(
      <StateProvider
        initialState={
          { workspaces: { activeId: "w1", byId: {}, allIds: [] } } as unknown as HydrationState
        }
      >
        <Probe />
      </StateProvider>,
    );
    await act(async () => {
      getByText("seed").click();
    });
    expect(getByTestId("remote-inbox-degraded")).not.toBeNull();
  });
});
