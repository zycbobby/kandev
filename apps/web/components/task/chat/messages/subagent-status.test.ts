import { describe, expect, it } from "vitest";
import type { ToolCallMetadata } from "@/components/task/chat/types";
import { isSubagentEffectivelyActive } from "./subagent-status";

const COMPLETE = "complete";
const STARTED = "started";
const QUEUED = "queued";

function subagentMetadata(
  status: ToolCallMetadata["status"] | undefined,
  payloadStatus: string | undefined,
): ToolCallMetadata {
  const metadata: ToolCallMetadata = {
    status,
    normalized: { kind: "subagent_task", subagent_task: { status: payloadStatus } },
  };
  if (status === undefined) delete metadata.status;
  return metadata;
}

const EFFECTIVELY_ACTIVE_CASES: Array<{
  name: string;
  metadataStatus: ToolCallMetadata["status"];
  payloadStatus: string;
  isContainingTurnActive: boolean;
  expected: boolean;
}> = [
  {
    name: "in-progress metadata is active during its turn without a started payload",
    metadataStatus: "in_progress",
    payloadStatus: QUEUED,
    isContainingTurnActive: true,
    expected: true,
  },
  {
    name: "in-progress metadata settles with its turn without a started payload",
    metadataStatus: "in_progress",
    payloadStatus: QUEUED,
    isContainingTurnActive: false,
    expected: false,
  },
  {
    name: "started payload with pending metadata is active during its turn",
    metadataStatus: "pending",
    payloadStatus: STARTED,
    isContainingTurnActive: true,
    expected: true,
  },
  {
    name: "started payload with pending metadata settles with its turn",
    metadataStatus: "pending",
    payloadStatus: STARTED,
    isContainingTurnActive: false,
    expected: false,
  },
  {
    name: "started payload without metadata status is active during its turn",
    metadataStatus: undefined,
    payloadStatus: STARTED,
    isContainingTurnActive: true,
    expected: true,
  },
  {
    name: "started payload without metadata status settles with its turn",
    metadataStatus: undefined,
    payloadStatus: STARTED,
    isContainingTurnActive: false,
    expected: false,
  },
  {
    name: "running metadata stays active without a containing-turn signal",
    metadataStatus: "running",
    payloadStatus: QUEUED,
    isContainingTurnActive: false,
    expected: true,
  },
  {
    name: "terminal nested payload overrides running metadata",
    metadataStatus: "running",
    payloadStatus: "errored",
    isContainingTurnActive: true,
    expected: false,
  },
  {
    name: "started payload stays active after spawn completes during its turn",
    metadataStatus: COMPLETE,
    payloadStatus: STARTED,
    isContainingTurnActive: true,
    expected: true,
  },
  {
    name: "pendingInit payload stays active after spawn completes during its turn",
    metadataStatus: COMPLETE,
    payloadStatus: "pendingInit",
    isContainingTurnActive: true,
    expected: true,
  },
  {
    name: "pendingInit payload settles with its turn after spawn completes",
    metadataStatus: COMPLETE,
    payloadStatus: "pendingInit",
    isContainingTurnActive: false,
    expected: false,
  },
  {
    name: "terminal nested payload stays settled after spawn completes",
    metadataStatus: COMPLETE,
    payloadStatus: COMPLETE,
    isContainingTurnActive: true,
    expected: false,
  },
  {
    name: "async_launched payload stays active after spawn completes during its turn",
    metadataStatus: COMPLETE,
    payloadStatus: "async_launched",
    isContainingTurnActive: true,
    expected: true,
  },
  {
    name: "async_launched payload settles with its turn after spawn completes",
    metadataStatus: COMPLETE,
    payloadStatus: "async_launched",
    isContainingTurnActive: false,
    expected: false,
  },
  {
    name: "ACP completed nested payload settles after spawn completes",
    metadataStatus: COMPLETE,
    payloadStatus: "completed",
    isContainingTurnActive: true,
    expected: false,
  },
  {
    name: "failed metadata stays settled during an active turn",
    metadataStatus: "failed",
    payloadStatus: STARTED,
    isContainingTurnActive: true,
    expected: false,
  },
  {
    name: "cancelled metadata stays settled during an active turn",
    metadataStatus: "cancelled",
    payloadStatus: STARTED,
    isContainingTurnActive: true,
    expected: false,
  },
  {
    name: "errored nested payload settles even while the turn is active",
    metadataStatus: "complete",
    payloadStatus: "errored",
    isContainingTurnActive: true,
    expected: false,
  },
  {
    name: "interrupted nested payload settles even while the turn is active",
    metadataStatus: "complete",
    payloadStatus: "interrupted",
    isContainingTurnActive: true,
    expected: false,
  },
  {
    name: "shutdown nested payload settles even while the turn is active",
    metadataStatus: "complete",
    payloadStatus: "shutdown",
    isContainingTurnActive: true,
    expected: false,
  },
  {
    name: "notFound nested payload settles even while the turn is active",
    metadataStatus: "complete",
    payloadStatus: "notFound",
    isContainingTurnActive: true,
    expected: false,
  },
];

describe("isSubagentEffectivelyActive", () => {
  it.each(EFFECTIVELY_ACTIVE_CASES)(
    "$name",
    ({ metadataStatus, payloadStatus, isContainingTurnActive, expected }) => {
      expect(
        isSubagentEffectivelyActive(
          subagentMetadata(metadataStatus, payloadStatus),
          isContainingTurnActive,
        ),
      ).toBe(expected);
    },
  );
});
