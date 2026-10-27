"use client";

import { useTranslation } from "react-i18next";
import { IconAlertTriangle, IconCheck, IconClock } from "@tabler/icons-react";
import { Alert, AlertDescription } from "@kandev/ui/alert";
import { useTick } from "@/components/integrations/auth-status-banner";
import { formatRelative } from "@/lib/i18n/formats";
import { instanceHealthCheckedAt, type InstanceHealth } from "@/lib/api/domains/instances-api";

/**
 * The per-instance health banner: one of four states derived from the
 * registry's last poll report.
 *
 *   ok          reachable and the remote version is supported
 *   degraded    unreachable (the poll error is shown verbatim)
 *   unsupported reachable but below the minimum remote version
 *   waiting     the poller has not produced a report yet
 *
 * `health` is null while the report is still loading; nothing renders then.
 */
export function InstanceHealthBanner({
  instanceId,
  health,
}: {
  instanceId: string;
  health: InstanceHealth | null;
}) {
  const { t } = useTranslation();
  useTick(30_000);
  if (!health) return null;

  const checkedAt = instanceHealthCheckedAt(health);
  const checkedLabel = checkedAt ? (
    <span className="text-xs text-muted-foreground ml-2">
      {t("instances:checkedRelative", { relative: formatRelative(checkedAt.toISOString()) })}
    </span>
  ) : null;

  if (!checkedAt) {
    return (
      <Alert data-testid={`instance-health-${instanceId}`} data-state="waiting">
        <IconClock className="h-4 w-4" />
        <AlertDescription className="text-sm">{t("instances:healthWaiting")}</AlertDescription>
      </Alert>
    );
  }

  if (health.reachable && health.supported) {
    return (
      <Alert
        data-testid={`instance-health-${instanceId}`}
        data-state="ok"
        className="border-green-500/40 bg-green-500/10 dark:border-green-400/30 dark:bg-green-400/10"
      >
        <IconCheck className="h-4 w-4 text-green-600 dark:text-green-400" />
        <AlertDescription className="text-sm font-medium">
          {t("instances:healthConnectedVersion", { version: health.version ?? "" })}
          {checkedLabel}
        </AlertDescription>
      </Alert>
    );
  }

  if (health.reachable && !health.supported) {
    return (
      <Alert
        data-testid={`instance-health-${instanceId}`}
        data-state="unsupported"
        variant="destructive"
      >
        <IconAlertTriangle className="h-4 w-4" />
        <AlertDescription className="text-sm">
          {t("instances:healthUnsupportedVersion", {
            error: health.error || t("instances:unknownError"),
          })}
          {checkedLabel}
        </AlertDescription>
      </Alert>
    );
  }

  return (
    <Alert
      data-testid={`instance-health-${instanceId}`}
      data-state="degraded"
      variant="destructive"
    >
      <IconAlertTriangle className="h-4 w-4" />
      <AlertDescription className="text-sm">
        {t("instances:healthUnreachable", {
          error: health.error || t("instances:unknownError"),
        })}
        {checkedLabel}
      </AlertDescription>
    </Alert>
  );
}
