"use client";

import { useTranslation } from "react-i18next";
import { Button } from "@kandev/ui/button";
import { useAppStore } from "@/components/state-provider";
import type { RemoteInboxDegradedReason } from "@/lib/types/remote-inbox";
import type { RemoteInboxInstanceState } from "@/lib/state/slices/needs-you-inbox/types";
import { NeedsYouInboxRow } from "./needs-you-inbox-row";
import { NeedsYouInboxHiddenPanel } from "./needs-you-inbox-hidden-panel";

const DEGRADED_REASON_KEYS: Record<RemoteInboxDegradedReason, string> = {
  unreachable: "needsYouInbox:remoteDegradedUnreachable",
  unauthorized: "needsYouInbox:remoteDegradedUnauthorized",
  inbox_disabled: "needsYouInbox:remoteDegradedInboxDisabled",
  error: "needsYouInbox:remoteDegradedError",
};

/**
 * One remote instance's answerable section of the Needs-you Inbox: rows are
 * answered in place through the forward routes, and a degraded instance
 * renders a named, retryable state instead of rows or an answer form that
 * could not succeed. Never affects the local page or other instances'
 * sections.
 */
export function RemoteInboxSection({ instance }: { instance: RemoteInboxInstanceState }) {
  const { t } = useTranslation();
  const bumpRetryTick = useAppStore((s) => s.bumpRemoteInboxRetryTick);
  const instanceName =
    useAppStore((s) => s.instances.items.find((item) => item.id === instance.instanceId)?.name) ??
    instance.instanceId;
  const retry = () => bumpRetryTick(instance.instanceId);

  return (
    <section data-testid="remote-inbox-section" data-instance-id={instance.instanceId}>
      <div className="flex items-center justify-between gap-2 pt-2">
        <h3
          className="text-sm font-medium text-muted-foreground"
          data-testid="remote-inbox-section-title"
        >
          {instanceName}
        </h3>
        {instance.status === "ready" && instance.hasMore && (
          <span className="text-xs text-muted-foreground" data-testid="remote-inbox-truncated">
            {t("needsYouInbox:truncatedNotice")}
          </span>
        )}
      </div>
      {instance.status === "error" ? (
        <div
          className="mt-2 flex items-center justify-between gap-2 rounded-lg border border-border p-4"
          data-testid="remote-inbox-degraded"
        >
          <p className="text-sm text-muted-foreground">
            {t(DEGRADED_REASON_KEYS[instance.degradedReason ?? "error"], {
              instance: instanceName,
            })}
          </p>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="cursor-pointer shrink-0"
            onClick={retry}
            data-testid="remote-inbox-retry"
          >
            {t("needsYouInbox:retry")}
          </Button>
        </div>
      ) : (
        <div className="mt-2 space-y-2">
          {instance.status === "ready" && instance.bundles.length === 0 && (
            <p className="text-xs text-muted-foreground" data-testid="remote-inbox-empty">
              {t("needsYouInbox:remoteEmpty")}
            </p>
          )}
          {instance.bundles.length > 0 && (
            <div className="overflow-hidden rounded-lg border border-border divide-y divide-border">
              {instance.bundles.map((bundle) => (
                <NeedsYouInboxRow
                  key={bundle.pending_id}
                  bundle={bundle}
                  source={{ instanceId: instance.instanceId, instanceName }}
                />
              ))}
            </div>
          )}
          {instance.status === "ready" && instance.hiddenCount > 0 && (
            <NeedsYouInboxHiddenPanel
              hiddenCount={instance.hiddenCount}
              listRevision={instance.appliedGeneration}
              source={{ instanceId: instance.instanceId }}
            />
          )}
        </div>
      )}
    </section>
  );
}
