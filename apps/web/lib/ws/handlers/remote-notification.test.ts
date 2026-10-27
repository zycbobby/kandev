import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { create } from "zustand";
import { immer } from "zustand/middleware/immer";
import type { StoreApi } from "zustand";
import type { AppState } from "@/lib/state/store";
import type { BackendMessageMap } from "@/lib/types/backend";
import { LOCAL_INSTANCE_ID } from "@/lib/state/slices/instances/types";
import { createRemoteNotificationsSlice } from "@/lib/state/slices/remote-notifications/remote-notifications-slice";
import type {
  RemoteNotificationEntry,
  RemoteNotificationsSlice,
} from "@/lib/state/slices/remote-notifications/types";
import {
  REMOTE_NOTIFICATION_ACTION,
  registerRemoteNotificationHandlers,
} from "./remote-notification";

// D9 red line: the remote-notification module never reaches for the local
// delivery channels. These mocks exist so the suite can assert zero calls.
vi.mock("@/lib/notifications/sound", () => ({
  playWaitingForInputSound: vi.fn(),
}));
vi.mock("@/lib/toast/sonner", () => ({
  toast: vi.fn(),
  createPluginToast: vi.fn(),
}));

import { playWaitingForInputSound } from "@/lib/notifications/sound";
import { toast } from "@/lib/toast/sonner";

const notificationConstructor = vi.fn();

type HandlerStoreState = RemoteNotificationsSlice & {
  instances: { activeId: string };
};

function makeStore(activeInstanceId: string = LOCAL_INSTANCE_ID): StoreApi<AppState> {
  return create<HandlerStoreState>()(
    immer((set) => ({
      instances: { activeId: activeInstanceId },
      ...createRemoteNotificationsSlice(set),
    })),
  ) as unknown as StoreApi<AppState>;
}

function remoteNotificationMessage(
  payloadOverrides: Record<string, unknown> = {},
): BackendMessageMap["remote_notification"] {
  return {
    type: "notification",
    action: REMOTE_NOTIFICATION_ACTION,
    payload: {
      id: "row-1",
      instance_id: "inst-1",
      event: "session.turn_finished",
      occurrence_id: "occ-1",
      task_id: "task-9",
      session_id: "session-9",
      title: "Turn finished",
      body: "The agent finished a turn.",
      created_at: "2026-10-28T00:00:00Z",
      read: false,
      dismissed: false,
      ...payloadOverrides,
    },
  } as BackendMessageMap["remote_notification"];
}

function handler(store: StoreApi<AppState>) {
  const handlers = registerRemoteNotificationHandlers(store) as Record<
    string,
    (message: BackendMessageMap["remote_notification"]) => void
  >;
  return handlers[REMOTE_NOTIFICATION_ACTION]!;
}

function entriesOf(store: StoreApi<AppState>): RemoteNotificationEntry[] {
  return (store.getState() as unknown as HandlerStoreState).remoteNotifications.entries;
}

function expectNoLocalDelivery() {
  expect(playWaitingForInputSound).not.toHaveBeenCalled();
  expect(toast).not.toHaveBeenCalled();
  expect(notificationConstructor).not.toHaveBeenCalled();
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal(
    "Notification",
    Object.assign(notificationConstructor, { permission: "granted" as NotificationPermission }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("remote_notification handler", () => {
  it("registers exactly one handler under the remote_notification action", () => {
    const handlers = registerRemoteNotificationHandlers(makeStore()) as Record<string, unknown>;
    expect(Object.keys(handlers)).toEqual([REMOTE_NOTIFICATION_ACTION]);
  });

  it("writes a received frame into the remote-notifications slice", () => {
    const store = makeStore();

    handler(store)(remoteNotificationMessage());

    expect(entriesOf(store)).toEqual([
      expect.objectContaining({
        id: "row-1",
        instanceId: "inst-1",
        taskId: "task-9",
        title: "Turn finished",
        read: false,
      }),
    ]);
  });

  it("keeps one entry when the same occurrence frame arrives twice", () => {
    const store = makeStore();
    const invoke = handler(store);

    invoke(remoteNotificationMessage());
    invoke(remoteNotificationMessage());
    invoke(remoteNotificationMessage({ read: true }));

    expect(entriesOf(store)).toHaveLength(1);
    expect(entriesOf(store)[0]).toMatchObject({ id: "row-1", read: true });
  });

  it("produces no toast, browser notification, or sound for a remote event", () => {
    const store = makeStore();

    handler(store)(remoteNotificationMessage());

    expect(entriesOf(store)).toHaveLength(1);
    expectNoLocalDelivery();
  });

  it("ignores frames while the panel views a remote instance", () => {
    const store = makeStore("inst-2");

    handler(store)(remoteNotificationMessage());

    expect(entriesOf(store)).toHaveLength(0);
    expectNoLocalDelivery();
  });
});
