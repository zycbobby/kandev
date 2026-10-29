"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@kandev/ui/button";
import { useAppStore } from "@/components/state-provider";
import { toast } from "@/lib/toast/sonner";
import { formatRelativeTime } from "@/lib/i18n/formats";
import { restoreClarificationInboxBundle } from "@/lib/api/domains/clarification-inbox-api";
import {
  listRemoteHiddenNeedsYouInbox,
  restoreRemoteClarificationBundle,
} from "@/lib/api/domains/remote-inbox-api";
import { listHiddenNeedsYouInbox } from "@/lib/api/domains/needs-you-inbox-api";
import type { NeedsYouInboxHiddenRow } from "@/lib/types/needs-you-inbox";
import { rowPrimaryText } from "@/lib/needs-you-inbox/row-presentation";

type HiddenListStatus = "idle" | "loading" | "ready" | "error";

// Each hidden row's primary text: the question's own text for a question row,
// the task's title for a waiting row.
function hiddenRowPrimaryText(row: NeedsYouInboxHiddenRow, fallback: string): string {
  if (row.kind === "waiting") {
    return row.task_title || row.task_id || fallback;
  }
  return rowPrimaryText(row, fallback);
}

// Each hidden row's secondary text: which step a waiting row is parked on, or
// nothing extra for a question row.
function hiddenRowSecondaryText(row: NeedsYouInboxHiddenRow): string | null {
  if (row.kind === "waiting") {
    return row.step_name || null;
  }
  return null;
}

function hiddenRowId(row: NeedsYouInboxHiddenRow): string {
  return row.kind === "waiting" ? row.id : row.pending_id;
}

function HiddenRow({
  row,
  onRestore,
}: {
  row: NeedsYouInboxHiddenRow;
  onRestore: (pendingId: string) => void;
}) {
  const { t } = useTranslation();
  const secondary = hiddenRowSecondaryText(row);
  return (
    <div
      className="flex items-center justify-between gap-2 rounded-md border border-border px-3 py-2"
      data-testid="needs-you-inbox-hidden-row"
      data-row-id={hiddenRowId(row)}
    >
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm">
          {hiddenRowPrimaryText(row, t("needsYouInbox:questionFromAgent"))}
        </p>
        {secondary && <p className="truncate text-xs text-muted-foreground">{secondary}</p>}
        <p className="text-xs text-muted-foreground">
          {row.state === "snoozed" && row.snooze_until
            ? t("needsYouInbox:snoozedUntil", { time: formatRelativeTime(row.snooze_until) })
            : t("needsYouInbox:dismissed")}
        </p>
      </div>
      <Button
        type="button"
        variant="ghost"
        size="default"
        className="cursor-pointer shrink-0"
        onClick={() => onRestore(hiddenRowId(row))}
      >
        {t("needsYouInbox:restore")}
      </Button>
    </div>
  );
}

function useHiddenList(
  scopeKey: string | null,
  open: boolean,
  hiddenCount: number,
  listRevision: number,
  source?: { instanceId: string },
) {
  const [status, setStatus] = useState<HiddenListStatus>("idle");
  const [rows, setRows] = useState<NeedsYouInboxHiddenRow[]>([]);
  const [total, setTotal] = useState<number | null>(null);
  const generationRef = useRef(0);
  // Read inside `load` instead of `scopeKey` itself: a restore captures this
  // closure while its DELETE is in flight, and a scope switch during that
  // wait must not let the stale continuation apply scope A's read to scope B's
  // panel (or bump the shared generation past B's own request).
  const currentScopeRef = useRef(scopeKey);
  currentScopeRef.current = scopeKey;

  const load = useCallback(async () => {
    if (!scopeKey || currentScopeRef.current !== scopeKey) return;
    const generation = ++generationRef.current;
    setStatus("loading");
    try {
      // Both branches enumerate the merged hidden set (questions and waiting
      // rows together, kind-tagged): local reads hit the workspace-local
      // merged endpoint, remote reads the instance forward route.
      let mapped: NeedsYouInboxHiddenRow[];
      let total: number;
      if (source) {
        const remotePage = await listRemoteHiddenNeedsYouInbox(source.instanceId);
        mapped = remotePage.rows;
        total = remotePage.total;
      } else {
        const localPage = await listHiddenNeedsYouInbox(scopeKey);
        mapped = localPage.rows;
        total = localPage.total;
      }
      if (generationRef.current !== generation || currentScopeRef.current !== scopeKey) {
        return;
      }
      setRows(mapped);
      setTotal(total);
      setStatus("ready");
    } catch {
      if (generationRef.current !== generation || currentScopeRef.current !== scopeKey) {
        return;
      }
      setStatus("error");
    }
  }, [scopeKey, source]);

  const previousScopeRef = useRef(scopeKey);
  useEffect(() => {
    if (previousScopeRef.current === scopeKey) return;
    previousScopeRef.current = scopeKey;
    generationRef.current += 1;
    setRows([]);
    setTotal(null);
    setStatus("idle");
    if (open) void load();
  }, [scopeKey, open, load]);

  // The main list's hidden count is the only signal this panel gets that the
  // hidden set changed elsewhere (another row dismissed/snoozed, or a snooze
  // expiring): re-enumerate so the disclosed total and rows stay reconciled
  // with it rather than showing what this panel happened to load last.
  const previousListState = useRef({ hiddenCount, listRevision });
  useEffect(() => {
    const previous = previousListState.current;
    previousListState.current = { hiddenCount, listRevision };
    if (previous.hiddenCount === hiddenCount && previous.listRevision === listRevision) return;
    if (open) void load();
  }, [hiddenCount, listRevision, open, load]);

  return { status, rows, total, load };
}

// Discloses how many rows this operator's own dismiss or snooze is hiding,
// and lets them enumerate and restore one. With `source` the panel enumerates
// and restores one remote instance's hidden set through the local backend's
// forward routes instead of the local sidecar.
export function NeedsYouInboxHiddenPanel({
  hiddenCount,
  listRevision = 0,
  source,
}: {
  hiddenCount: number;
  listRevision?: number;
  source?: { instanceId: string };
}) {
  const { t } = useTranslation();
  const workspaceId = useAppStore((s) => s.workspaces.activeId);
  const bumpRefreshTick = useAppStore((s) => s.bumpNeedsYouInboxRefreshTick);
  const bumpRemoteRefreshTick = useAppStore((s) => s.bumpRemoteInboxRefreshTick);
  const scopeKey = source ? source.instanceId : workspaceId;
  const [open, setOpen] = useState(false);
  const { status, rows, total, load } = useHiddenList(
    scopeKey,
    open,
    hiddenCount,
    listRevision,
    source,
  );
  const displayCount = total ?? hiddenCount;

  const toggle = useCallback(() => {
    setOpen((current) => {
      const next = !current;
      if (next) void load();
      return next;
    });
  }, [load]);

  const handleRestore = useCallback(
    async (rowId: string) => {
      try {
        if (source) {
          await restoreRemoteClarificationBundle(source.instanceId, rowId);
          bumpRemoteRefreshTick(source.instanceId);
        } else {
          // Restore is one sidecar DELETE for both kinds: the mw identity a
          // waiting row carries addresses the same per-user sidecar.
          await restoreClarificationInboxBundle(rowId);
          bumpRefreshTick();
        }
        void load();
      } catch {
        toast.error(t("needsYouInbox:restoreFailed"));
      }
    },
    [bumpRefreshTick, bumpRemoteRefreshTick, load, source, t],
  );

  return (
    <div className="rounded-lg border border-border p-4" data-testid="needs-you-inbox-hidden-panel">
      <div className="flex items-center justify-between gap-2">
        <p className="text-xs text-muted-foreground">
          {t("needsYouInbox:hiddenNotice", { count: displayCount })}
        </p>
        <Button
          type="button"
          variant="outline"
          size="default"
          className="cursor-pointer"
          onClick={toggle}
        >
          {open ? t("needsYouInbox:hideHidden") : t("needsYouInbox:showHidden")}
        </Button>
      </div>
      {open && (
        <div className="mt-3 space-y-2">
          {status === "loading" && (
            <p className="text-xs text-muted-foreground">{t("needsYouInbox:hiddenLoading")}</p>
          )}
          {status === "error" && (
            <p className="text-xs text-destructive">{t("needsYouInbox:hiddenLoadFailed")}</p>
          )}
          {status === "ready" && rows.length === 0 && (
            <p className="text-xs text-muted-foreground">{t("needsYouInbox:hiddenEmpty")}</p>
          )}
          {status === "ready" &&
            rows.map((row) => (
              <HiddenRow key={hiddenRowId(row)} row={row} onRestore={handleRestore} />
            ))}
        </div>
      )}
    </div>
  );
}
