import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AppState } from "@/lib/state/store";
import { NOTIFICATION_EVENT_TASK_MANUAL_STEP_ENTERED } from "@/lib/notifications/events";
import type { SemanticNotificationMessage } from "./notifications.test-helpers";
import {
  MESSAGE_BODY,
  MESSAGE_TITLE,
  OTHER_TASK_ID,
  SESSION_ID,
  TASK_ID,
  getHandler,
  makeMessage,
  makeStore,
  notificationMock,
} from "./notifications.test-helpers";

vi.mock("@/lib/notifications/sound", () => ({
  playWaitingForInputSound: vi.fn(),
}));

import { playWaitingForInputSound } from "@/lib/notifications/sound";

describe("manual step wait notification handler", () => {
  const MANUAL_STEP_TITLE = "Task waiting on you";
  const MANUAL_STEP_BODY = 'The task "Task one" moved to a step that waits for you.';

  beforeEach(() => {
    vi.clearAllMocks();
    vi.stubGlobal(
      "Notification",
      Object.assign(notificationMock, { permission: "granted" as NotificationPermission }),
    );
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    delete (window as Window & { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;
  });

  it("plays the waiting sound and shows a notification with no open action", () => {
    const handler = getHandler(makeStore(), NOTIFICATION_EVENT_TASK_MANUAL_STEP_ENTERED);
    handler(
      makeMessage(
        "mw-1",
        NOTIFICATION_EVENT_TASK_MANUAL_STEP_ENTERED,
        MANUAL_STEP_TITLE,
        MANUAL_STEP_BODY,
        "mw:task-1:step-review:7",
      ),
    );

    // The handler is the generic attention registration: sound plus a native
    // notification, and deliberately no navigation/open path (D8).
    expect(playWaitingForInputSound).toHaveBeenCalledTimes(1);
    expect(notificationMock).toHaveBeenCalledWith(MANUAL_STEP_TITLE, {
      body: MANUAL_STEP_BODY,
    });
  });

  it("uses the episode occurrence as the native identity, like a clarification", () => {
    const invoke = vi.fn().mockResolvedValue("shown");
    (window as Window & { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__ = {
      invoke,
      transformCallback: vi.fn(),
    };
    const handler = getHandler(makeStore(), NOTIFICATION_EVENT_TASK_MANUAL_STEP_ENTERED);
    const occurrenceID = "mw:task-1:step-review:7";
    // The local provider stamps the frame id with the occurrence id, so both
    // convergences of one episode share the native notification identity.
    const message = () =>
      makeMessage(
        occurrenceID,
        NOTIFICATION_EVENT_TASK_MANUAL_STEP_ENTERED,
        MANUAL_STEP_TITLE,
        MANUAL_STEP_BODY,
        occurrenceID,
      );

    handler(message());
    handler(message());

    expect(invoke).toHaveBeenCalledTimes(2);
    expect(invoke).toHaveBeenNthCalledWith(1, "show_native_notification", {
      request: {
        eventId: `${NOTIFICATION_EVENT_TASK_MANUAL_STEP_ENTERED}:${occurrenceID}`,
        title: MANUAL_STEP_TITLE,
        body: MANUAL_STEP_BODY,
        taskId: TASK_ID,
        sessionId: SESSION_ID,
      },
    });
    expect(invoke).toHaveBeenNthCalledWith(2, "show_native_notification", {
      request: {
        eventId: `${NOTIFICATION_EVENT_TASK_MANUAL_STEP_ENTERED}:${occurrenceID}`,
        title: MANUAL_STEP_TITLE,
        body: MANUAL_STEP_BODY,
        taskId: TASK_ID,
        sessionId: SESSION_ID,
      },
    });
  });

  it("suppresses both channels while the user is viewing the task", () => {
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    vi.spyOn(document, "hasFocus").mockReturnValue(true);
    const store = makeStore({ tasks: { activeTaskId: TASK_ID } } as unknown as Partial<AppState>);

    getHandler(
      store,
      NOTIFICATION_EVENT_TASK_MANUAL_STEP_ENTERED,
    )(
      makeMessage(
        "mw-2",
        NOTIFICATION_EVENT_TASK_MANUAL_STEP_ENTERED,
        MANUAL_STEP_TITLE,
        MANUAL_STEP_BODY,
      ),
    );

    expect(playWaitingForInputSound).not.toHaveBeenCalled();
    expect(notificationMock).not.toHaveBeenCalled();
  });

  it("falls back to the settings event labels when the backend sends no copy", () => {
    getHandler(
      makeStore(),
      NOTIFICATION_EVENT_TASK_MANUAL_STEP_ENTERED,
    )(makeMessage("mw-3", NOTIFICATION_EVENT_TASK_MANUAL_STEP_ENTERED, "", ""));

    expect(notificationMock).toHaveBeenCalledWith("Task waiting on you", {
      body: "Notify when a task moves to a step that waits for you.",
    });
  });
});

describe("relayed notification origin handling", () => {
  const ORIGIN_INSTANCE_ID = "inst-1";
  const ORIGIN_INSTANCE_NAME = "Remote Studio";

  beforeEach(() => {
    vi.clearAllMocks();
    vi.stubGlobal(
      "Notification",
      Object.assign(notificationMock, { permission: "granted" as NotificationPermission }),
    );
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    delete (window as Window & { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__;
  });

  function makeRelayedMessage(
    id = "relayed-1",
    action:
      | "session.clarification_requested"
      | "task.manual_step_entered" = "session.clarification_requested",
  ): SemanticNotificationMessage {
    return {
      ...makeMessage(id, action),
      payload: {
        ...makeMessage(id, action).payload,
        origin_instance_id: ORIGIN_INSTANCE_ID,
        origin_instance_name: ORIGIN_INSTANCE_NAME,
      },
    } as SemanticNotificationMessage;
  }

  it("labels the notification with the origin instance and offers no open action", () => {
    const invoke = vi.fn().mockResolvedValue("shown");
    (window as Window & { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__ = {
      invoke,
      transformCallback: vi.fn(),
    };

    getHandler(makeStore())(makeRelayedMessage());

    // The origin instance is the label; the request carries no open action or
    // navigation surface beyond the notification itself (navigation is by
    // Inbox row only, D8).
    expect(invoke).toHaveBeenCalledWith("show_native_notification", {
      request: {
        eventId: `session.clarification_requested:relayed-1`,
        title: `[${ORIGIN_INSTANCE_NAME}] ${MESSAGE_TITLE}`,
        body: MESSAGE_BODY,
        taskId: TASK_ID,
        sessionId: SESSION_ID,
      },
    });
    expect(playWaitingForInputSound).toHaveBeenCalledTimes(1);
  });

  it("labels the browser fallback notification with the origin instance", () => {
    getHandler(makeStore())(makeRelayedMessage());

    expect(notificationMock).toHaveBeenCalledWith(`[${ORIGIN_INSTANCE_NAME}] ${MESSAGE_TITLE}`, {
      body: MESSAGE_BODY,
    });
  });

  it("keeps local notifications unlabeled when no origin is present", () => {
    getHandler(makeStore())(makeMessage());

    expect(notificationMock).toHaveBeenCalledWith(MESSAGE_TITLE, { body: MESSAGE_BODY });
  });

  it("delivers a relayed notification while the panel views the local instance", () => {
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    vi.spyOn(document, "hasFocus").mockReturnValue(true);
    const store = makeStore({ tasks: { activeTaskId: TASK_ID } } as unknown as Partial<AppState>);

    getHandler(store)(makeRelayedMessage());

    expect(playWaitingForInputSound).toHaveBeenCalledTimes(1);
    expect(notificationMock).toHaveBeenCalledTimes(1);
  });

  it("suppresses a relayed notification while viewing that task on its origin instance", () => {
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    vi.spyOn(document, "hasFocus").mockReturnValue(true);
    const store = makeStore({
      tasks: { activeTaskId: TASK_ID },
      instances: { activeId: ORIGIN_INSTANCE_ID },
    } as unknown as Partial<AppState>);

    getHandler(store)(makeRelayedMessage());

    expect(playWaitingForInputSound).not.toHaveBeenCalled();
    expect(notificationMock).not.toHaveBeenCalled();
  });

  it("delivers a relayed notification for a different task on the origin instance", () => {
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    vi.spyOn(document, "hasFocus").mockReturnValue(true);
    const store = makeStore({
      tasks: { activeTaskId: OTHER_TASK_ID },
      instances: { activeId: ORIGIN_INSTANCE_ID },
    } as unknown as Partial<AppState>);

    getHandler(store)(makeRelayedMessage());

    expect(playWaitingForInputSound).toHaveBeenCalledTimes(1);
    expect(notificationMock).toHaveBeenCalledTimes(1);
  });
});
