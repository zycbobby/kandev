"use client";

import { useCallback, useState } from "react";
import { IconDotsVertical } from "@tabler/icons-react";
import { Button } from "@kandev/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@kandev/ui/dropdown-menu";
import { useTranslation } from "react-i18next";
import Link from "@/components/routing/app-link";
import type { ClarificationInboxSnoozeDuration } from "@/lib/types/clarification-inbox";

const SNOOZE_DURATIONS: ClarificationInboxSnoozeDuration[] = ["1h", "4h", "24h"];

const SNOOZE_DURATION_LABEL_KEYS: Record<ClarificationInboxSnoozeDuration, string> = {
  "1h": "needsYouInbox:snoozeDuration1h",
  "4h": "needsYouInbox:snoozeDuration4h",
  "24h": "needsYouInbox:snoozeDuration24h",
};

/** One sidecar write's busy flag and serialized runner: a row never fires two
 * concurrent writes and always reports the outcome through exactly one of
 * onDone/onFailed. */
export function useSidecarAction(pendingId: string, onDone: () => void, onFailed: () => void) {
  const [busy, setBusy] = useState(false);
  const run = useCallback(
    async (action: () => Promise<void>) => {
      if (busy) return;
      setBusy(true);
      try {
        await action();
        onDone();
      } catch {
        onFailed();
      } finally {
        setBusy(false);
      }
    },
    [busy, onDone, onFailed],
  );
  return { busy, run };
}

/** The row actions menu shared by question rows and manual-step waiting rows:
 * open the task, dismiss, or snooze. The open-task entry navigates this
 * instance's router on local rows; remote rows pass onOpenTask instead. */
export function InboxRowActionsMenu({
  disabled,
  taskHref,
  onOpenTask,
  onDismiss,
  onSnooze,
}: {
  disabled: boolean;
  taskHref: string;
  // Present when opening the task must not navigate this instance's router
  // (remote rows switch the active instance instead).
  onOpenTask?: () => void;
  onDismiss: () => void;
  onSnooze: (duration: ClarificationInboxSnoozeDuration) => void;
}) {
  const { t } = useTranslation();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="cursor-pointer shrink-0"
          disabled={disabled}
          aria-label={t("needsYouInbox:rowActions")}
        >
          <IconDotsVertical className="h-4 w-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {onOpenTask ? (
          <DropdownMenuItem
            className="cursor-pointer"
            onSelect={onOpenTask}
            data-testid="needs-you-inbox-open-task-menu-item"
          >
            {t("needsYouInbox:openTask")}
          </DropdownMenuItem>
        ) : (
          <DropdownMenuItem asChild className="cursor-pointer">
            <Link href={taskHref} data-testid="needs-you-inbox-open-task-menu-item">
              {t("needsYouInbox:openTask")}
            </Link>
          </DropdownMenuItem>
        )}
        <DropdownMenuItem className="cursor-pointer" onSelect={onDismiss}>
          {t("needsYouInbox:dismiss")}
        </DropdownMenuItem>
        <DropdownMenuSub>
          <DropdownMenuSubTrigger className="cursor-pointer">
            {t("needsYouInbox:snooze")}
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent>
            {SNOOZE_DURATIONS.map((duration) => (
              <DropdownMenuItem
                key={duration}
                className="cursor-pointer"
                onSelect={() => onSnooze(duration)}
              >
                {t(SNOOZE_DURATION_LABEL_KEYS[duration])}
              </DropdownMenuItem>
            ))}
          </DropdownMenuSubContent>
        </DropdownMenuSub>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
