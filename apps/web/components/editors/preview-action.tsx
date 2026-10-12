"use client";

import { IconEye, IconLoader2 } from "@tabler/icons-react";
import { Button } from "@kandev/ui/button";
import { DropdownMenuItem } from "@kandev/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@kandev/ui/tooltip";
import type { FilePreviewKind } from "@/lib/utils/file-types";

/**
 * A single preview control in the editor toolbar. Resolved from the file's
 * `previewKind`, or supplied by a host for files the preview-kind mechanism
 * does not model (a text-encoded image such as SVG, whose `previewKind` is
 * `"none"` while it still needs a way back from the source editor).
 */
export type PreviewAction = {
  label: string;
  testId: string;
  onClick: () => void;
  disabled?: boolean;
  busy?: boolean;
  trustWarning?: string;
};

type PreviewActionSources = {
  previewKind: FilePreviewKind;
  onTogglePreview?: () => void;
  onPreviewHtml?: () => void;
  isPublishingHtmlPreview?: boolean;
  sourceAction?: PreviewAction;
};

/**
 * Resolve the toolbar's preview control. `sourceAction` wins outright: a host
 * only supplies it for files outside the markdown/HTML preview mechanism.
 */
export function resolvePreviewAction(
  {
    previewKind,
    onTogglePreview,
    onPreviewHtml,
    isPublishingHtmlPreview = false,
    sourceAction,
  }: PreviewActionSources,
  t: (key: string) => string,
): PreviewAction | null {
  if (sourceAction) return sourceAction;
  if (previewKind === "markdown" && onTogglePreview) {
    return {
      label: t("editors:previewMarkdown"),
      testId: "markdown-preview-toggle",
      onClick: onTogglePreview,
    };
  }
  if (previewKind === "html" && onPreviewHtml) {
    return {
      label: t("editors:previewHtml"),
      testId: "html-preview-toggle",
      onClick: onPreviewHtml,
      disabled: isPublishingHtmlPreview,
      busy: isPublishingHtmlPreview,
      trustWarning: t("task:htmlPreviewTrustedCode"),
    };
  }
  return null;
}

/**
 * The `Preview SVG` control a text-encoded image shows while its source editor
 * is open, returning the viewer to the rendered image.
 */
export function textImagePreviewAction(
  onClick: () => void,
  t: (key: string) => string,
): PreviewAction {
  return { label: t("editors:previewSvg"), testId: "svg-preview-toggle", onClick };
}

export function PreviewActionButton({ action }: { action: PreviewAction }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          size="sm"
          variant="ghost"
          onClick={action.onClick}
          disabled={action.disabled}
          aria-label={action.label}
          title={action.trustWarning}
          className="h-6 w-6 p-0 cursor-pointer max-md:h-11 [@media(pointer:coarse)]:h-11 max-md:w-11 [@media(pointer:coarse)]:w-11"
          data-testid={action.testId}
        >
          {action.busy ? (
            <IconLoader2 className="h-4 w-4 animate-spin" />
          ) : (
            <IconEye className="h-4 w-4" />
          )}
        </Button>
      </TooltipTrigger>
      <TooltipContent>
        <p>{action.label}</p>
        {action.trustWarning && (
          <p className="mt-1 max-w-xs text-muted-foreground">{action.trustWarning}</p>
        )}
      </TooltipContent>
    </Tooltip>
  );
}

/** The same control as an overflow-menu row, where a button does not fit. */
export function PreviewActionMenuItem({ action }: { action: PreviewAction }) {
  return (
    <DropdownMenuItem
      className="cursor-pointer gap-2"
      disabled={action.disabled}
      onSelect={action.onClick}
    >
      {action.busy ? (
        <IconLoader2 className="size-4 animate-spin" />
      ) : (
        <IconEye className="size-4" />
      )}
      {action.label}
    </DropdownMenuItem>
  );
}
