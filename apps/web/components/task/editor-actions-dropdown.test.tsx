import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const remote = vi.hoisted(() => ({ value: false }));

vi.mock("@/hooks/domains/instances/use-is-remote-instance", () => ({
  useIsRemoteInstance: () => remote.value,
}));

const action = vi.hoisted(() => ({
  open: vi.fn(),
}));

vi.mock("@/hooks/use-task-folder-action", () => ({
  useTaskFolderAction: () => ({
    options: [],
    pickerOpen: false,
    isLoading: false,
    disabled: remote.value,
    open: action.open,
    select: vi.fn(),
    onOpenChange: vi.fn(),
    onCloseAutoFocus: vi.fn(),
  }),
}));

vi.mock("./task-folder-picker", () => ({ TaskFolderPicker: () => null }));

// Radix dropdown primitives rely on pointer/portal behaviour that jsdom does
// not model well; render plain elements so `onSelect` fires on click.
vi.mock("@kandev/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuItem: ({
    children,
    onSelect,
    disabled,
  }: {
    children: React.ReactNode;
    onSelect?: () => void;
    disabled?: boolean;
  }) => (
    <button type="button" disabled={disabled} onClick={() => onSelect?.()}>
      {children}
    </button>
  ),
  DropdownMenuSeparator: () => <hr />,
}));

import { EditorActionsDropdown } from "./editor-actions-dropdown";

function view() {
  return (
    <EditorActionsDropdown sessionId="s1">
      <button type="button">entry</button>
    </EditorActionsDropdown>
  );
}

beforeEach(() => {
  remote.value = false;
  action.open.mockReset();
});

afterEach(cleanup);

describe("EditorActionsDropdown host-bound degradation", () => {
  it("keeps the open-folder action enabled for the local instance", () => {
    render(view());

    const item = screen.getByRole("button", { name: /Open folder/ });
    expect((item as HTMLButtonElement).disabled).toBe(false);
    expect(item.textContent).not.toContain("Unavailable for remote instances");
  });

  it("disables the open-folder action with an explanation for a remote instance", () => {
    remote.value = true;
    render(view());

    const item = screen.getByRole("button", { name: /Open folder/ }) as HTMLButtonElement;
    expect(item.disabled).toBe(true);
    fireEvent.click(item);
    expect(action.open).not.toHaveBeenCalled();
    expect(item.textContent).toContain("Unavailable for remote instances");
  });
});
