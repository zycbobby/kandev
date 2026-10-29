// Merged Needs-you Inbox wire types (needs-you-inbox-waiting-rows design
// D12). Mirrors apps/backend/internal/clarification/needs_you_inbox_handlers.go's
// needsYouQuestionRowView / needsYouWaitingRowView / needsYouListResponse /
// needsYouHiddenListResponse.
import type {
  ClarificationInboxBundle,
  ClarificationInboxHiddenBundle,
} from "@/lib/types/clarification-inbox";

export type NeedsYouInboxQuestionRow = ClarificationInboxBundle & {
  kind: "question";
};

// A manual-step waiting row: a task parked on a step that neither starts work
// nor completes the task. No messages and no answer surface; the row's only
// affordances are opening the task and the dismiss/snooze menu.
export type NeedsYouInboxWaitingRow = {
  kind: "waiting";
  id: string;
  task_id: string;
  task_title: string;
  workspace_id: string;
  step_id: string;
  step_name: string;
  reference_time: string;
};

export type NeedsYouInboxRow = NeedsYouInboxQuestionRow | NeedsYouInboxWaitingRow;

export function isNeedsYouWaitingRow(row: NeedsYouInboxRow): row is NeedsYouInboxWaitingRow {
  return row.kind === "waiting";
}

export type NeedsYouInboxPage = {
  rows: NeedsYouInboxRow[];
  count: number;
  hidden_count: number;
  has_more: boolean;
  next_snooze_expiry: string | null;
};

export type NeedsYouInboxHiddenQuestionRow = ClarificationInboxHiddenBundle & {
  kind: "question";
};

export type NeedsYouInboxHiddenWaitingRow = NeedsYouInboxWaitingRow & {
  state: string;
  snooze_until: string | null;
};

export type NeedsYouInboxHiddenRow = NeedsYouInboxHiddenQuestionRow | NeedsYouInboxHiddenWaitingRow;

export type NeedsYouInboxHiddenPage = {
  rows: NeedsYouInboxHiddenRow[];
  count: number;
  total: number;
};
