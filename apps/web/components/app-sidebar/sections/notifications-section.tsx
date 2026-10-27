"use client";

import { useTranslation } from "react-i18next";
import { IconBell, IconCheck, IconX } from "@tabler/icons-react";
import { Badge } from "@kandev/ui/badge";
import { useAppStore } from "@/components/state-provider";
import { useLocalFeatureFlag } from "@/hooks/domains/features/use-local-feature-flag";
import type { RemoteNotificationsController } from "@/hooks/domains/remote-notifications/use-remote-notifications";
import { useRemoteNotifications } from "@/hooks/domains/remote-notifications/use-remote-notifications";
import { switchInstance } from "@/lib/instances/switch-instance";
import { linkToTask } from "@/lib/links";
import { LOCAL_INSTANCE_ID, type InstanceListItem } from "@/lib/state/slices/instances/types";
import type { RemoteNotificationEntry } from "@/lib/state/slices/remote-notifications/types";
import { cn } from "@/lib/utils";
import { APP_SIDEBAR_SECTION_IDS } from "../app-sidebar-constants";
import { AppSidebarSection } from "../app-sidebar-section";

type NotificationsSectionProps = {
  collapsed: boolean;
};

/**
 * The cross-instance notifications module: a sidebar section that groups
 * relayed occurrences by their originating instance. Clicking an entry
 * switches the panel to that instance on the referenced task's route and
 * marks the entry read. This module is the only surface for remote events:
 * no entry here ever toasts, browser-notifies, or plays a sound, and the
 * local instance's own notification behavior stays untouched.
 */
export function NotificationsSection({ collapsed }: NotificationsSectionProps) {
  const enabled = useLocalFeatureFlag("multiInstance");
  const { entries, status, markRead, dismiss, markAllRead } = useRemoteNotifications();
  const instanceItems = useAppStore((s) => s.instances.items);
  const { t } = useTranslation();

  const groups = buildGroups(entries, instanceItems, t);
  const totalUnread = groups.reduce((sum, group) => sum + group.unread, 0);
  if (!enabled || status !== "ready" || groups.length === 0) return null;

  return (
    <AppSidebarSection
      presentation="navigation"
      id={APP_SIDEBAR_SECTION_IDS.notifications}
      label={t("instances:notificationsSectionLabel")}
      collapsed={collapsed}
      icon={IconBell}
      headerAction={
        <span className="flex items-center gap-1">
          {totalUnread > 0 && (
            <Badge
              data-testid="sidebar-notifications-unread-total"
              className="rounded-full bg-primary px-1.5 py-0.5 text-xs text-primary-foreground"
            >
              {totalUnread}
            </Badge>
          )}
          {totalUnread > 0 && <MarkAllReadButton onMarkAllRead={markAllRead} />}
        </span>
      }
      headerActionVisibility="always"
      defaultExpanded={true}
    >
      {groups.map((group) => (
        <InstanceGroup key={group.instanceId} group={group} markRead={markRead} dismiss={dismiss} />
      ))}
    </AppSidebarSection>
  );
}

type NotificationGroup = {
  instanceId: string;
  label: string;
  /** Newest first inside the group. */
  entries: RemoteNotificationEntry[];
  unread: number;
};

function buildGroups(
  entries: RemoteNotificationEntry[],
  instanceItems: InstanceListItem[],
  t: (key: string) => string,
): NotificationGroup[] {
  const byInstance = new Map<string, RemoteNotificationEntry[]>();
  for (const entry of entries) {
    if (entry.dismissed) continue;
    const bucket = byInstance.get(entry.instanceId) ?? [];
    bucket.push(entry);
    byInstance.set(entry.instanceId, bucket);
  }
  // Registry order first (the local row leads like the picker), then any
  // instance the registry no longer lists in arrival order.
  const order = [
    ...instanceItems.map((item) => item.id),
    ...[...byInstance.keys()].filter((id) => !instanceItems.some((item) => item.id === id)),
  ];
  const labelFor = (instanceId: string): string => {
    if (instanceId === LOCAL_INSTANCE_ID) return t("instances:localInstance");
    return instanceItems.find((item) => item.id === instanceId)?.name ?? instanceId;
  };
  return order
    .filter((instanceId) => byInstance.has(instanceId))
    .map((instanceId) => {
      const groupEntries = [...byInstance.get(instanceId)!].reverse();
      return {
        instanceId,
        label: labelFor(instanceId),
        entries: groupEntries,
        unread: groupEntries.filter((entry) => !entry.read).length,
      };
    });
}

function MarkAllReadButton({ onMarkAllRead }: { onMarkAllRead: () => void }) {
  const { t } = useTranslation();
  return (
    <button
      type="button"
      onClick={onMarkAllRead}
      aria-label={t("instances:notificationsMarkAllRead")}
      data-testid="sidebar-notifications-read-all"
      className="flex h-7 w-7 [@media(pointer:coarse)]:size-11 cursor-pointer items-center justify-center rounded text-muted-foreground/70 hover:bg-muted/60 hover:text-foreground transition-colors"
    >
      <IconCheck className="h-3.5 w-3.5" aria-hidden />
    </button>
  );
}

function InstanceGroup({
  group,
  markRead,
  dismiss,
}: {
  group: NotificationGroup;
  markRead: RemoteNotificationsController["markRead"];
  dismiss: RemoteNotificationsController["dismiss"];
}) {
  return (
    <div
      className="flex flex-col gap-0.5"
      data-testid={`sidebar-notification-group-${group.instanceId}`}
    >
      <div className="flex min-h-7 items-center gap-1.5 px-2.5 text-[11px] font-medium text-muted-foreground">
        <span className="min-w-0 flex-1 truncate">{group.label}</span>
        {group.unread > 0 && (
          <Badge
            data-testid={`sidebar-notification-unread-${group.instanceId}`}
            aria-hidden="true"
            className="rounded-full bg-primary px-1.5 py-0.5 text-[10px] text-primary-foreground"
          >
            {group.unread}
          </Badge>
        )}
      </div>
      {group.entries.map((entry) => (
        <NotificationEntryRow key={entry.id} entry={entry} markRead={markRead} dismiss={dismiss} />
      ))}
    </div>
  );
}

function NotificationEntryRow({
  entry,
  markRead,
  dismiss,
}: {
  entry: RemoteNotificationEntry;
  markRead: RemoteNotificationsController["markRead"];
  dismiss: RemoteNotificationsController["dismiss"];
}) {
  const { t } = useTranslation();

  const open = () => {
    // The write fires before the switch so the local row is already read when
    // the reload re-reads the list.
    markRead(entry.id);
    void switchInstance(entry.instanceId, entry.taskId ? linkToTask(entry.taskId) : undefined);
  };

  return (
    <div className="flex items-start gap-0.5">
      <button
        type="button"
        onClick={open}
        data-testid={`sidebar-notification-row-${entry.id}`}
        className={cn(
          "flex min-w-0 flex-1 cursor-pointer items-start gap-2 rounded-md px-2.5 py-1.5 text-left text-[13px] transition-colors hover:bg-muted/60",
          "[@media(pointer:coarse)]:min-h-11",
          entry.read ? "text-foreground/70" : "font-medium text-foreground",
        )}
      >
        <span
          className={cn(
            "mt-1.5 size-1.5 shrink-0 rounded-full",
            entry.read ? "bg-transparent" : "bg-primary",
          )}
          aria-hidden="true"
        />
        <span className="flex min-w-0 flex-col gap-0.5">
          <span className="truncate">{entry.title}</span>
          <span className="truncate text-xs font-normal text-muted-foreground/80">
            {entry.body}
          </span>
        </span>
      </button>
      <button
        type="button"
        onClick={() => dismiss(entry.id)}
        aria-label={t("instances:notificationDismiss")}
        data-testid={`sidebar-notification-dismiss-${entry.id}`}
        className="flex h-6 w-6 shrink-0 cursor-pointer items-center justify-center rounded text-muted-foreground/50 hover:bg-muted/60 hover:text-foreground [@media(pointer:coarse)]:size-11"
      >
        <IconX className="h-3 w-3" aria-hidden />
      </button>
    </div>
  );
}
