import { isTerminalToolCallStatus } from "@/lib/utils/tool-call-status";
import { normalizeToolCallStatus } from "./tool-status";
import type { SubagentTaskPayload, ToolCallMetadata } from "@/components/task/chat/types";

const LIVE_SUBAGENT_PAYLOAD_STATUSES = new Set(["pendingInit", "started", "async_launched"]);

// Mirrors backend subagentStatusTerminal: Codex session statuses that mean the
// nested task has already settled, even while the parent turn is still active.
const TERMINAL_SUBAGENT_PAYLOAD_STATUSES = new Set([
  "completed",
  "failed",
  "cancelled",
  "errored",
  "interrupted",
  "shutdown",
  "notFound",
]);

function isSubagentPayloadTerminal(status: string | undefined): boolean {
  if (!status) return false;
  return TERMINAL_SUBAGENT_PAYLOAD_STATUSES.has(status) || isTerminalToolCallStatus(status);
}

function isSubagentPayloadLive(status: string | undefined): boolean {
  if (!status) return false;
  if (LIVE_SUBAGENT_PAYLOAD_STATUSES.has(status)) return true;
  return !isSubagentPayloadTerminal(status);
}

export type SubagentTerminalStatus = "complete" | "error" | "cancelled";

function normalizeSubagentTerminalStatus(
  status: string | undefined,
): SubagentTerminalStatus | undefined {
  if (status === "errored" || status === "notFound") return "error";
  if (status === "interrupted" || status === "shutdown") return "cancelled";

  const normalized = normalizeToolCallStatus(status);
  if (normalized === "complete" || normalized === "error" || normalized === "cancelled") {
    return normalized;
  }
  return undefined;
}

export function resolveSubagentTerminalStatus(
  metadataStatus: ToolCallMetadata["status"] | undefined,
  payloadStatus: string | undefined,
): SubagentTerminalStatus {
  const payloadTerminal = normalizeSubagentTerminalStatus(payloadStatus);
  const metadataTerminal = normalizeSubagentTerminalStatus(metadataStatus);
  if (payloadTerminal && payloadTerminal !== "complete") return payloadTerminal;
  if (metadataTerminal && metadataTerminal !== "complete") return metadataTerminal;
  return payloadTerminal ?? metadataTerminal ?? "complete";
}

export function isSubagentEffectivelyActive(
  metadata: ToolCallMetadata | undefined,
  isContainingTurnActive: boolean,
): boolean {
  const payloadStatus = metadata?.normalized?.subagent_task?.status;
  if (payloadStatus && isSubagentPayloadTerminal(payloadStatus)) return false;

  const status = metadata?.status;
  if (status === "running") return true;

  const normalized = normalizeToolCallStatus(status);
  if (normalized === "error" || normalized === "cancelled") return false;

  if (!isContainingTurnActive) return false;

  if (status === "in_progress" || isSubagentPayloadLive(payloadStatus)) return true;
  return false;
}

// A backgrounded subagent is still running out-of-band: the Task tool call
// itself reached its terminal dispatch state, but the agent-level status
// (`async_launched`, or `is_async` with no terminal agent result yet) is the
// only per-card truth that the subagent itself has not settled.
export function isSubagentBackgroundRunning(payload: SubagentTaskPayload | undefined): boolean {
  if (!payload || isSubagentPayloadTerminal(payload.status)) return false;
  return payload.status === "async_launched" || payload.is_async === true;
}

export type SubagentDisplayStatus =
  | { kind: "background" } // spinner + explicit background-running label
  | { kind: "working" } // spinner + the generic working label (synchronous path)
  | { kind: "terminal"; terminal: SubagentTerminalStatus };

// The order is the semantics: background wins regardless of tool status or
// turn activity, then the turn-scoped working state, then the terminal icon.
export function resolveSubagentDisplayStatus(
  metadata: ToolCallMetadata | undefined,
  isContainingTurnActive: boolean,
): SubagentDisplayStatus {
  const payload = metadata?.normalized?.subagent_task;
  if (isSubagentBackgroundRunning(payload)) return { kind: "background" };
  if (isSubagentEffectivelyActive(metadata, isContainingTurnActive)) return { kind: "working" };
  return {
    kind: "terminal",
    terminal: resolveSubagentTerminalStatus(metadata?.status, payload?.status),
  };
}
