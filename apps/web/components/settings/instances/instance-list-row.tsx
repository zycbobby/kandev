"use client";

import { useRef, useState, type RefObject } from "react";
import { useTranslation } from "react-i18next";
import { IconEdit, IconServer, IconTrash } from "@tabler/icons-react";
import { Badge } from "@kandev/ui/badge";
import { Button } from "@kandev/ui/button";
import { ActionConfirmPopover } from "@/components/confirmation/action-confirm-popover";
import { InlineConfirmActions } from "@/components/confirmation/inline-confirm-actions";
import { MobileActionConfirmation } from "@/components/confirmation/mobile-action-confirmation";
import { useResponsiveBreakpoint } from "@/hooks/use-responsive-breakpoint";
import type { InstanceHealth, RemoteInstance } from "@/lib/api/domains/instances-api";
import { InstanceHealthBanner } from "./instance-health-banner";

type InstanceListRowProps = {
  instance: RemoteInstance;
  health: InstanceHealth | null;
  isBusy: boolean;
  isEditing: boolean;
  onEdit: (instance: RemoteInstance) => void;
  onDelete: (instance: RemoteInstance) => Promise<void>;
};

/**
 * One registered instance: identity, last health report, and the edit/remove
 * actions. Removal asks for confirmation on every pointer type, following the
 * settings-row confirmation pattern.
 */
export function InstanceListRow({
  instance,
  health,
  isBusy,
  isEditing,
  onEdit,
  onDelete,
}: InstanceListRowProps) {
  const { t } = useTranslation();
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const deleteAnchorRef = useRef<HTMLButtonElement>(null);
  const { isFinePointer, isMobile } = useResponsiveBreakpoint();

  return (
    <div
      data-testid={`instance-row-${instance.id}`}
      className="space-y-3 rounded-lg border border-border/70 bg-background p-4"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <IconServer className="h-4 w-4 shrink-0 text-muted-foreground" />
          <div className="truncate text-sm font-medium text-foreground">{instance.name}</div>
          {!instance.enabled && (
            <Badge variant="outline" className="shrink-0 text-[10px]">
              {t("instances:disabledBadge")}
            </Badge>
          )}
        </div>
        <div className="flex shrink-0 flex-wrap items-center justify-end gap-1">
          <Button
            type="button"
            variant="ghost"
            size="icon"
            onClick={() => onEdit(instance)}
            disabled={isBusy || isEditing}
            className="cursor-pointer"
            aria-label={t("instances:editInstanceNamed", { name: instance.name })}
          >
            <IconEdit className="h-4 w-4" />
          </Button>
          {(isMobile || isFinePointer || !confirmingDelete) && (
            <Button
              ref={deleteAnchorRef}
              type="button"
              variant="ghost"
              size="icon"
              onClick={() => setConfirmingDelete(true)}
              disabled={isBusy || isEditing}
              className="cursor-pointer text-destructive hover:text-destructive"
              data-testid="instance-delete-button"
              aria-label={t("instances:removeInstanceLabel", { name: instance.name })}
            >
              <IconTrash className="h-4 w-4" />
            </Button>
          )}
          <InstanceDeleteConfirmation
            instance={instance}
            open={confirmingDelete}
            onOpenChange={setConfirmingDelete}
            onDelete={onDelete}
            isFinePointer={isFinePointer}
            deleteAnchorRef={deleteAnchorRef}
          />
        </div>
      </div>
      <div className="truncate font-mono text-xs text-muted-foreground">{instance.base_url}</div>
      <InstanceHealthBanner instanceId={instance.id} health={health} />
    </div>
  );
}

type InstanceDeleteConfirmationProps = {
  instance: RemoteInstance;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onDelete: (instance: RemoteInstance) => Promise<void>;
  isFinePointer: boolean;
  deleteAnchorRef: RefObject<HTMLButtonElement | null>;
};

/** The removal confirmation surface: phone drawer, touch inline, or popover. */
function InstanceDeleteConfirmation({
  instance,
  open,
  onOpenChange,
  onDelete,
  isFinePointer,
  deleteAnchorRef,
}: InstanceDeleteConfirmationProps) {
  const { t } = useTranslation();
  const removeConfirmation = t("instances:removeInstanceNamed", { name: instance.name });
  const removeLabel = t("instances:remove");
  const cancel = () => onOpenChange(false);
  const confirm = () => onDelete(instance);

  return (
    <MobileActionConfirmation
      open={open}
      targetKey={instance.id}
      title={removeConfirmation}
      cancelLabel={t("common:cancel")}
      confirmLabel={removeLabel}
      confirmAriaLabel={removeConfirmation}
      confirmTestId="instance-remove-confirm"
      onOpenChange={onOpenChange}
      focusReturnRef={deleteAnchorRef}
      onConfirm={confirm}
      fallback={
        !isFinePointer ? (
          <InlineConfirmActions
            density="touch"
            testId="instance-remove-inline-confirmation"
            ariaLabel={removeConfirmation}
            description={removeConfirmation}
            cancelLabel={t("common:cancel")}
            confirmLabel={removeLabel}
            confirmAriaLabel={removeConfirmation}
            confirmTestId="instance-remove-confirm"
            onCancel={cancel}
            onClose={cancel}
            onConfirm={confirm}
          />
        ) : (
          <ActionConfirmPopover
            open={open}
            anchorRef={deleteAnchorRef}
            title={removeConfirmation}
            cancelLabel={t("common:cancel")}
            confirmLabel={removeLabel}
            confirmAriaLabel={removeConfirmation}
            confirmTestId="instance-remove-confirm"
            testId="instance-remove-confirm-popover"
            onOpenChange={onOpenChange}
            onCancel={cancel}
            onConfirm={confirm}
          />
        )
      }
    />
  );
}
