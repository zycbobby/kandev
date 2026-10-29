"use client";

import { useCallback, useRef, useState } from "react";
import { IconChevronDown, IconChevronRight, IconMessageQuestion } from "@tabler/icons-react";
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
  remoteClarificationRespondTarget,
  snoozeRemoteClarificationBundle,
} from "@/lib/api/domains/remote-inbox-api";
import type {
  ClarificationInboxBundle,
  ClarificationInboxSnoozeDuration,
} from "@/lib/types/clarification-inbox";
import type {
  ClarificationOutcome,
  ResolvedStatus,
} from "@/hooks/domains/session/use-clarification-group";
import { ClarificationPanelSection } from "@/components/task/chat/clarification-panel-section";
import {
  rowPrimaryText,
  rowQuestionCount,
  rowSecondaryText,
} from "@/lib/needs-you-inbox/row-presentation";
import { resolveThreadSessionStatus } from "@/lib/threads/thread-session-status";
import { linkToTask } from "@/lib/links";
import type { ClarificationAnswer, TaskSessionState } from "@/lib/types/http";
import type {
  LateClarificationSnapshot,
  LateClarificationState,
} from "@/hooks/use-late-clarification-message";
import type { MessageAdmissionOutcome } from "@/hooks/use-message-handler";
import { InboxRowActionsMenu, useSidecarAction } from "./inbox-row-actions";

// The winner's status is absent only against a pre-R10 backend that never
// sent an envelope status; treating that as "answered" preserves this
// notice's pre-existing wording for that case.
function anotherCallerOutcomeKey(status: ResolvedStatus | undefined): string {
  return status === "rejected"
    ? "needsYouInbox:anotherCallerRejected"
    : "needsYouInbox:anotherCallerResolved";
}

function useRowOutcomeNotice(primaryText: string, bumpRefreshTick: () => void) {
  const { t } = useTranslation();
  return useCallback(
    (outcome: ClarificationOutcome) => {
      if (outcome.kind === "resolved") {
        if (!outcome.claimedByThisCaller) {
          toast(t(anotherCallerOutcomeKey(outcome.status), { question: primaryText }));
        }
        bumpRefreshTick();
        return;
      }
      if (outcome.kind === "no_longer_active") {
        toast(t("needsYouInbox:bundleNoLongerActive", { question: primaryText }));
        bumpRefreshTick();
      }
      if (outcome.kind === "late_message_admitted") {
        bumpRefreshTick();
      }
      // submission_failed: the shared overlay's own inline banner already
      // offers a retry; the row and count stay untouched here.
    },
    [bumpRefreshTick, primaryText, t],
  );
}

function taskHrefForBundle(bundle: ClarificationInboxBundle): string {
  return linkToTask(bundle.task_id, { sessionId: bundle.session_id ?? undefined });
}

// The open-task control: a plain navigation Link on local rows, an
// instance-switch action on remote rows (the task lives on another panel).
function RowOpenTaskControl({
  source,
  taskHref,
  onOpenTask,
  label,
}: {
  source?: { instanceId: string; instanceName: string };
  taskHref: string;
  onOpenTask: () => void;
  label: string;
}) {
  if (source) {
    return (
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="hidden shrink-0 cursor-pointer sm:inline-flex"
        onClick={onOpenTask}
        data-testid="needs-you-inbox-open-task"
      >
        {label}
      </Button>
    );
  }
  return (
    <Button
      asChild
      variant="outline"
      size="sm"
      className="hidden shrink-0 cursor-pointer sm:inline-flex"
    >
      <Link href={taskHref} data-testid="needs-you-inbox-open-task">
        {label}
      </Link>
    </Button>
  );
}

// The expanded answer panel: remote rows submit through the forward route and
// never offer the late-answer fallback (the owning agent session lives on the
// remote instance, not here).
function RowExpandedPanel({
  source,
  bundle,
  onOutcome,
  onLateAnswer,
  lateAnswerState,
  shortcutScopeRef,
  initialAnswers,
  onAnswersChange,
}: {
  source?: { instanceId: string; instanceName: string };
  bundle: ClarificationInboxBundle;
  onOutcome: (outcome: ClarificationOutcome) => void;
  onLateAnswer?: (snapshot: LateClarificationSnapshot) => Promise<MessageAdmissionOutcome>;
  lateAnswerState?: LateClarificationState;
  shortcutScopeRef: React.RefObject<HTMLDivElement | null>;
  initialAnswers?: readonly ClarificationAnswer[];
  onAnswersChange?: (answers: ClarificationAnswer[]) => void;
}) {
  const initialAnswersRef = useRef(initialAnswers);
  if (source) {
    return (
      <ClarificationPanelSection
        pending
        messages={bundle.messages}
        onResolved={() => {}}
        onOutcome={onOutcome}
        respondTarget={remoteClarificationRespondTarget(source.instanceId)}
        initialAnswers={initialAnswersRef.current}
        onAnswersChange={onAnswersChange}
        shortcutScopeRef={shortcutScopeRef}
        maxHeightVh={50}
      />
    );
  }
  return (
    <ClarificationPanelSection
      pending
      messages={bundle.messages}
      onResolved={() => {}}
      onOutcome={onOutcome}
      onLateAnswer={onLateAnswer}
      lateAnswerState={lateAnswerState}
      shortcutScopeRef={shortcutScopeRef}
      maxHeightVh={50}
    />
  );
}

/** Picks the dismiss/snooze/open-task targets: remote rows forward through
 * the instance routes, local rows call the workspace-local sidecar APIs. */
function rowSidecarActions(
  source: { instanceId: string; instanceName: string } | undefined,
  bundle: ClarificationInboxBundle,
  taskHref: string,
) {
  const pendingId = bundle.pending_id;
  if (!source) {
    return {
      dismiss: () => dismissClarificationInboxBundle(pendingId),
      snooze: (duration: ClarificationInboxSnoozeDuration) =>
        snoozeClarificationInboxBundle(pendingId, duration),
      openTask: undefined as (() => void) | undefined,
    };
  }
  return {
    dismiss: () => dismissRemoteClarificationBundle(source.instanceId, pendingId),
    snooze: (duration: ClarificationInboxSnoozeDuration) =>
      snoozeRemoteClarificationBundle(source.instanceId, pendingId, duration),
    openTask: () => void switchInstance(source.instanceId, taskHref),
  };
}

function useRemoteAnswerDraft(
  instanceId: string | undefined,
  pendingId: string,
  notify: (outcome: ClarificationOutcome) => void,
) {
  const setDraft = useAppStore((s) => s.setRemoteInboxDraft);
  const savedDraft = useAppStore((s) =>
    instanceId ? s.needsYouInbox?.remoteDraftsByInstanceId?.[instanceId]?.[pendingId] : undefined,
  );
  const onAnswersChange = useCallback(
    (answers: ClarificationAnswer[]) => {
      if (instanceId) setDraft(instanceId, pendingId, answers);
    },
    [instanceId, pendingId, setDraft],
  );
  const handleOutcome = (outcome: ClarificationOutcome) => {
    if (instanceId && (outcome.kind === "resolved" || outcome.kind === "no_longer_active")) {
      setDraft(instanceId, pendingId, []);
    }
    notify(outcome);
  };
  return { savedDraft, onAnswersChange, handleOutcome };
}

// eslint-disable-next-line max-lines-per-function -- the row keeps its responsive actions and inline panel together.
export function NeedsYouInboxRow({
  bundle,
  onLateAnswer,
  lateAnswerState,
  source,
}: {
  bundle: ClarificationInboxBundle;
  onLateAnswer?: (snapshot: LateClarificationSnapshot) => Promise<MessageAdmissionOutcome>;
  lateAnswerState?: LateClarificationState;
  // Present on remote-instance rows: answers and sidecar writes forward
  // through the local backend to the owning instance, and opening the task
  // switches the panel there. Local rows omit it and keep today's behavior
  // byte-for-byte.
  source?: { instanceId: string; instanceName: string };
}) {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState(false);
  const rowRef = useRef<HTMLDivElement>(null);
  const bumpRefreshTick = useAppStore((s) => s.bumpNeedsYouInboxRefreshTick);
  const bumpRemoteRefreshTick = useAppStore((s) => s.bumpRemoteInboxRefreshTick);
  const bumpTick = source ? () => bumpRemoteRefreshTick(source.instanceId) : bumpRefreshTick;

  const primaryText = rowPrimaryText(bundle, t("needsYouInbox:questionFromAgent"));
  const secondaryText = rowSecondaryText(bundle);
  const questionCount = rowQuestionCount(bundle);
  const taskHref = taskHrefForBundle(bundle);
  const relativeTime = formatRelativeTime(bundle.created_at);
  const statusLabel = t(
    resolveThreadSessionStatus({
      state: bundle.session_state as TaskSessionState,
      pending_action: "clarification",
    }).labelKey,
  );

  const { busy: dismissing, run: runDismiss } = useSidecarAction(bundle.pending_id, bumpTick, () =>
    toast.error(t("needsYouInbox:dismissFailed")),
  );
  const { busy: snoozing, run: runSnooze } = useSidecarAction(bundle.pending_id, bumpTick, () =>
    toast.error(t("needsYouInbox:snoozeFailed")),
  );
  const notifyOutcome = useRowOutcomeNotice(primaryText, bumpTick);
  const { savedDraft, onAnswersChange, handleOutcome } = useRemoteAnswerDraft(
    source?.instanceId,
    bundle.pending_id,
    notifyOutcome,
  );
  const {
    dismiss: dismissAction,
    snooze: snoozeAction,
    openTask,
  } = rowSidecarActions(source, bundle, taskHref);

  return (
    <div ref={rowRef} data-testid="needs-you-inbox-row" data-pending-id={bundle.pending_id}>
      <div className="flex items-center gap-3 px-4 py-2.5">
        <button
          type="button"
          className="flex min-w-0 flex-1 items-center gap-3 text-left cursor-pointer max-md:min-h-11 [@media(pointer:coarse)]:min-h-11"
          onClick={() => setExpanded((current) => !current)}
          aria-expanded={expanded}
          data-testid="needs-you-inbox-row-toggle"
        >
          <span
            className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-muted"
            role="img"
            aria-label={statusLabel}
          >
            <IconMessageQuestion className="h-4 w-4 text-yellow-500" />
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
            <span className="block truncate text-sm font-medium">{primaryText}</span>
            <span className="block truncate text-xs text-muted-foreground">{secondaryText}</span>
            {questionCount > 1 && (
              <span
                className="block truncate text-xs text-muted-foreground"
                data-testid="needs-you-inbox-row-question-count"
              >
                {t("needsYouInbox:questionCount", { count: questionCount })}
              </span>
            )}
          </span>
          <span className="shrink-0 text-xs text-muted-foreground">{relativeTime}</span>
          {expanded ? (
            <IconChevronDown className="h-4 w-4 shrink-0 text-muted-foreground" />
          ) : (
            <IconChevronRight className="h-4 w-4 shrink-0 text-muted-foreground" />
          )}
        </button>
        {/* The row body toggles the answer panel, so without this the task is
            unreachable from the Inbox (design-03#D1). Withheld below `sm`,
            where it would squeeze the title; the actions menu carries it at
            every width. */}
        <RowOpenTaskControl
          source={source}
          taskHref={taskHref}
          onOpenTask={openTask ?? (() => {})}
          label={t("needsYouInbox:openTask")}
        />
        <InboxRowActionsMenu
          disabled={dismissing || snoozing}
          taskHref={taskHref}
          onOpenTask={openTask}
          onDismiss={() => void runDismiss(dismissAction)}
          onSnooze={(duration) => void runSnooze(() => snoozeAction(duration))}
        />
      </div>
      {expanded && (
        <RowExpandedPanel
          source={source}
          bundle={bundle}
          onOutcome={handleOutcome}
          onLateAnswer={onLateAnswer}
          lateAnswerState={lateAnswerState}
          shortcutScopeRef={rowRef}
          initialAnswers={savedDraft}
          onAnswersChange={onAnswersChange}
        />
      )}
    </div>
  );
}
