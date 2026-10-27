"use client";

import { useCallback, useState } from "react";
import { IconBrowser, IconCheck, IconCopy, IconExternalLink } from "@tabler/icons-react";
import { Button } from "@kandev/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@kandev/ui/tooltip";
import { useIsRemoteInstance } from "@/hooks/domains/instances/use-is-remote-instance";
import { copyToClipboard } from "@/lib/utils/copy-to-clipboard";
import { useTranslation } from "react-i18next";

export function PortUrlActions({
  url,
  onOpenBrowserPanel,
  browserActionTestId,
}: {
  url: string;
  onOpenBrowserPanel?: (url: string) => void;
  browserActionTestId?: string;
}) {
  const { t } = useTranslation();
  // Host-bound: the port-proxy and dedicated tunnel ports bind to the
  // machine serving the panel, so the open actions degrade for a remote
  // instance instead of navigating to a dead URL.
  const isRemoteInstance = useIsRemoteInstance();
  const [copied, setCopied] = useState(false);
  const handleCopy = useCallback(async () => {
    if (await copyToClipboard(url)) {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    }
  }, [url]);

  return (
    <div className="flex items-center gap-0.5">
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            size="icon"
            variant="ghost"
            aria-label={t("task:copyUrl")}
            className="cursor-pointer"
            onClick={handleCopy}
          >
            {copied ? (
              <IconCheck className="h-3.5 w-3.5 text-green-500" />
            ) : (
              <IconCopy className="h-3.5 w-3.5" />
            )}
          </Button>
        </TooltipTrigger>
        <TooltipContent>{t("task:copyUrl")}</TooltipContent>
      </Tooltip>
      {isRemoteInstance ? (
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              size="icon"
              variant="ghost"
              aria-label={t("task:openInNewTab")}
              disabled
              className="text-muted-foreground/60"
            >
              <IconExternalLink className="h-3.5 w-3.5" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>{t("instances:unavailableForRemote")}</TooltipContent>
        </Tooltip>
      ) : (
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              size="icon"
              variant="ghost"
              aria-label={t("task:openInNewTab")}
              className="cursor-pointer"
              asChild
            >
              <a href={url} target="_blank" rel="noopener noreferrer">
                <IconExternalLink className="h-3.5 w-3.5" />
              </a>
            </Button>
          </TooltipTrigger>
          <TooltipContent>{t("task:openInNewTab")}</TooltipContent>
        </Tooltip>
      )}
      {onOpenBrowserPanel && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              size="icon"
              variant="ghost"
              aria-label={t("task:openInBrowserPanel")}
              data-testid={browserActionTestId}
              className="cursor-pointer"
              disabled={isRemoteInstance}
              onClick={() => onOpenBrowserPanel(url)}
            >
              <IconBrowser className="h-3.5 w-3.5" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>
            {isRemoteInstance ? t("instances:unavailableForRemote") : t("task:openInBrowserPanel")}
          </TooltipContent>
        </Tooltip>
      )}
    </div>
  );
}
