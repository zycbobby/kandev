import { fireEvent, render, screen } from "@testing-library/react";
import { type ReactElement, type ReactNode, cloneElement } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { WorkflowSelectorRow } from "./workflow-selector-row";

let capturedContentClassName: string | undefined;

vi.mock("@kandev/ui/popover", () => ({
  Popover: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  PopoverTrigger: ({ children }: { children: ReactElement<{ onClick?: () => void }> }) =>
    cloneElement(children, { onClick: () => capturedContentClassName !== undefined }),
  PopoverContent: ({ children, className }: { children: ReactNode; className?: string }) => {
    capturedContentClassName = className;
    return <div>{children}</div>;
  },
}));

vi.mock("@kandev/ui/tooltip", () => ({
  Tooltip: ({ children }: { children: ReactNode }) => <>{children}</>,
  TooltipTrigger: ({ children }: { children: ReactNode }) => <>{children}</>,
  TooltipContent: ({ children }: { children: ReactNode }) => <>{children}</>,
}));

afterEach(() => {
  capturedContentClassName = undefined;
});

const workflows = Array.from({ length: 10 }, (_, i) => ({
  id: `wf-${i}`,
  name: `Workflow ${i}`,
}));

describe("WorkflowSelectorRow popover scroll constraint", () => {
  it("constrains the popover list height and enables vertical scroll", () => {
    render(
      <WorkflowSelectorRow
        workflows={workflows}
        snapshots={{}}
        selectedWorkflowId={null}
        onWorkflowChange={vi.fn()}
        agentProfiles={[]}
      />,
    );
    fireEvent.click(screen.getByTestId("workflow-selector-trigger"));
    expect(capturedContentClassName).toContain("overflow-y-auto");
    expect(capturedContentClassName).toContain("max-h-");
  });
});
