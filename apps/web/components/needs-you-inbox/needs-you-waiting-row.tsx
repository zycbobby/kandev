"use client";

import { IconHourglass } from "@tabler/icons-react";
import { Button } from "@kandev/ui/button";
import { useTranslation } from "react-i18next";
import Link from "@/components/routing/app-link";
import { useAppStore } from "@/components/state-provider";
import { toast } from "@/lib/toast/sonner";
import { formatRelativeTime } from "@/lib/i18n/formats";
import { switchInstance } from "@/lib/instances/switch-instance";
import {
  dismissClarificationInboxBundle,
  snoozeClarificationInboxBundle,
} from "@/lib/api/domains/clarification-inbox-api";
import {
  dismissRemoteClarificationBundle,
  snoozeRemoteClarificationBundle,
} from "@/lib/api/domains/remote-inbox-api";
import type { ClarificationInboxSnoozeDuration } from "@/lib/types/clarification-inbox";
import type { NeedsYouInboxWaitingRow } from "@/lib/types/needs-you-inbox";
import { linkToTask } from "@/lib/links";
import { InboxRowActionsMenu, useSidecarAction } from "./inbox-row-actions";

/** Picks the dismiss/snooze targets: remote rows forward through the
 * instance routes, local rows call the workspace-local sidecar APIs. */
function waitingRowSidecarActions(
  source: { instanceId: string; instanceName: string } | undefined,
  rowId: string,
) {
  if (!source) {
    return {
      dismiss: () => dismissClarificationInboxBundle(rowId),
      snooze: (duration: ClarificationInboxSnoozeDuration) =>
        snoozeClarificationInboxBundle(rowId, duration),
    };
  }
  return {
    dismiss: () => dismissRemoteClarificationBundle(source.instanceId, rowId),
    snooze: (duration: ClarificationInboxSnoozeDuration) =>
      snoozeRemoteClarificationBundle(source.instanceId, rowId, duration),
  };
}

/**
 * One manual-step waiting row: a task parked on a workflow step that neither
 * starts work on entry nor completes the task. The row names the task and the
 * step, offers open/dismiss/snooze, and deliberately has no expandable answer
 * surface: nothing is blocking an agent, the task is blocking on the operator.
 *
 * With `source` the row is a remote instance's: the origin label names that
 * instance, opening the task switches the panel there, and sidecar writes
 * forward through the local backend instead of the local sidecar.
 */
export function NeedsYouWaitingRow({
  row,
  source,
}: {
  row: NeedsYouInboxWaitingRow;
  // Present on remote-instance rows; local rows omit it and keep today's
  // behavior byte-for-byte.
  source?: { instanceId: string; instanceName: string };
}) {
  const { t } = useTranslation();
  const bumpRefreshTick = useAppStore((s) => s.bumpNeedsYouInboxRefreshTick);
  const bumpRemoteRefreshTick = useAppStore((s) => s.bumpRemoteInboxRefreshTick);
  const bumpTick = source ? () => bumpRemoteRefreshTick(source.instanceId) : bumpRefreshTick;
  const taskHref = linkToTask(row.task_id);
  // The waiting row's identity is the same mw:<task>:<step>:<transition>
  // string the sidecar writes address, so the existing per-user sidecar
  // endpoints carry dismiss/snooze/restore unchanged.
  const { busy: dismissing, run: runDismiss } = useSidecarAction(row.id, bumpTick, () =>
    toast.error(t("needsYouInbox:waitingDismissFailed")),
  );
  const { busy: snoozing, run: runSnooze } = useSidecarAction(row.id, bumpTick, () =>
    toast.error(t("needsYouInbox:waitingSnoozeFailed")),
  );
  const { dismiss: dismissAction, snooze: snoozeAction } = waitingRowSidecarActions(source, row.id);
  const openTask = source ? () => void switchInstance(source.instanceId, taskHref) : undefined;

  return (
    <div data-testid="needs-you-waiting-row" data-row-id={row.id}>
      <div className="flex items-center gap-3 px-4 py-2.5">
        <span
          className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-muted"
          role="img"
          aria-label={t("needsYouInbox:waitingStatusLabel")}
        >
          <IconHourglass className="h-4 w-4 text-muted-foreground" />
        </span>
        {source && (
          <span
            className="shrink-0 rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground"
            data-testid="needs-you-inbox-instance-chip"
          >
            {source.instanceName}
          </span>
        )}
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-medium">
            {row.task_title || row.task_id || t("needsYouInbox:waitingRowFallbackTitle")}
          </span>
          <span className="block truncate text-xs text-muted-foreground">
            {row.step_name || t("needsYouInbox:waitingRowFallbackStep")}
          </span>
        </span>
        <span className="shrink-0 text-xs text-muted-foreground">
          {formatRelativeTime(row.reference_time)}
        </span>
        {/* Same affordance set as the question row: the task is unreachable
            from the Inbox without it, and it is withheld below `sm` only. */}
        {source ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="hidden shrink-0 cursor-pointer sm:inline-flex"
            onClick={openTask}
            data-testid="needs-you-waiting-open-task"
          >
            {t("needsYouInbox:openTask")}
          </Button>
        ) : (
          <Button
            asChild
            variant="outline"
            size="sm"
            className="hidden shrink-0 cursor-pointer sm:inline-flex"
          >
            <Link href={taskHref} data-testid="needs-you-waiting-open-task">
              {t("needsYouInbox:openTask")}
            </Link>
          </Button>
        )}
        <InboxRowActionsMenu
          disabled={dismissing || snoozing}
          taskHref={taskHref}
          onOpenTask={openTask}
          onDismiss={() => void runDismiss(dismissAction)}
          onSnooze={(duration) => void runSnooze(() => snoozeAction(duration))}
        />
      </div>
    </div>
  );
}
