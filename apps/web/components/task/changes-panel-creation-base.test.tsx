import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { CreationBaseDisplay } from "./changes-panel-creation-base";

afterEach(cleanup);

describe("CreationBaseDisplay", () => {
  it("renders the recorded mother branch names", () => {
    render(<CreationBaseDisplay bases={["main"]} errorCode={null} />);

    expect(screen.getByTestId("changes-creation-base").textContent).toContain("main");
    expect(screen.queryByText("Worktree base unavailable")).toBeNull();
  });

  it("joins multiple repositories with commas", () => {
    render(<CreationBaseDisplay bases={["alpha", "beta"]} errorCode={null} />);

    expect(screen.getByTestId("changes-creation-base").textContent).toContain("alpha, beta");
  });

  it("shows the unavailable note when the error code is set", () => {
    render(<CreationBaseDisplay bases={["main"]} errorCode="creation_base_ref_unavailable" />);

    expect(screen.getByTestId("changes-creation-base").textContent).toContain("main");
    expect(screen.getByText("Worktree base unavailable")).not.toBeNull();
  });

  it("renders nothing without a recorded base", () => {
    const { container } = render(<CreationBaseDisplay bases={[]} errorCode={null} />);

    expect(container.childElementCount).toBe(0);
  });
});
