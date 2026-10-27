import { isTerminalToolCallStatus } from "@/lib/utils/tool-call-status";
import { normalizeToolCallStatus } from "./tool-status";
import type { ToolCallMetadata } from "@/components/task/chat/types";

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
