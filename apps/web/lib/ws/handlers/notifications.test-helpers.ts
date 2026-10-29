import { vi } from "vitest";
import type { StoreApi } from "zustand";
import type { AppState } from "@/lib/state/store";
import {
  NOTIFICATION_EVENT_OFFICE_INBOX_ITEM,
  NOTIFICATION_EVENT_SESSION_CLARIFICATION_REQUESTED,
  NOTIFICATION_EVENT_SESSION_TURN_FINISHED,
  NOTIFICATION_EVENT_TASK_MANUAL_STEP_ENTERED,
} from "@/lib/notifications/events";
import type { BackendMessageMap } from "@/lib/types/backend";
import { registerNotificationsHandlers } from "./notifications";

export const TASK_ID = "task-1";
export const SESSION_ID = "session-1";
export const OTHER_TASK_ID = "task-2";
// i18n-exempt: test fixture title, never rendered outside assertions.
export const MESSAGE_TITLE = "Agent needs your answer";
// i18n-exempt: test fixture body, never rendered outside assertions.
export const MESSAGE_BODY = 'The agent asked a question on "Task one".';
export const notificationMock = vi.fn();

export type SemanticEvent =
  | typeof NOTIFICATION_EVENT_SESSION_CLARIFICATION_REQUESTED
  | typeof NOTIFICATION_EVENT_SESSION_TURN_FINISHED
  | typeof NOTIFICATION_EVENT_OFFICE_INBOX_ITEM
  | typeof NOTIFICATION_EVENT_TASK_MANUAL_STEP_ENTERED;

export type SemanticNotificationMessage =
  | BackendMessageMap["session.clarification_requested"]
  | BackendMessageMap["session.turn_finished"]
  | BackendMessageMap["office.inbox_item"]
  | BackendMessageMap["task.manual_step_entered"];

export function makeStore(overrides: Partial<AppState> = {}) {
  const state = {
    turns: { bySession: { [SESSION_ID]: [{ id: "turn-1" }] } },
    tasks: { activeTaskId: OTHER_TASK_ID },
    ...overrides,
  } as unknown as AppState;

  return { getState: () => state } as unknown as StoreApi<AppState>;
}

export function makeMessage(
  id = "message-1",
  action: SemanticEvent = NOTIFICATION_EVENT_SESSION_CLARIFICATION_REQUESTED,
  title = MESSAGE_TITLE,
  body = MESSAGE_BODY,
  occurrenceId = "pending-1",
): SemanticNotificationMessage {
  return {
    id,
    type: "notification",
    action,
    payload: {
      task_id: TASK_ID,
      session_id: SESSION_ID,
      occurrence_id: occurrenceId,
      title,
      body,
    },
  } as SemanticNotificationMessage;
}

export function getHandler(
  store: StoreApi<AppState>,
  action: SemanticEvent = NOTIFICATION_EVENT_SESSION_CLARIFICATION_REQUESTED,
) {
  return (
    registerNotificationsHandlers(store) as unknown as Record<
      string,
      (message: SemanticNotificationMessage) => void
    >
  )[action]!;
}
