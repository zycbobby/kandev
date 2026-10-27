import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { EditorOption } from "@/lib/types/http";

const remote = vi.hoisted(() => ({ value: false }));
const hooks = vi.hoisted(() => ({
  openEditor: vi.fn(),
  openFolder: vi.fn(),
}));

vi.mock("@/hooks/domains/instances/use-is-remote-instance", () => ({
  useIsRemoteInstance: () => remote.value,
}));

vi.mock("@/hooks/use-open-session-in-editor", () => ({
  useOpenSessionInEditor: () => ({ open: hooks.openEditor, isLoading: false }),
}));

// The real folder hook reports availability false while viewing a remote
// instance; the mock mirrors that contract so the items under test render the
// same disabled state as production.
vi.mock("@/hooks/use-open-session-folder", () => ({
  useOpenSessionFolder: () => ({
    open: hooks.openFolder,
    available: !remote.value,
    status: "idle",
    isLoading: false,
  }),
}));

const EDITORS: EditorOption[] = [
  {
    id: "editor-1",
    type: "editor",
    name: "Test editor",
    kind: "custom",
    installed: true,
    enabled: true,
  },
];

vi.mock("@/hooks/domains/settings/use-editors", () => ({
  useEditors: () => ({ editors: EDITORS, folderOpeningAvailable: true }),
}));

vi.mock("@/components/state-provider", () => ({
  useAppStore: (select: (state: unknown) => unknown) =>
    select({
      tasks: { activeSessionId: "s1" },
      taskSessions: { items: {} },
      userSettings: { defaultEditorId: "editor-1" },
    }),
}));

// Radix menu items must sit inside a Menu root; render them as plain buttons
// so `onSelect` fires on click and the disabled state is directly assertable.
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

import { FileActionsMenuItems } from "./file-actions-dropdown";

beforeEach(() => {
  remote.value = false;
  hooks.openEditor.mockReset();
  hooks.openFolder.mockReset();
});

afterEach(cleanup);

function view() {
  return <FileActionsMenuItems filePath="apps/web/app.ts" sessionId="s1" />;
}

describe("FileActionsMenuItems host-bound degradation", () => {
  it("opens the editor and folder for the local instance", () => {
    render(view());

    fireEvent.click(screen.getByRole("button", { name: /Test editor/ }));
    expect(hooks.openEditor).toHaveBeenCalledWith(
      expect.objectContaining({ editorId: "editor-1", filePath: "apps/web/app.ts" }),
    );

    const folder = screen.getByRole("button", { name: /Open folder/ }) as HTMLButtonElement;
    expect(folder.disabled).toBe(false);
    fireEvent.click(folder);
    expect(hooks.openFolder).toHaveBeenCalled();
  });

  it("disables the editor and folder actions with an explanation for a remote instance", () => {
    remote.value = true;
    render(view());

    const editorItem = screen.getByRole("button", { name: /Test editor/ }) as HTMLButtonElement;
    expect(editorItem.disabled).toBe(true);
    fireEvent.click(editorItem);
    expect(hooks.openEditor).not.toHaveBeenCalled();

    const folder = screen.getByRole("button", { name: /Open folder/ }) as HTMLButtonElement;
    expect(folder.disabled).toBe(true);
    fireEvent.click(folder);
    expect(hooks.openFolder).not.toHaveBeenCalled();

    expect(screen.getByText("Unavailable for remote instances")).toBeTruthy();
    // Copying the path stays available: it is not a host-bound action.
    expect((screen.getByRole("button", { name: /Copy path/ }) as HTMLButtonElement).disabled).toBe(
      false,
    );
  });
});
