"use client";

import { useTranslation } from "react-i18next";
import { IconChevronDown, IconDeviceLaptop, IconServer } from "@tabler/icons-react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@kandev/ui/dropdown-menu";
import { useLocalFeatureFlag } from "@/hooks/domains/features/use-local-feature-flag";
import { useInstances } from "@/hooks/domains/instances/use-instances";
import { switchInstance } from "@/lib/instances/switch-instance";
import type { InstanceHealth } from "@/lib/api/domains/instances-api";
import { LOCAL_INSTANCE_ID, type InstanceListItem } from "@/lib/state/slices/instances/types";
import { cn } from "@/lib/utils";

type AppSidebarInstancePickerProps = {
  triggerClassName?: string;
  triggerTestId?: string;
  itemTestIdPrefix?: string;
  healthTestIdPrefix?: string;
};

/**
 * Compact instance switcher inlined after the workspace picker in the sidebar
 * header. Only the current instance's name shows on the trigger; the rows
 * carry a health dot. Selecting an instance is a full page reload, so there
 * is no local navigation to close or guard. Hidden while the feature is off
 * or no remote instance is registered.
 */
export function AppSidebarInstancePicker({
  triggerClassName,
  triggerTestId = "sidebar-instance-trigger",
  itemTestIdPrefix = "sidebar-instance-item",
  healthTestIdPrefix = "sidebar-instance-health",
}: AppSidebarInstancePickerProps = {}) {
  const { t } = useTranslation();
  const enabled = useLocalFeatureFlag("multiInstance");
  const { items, activeId, health } = useInstances();

  if (!enabled || items.length <= 1) return null;
  const active = items.find((item) => item.id === activeId) ?? items[0];

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          data-testid={triggerTestId}
          aria-label={t("instances:switchInstance")}
          className={cn(
            "flex min-w-0 items-center gap-1 rounded-md px-1.5 text-xs",
            "text-muted-foreground hover:text-foreground cursor-pointer",
            triggerClassName,
          )}
        >
          <span className="truncate">
            {active.id === LOCAL_INSTANCE_ID ? t("instances:localInstance") : active.name}
          </span>
          <IconChevronDown className="h-3 w-3 shrink-0" aria-hidden />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-64">
        {items.map((item) => (
          <InstancePickerItem
            key={item.id}
            item={item}
            active={item.id === activeId}
            health={health[item.id]}
            itemTestId={`${itemTestIdPrefix}-${item.id}`}
            healthTestId={`${healthTestIdPrefix}-${item.id}`}
          />
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

type InstancePickerItemProps = {
  item: InstanceListItem;
  active: boolean;
  health: InstanceHealth | undefined;
  itemTestId: string;
  healthTestId: string;
};

function InstancePickerItem({
  item,
  active,
  health,
  itemTestId,
  healthTestId,
}: InstancePickerItemProps) {
  const { t } = useTranslation();
  const isLocal = item.id === LOCAL_INSTANCE_ID;
  return (
    <DropdownMenuItem
      data-testid={itemTestId}
      // A disabled instance keeps its registration but has no live proxy, so
      // selecting it would strand the shell on a local boot.
      disabled={!item.enabled}
      aria-current={active ? "true" : undefined}
      onSelect={() => {
        if (!active) void switchInstance(item.id);
      }}
      className={cn("cursor-pointer gap-2", !item.enabled && "cursor-default")}
    >
      {isLocal ? (
        <IconDeviceLaptop className="size-3.5 shrink-0" aria-hidden />
      ) : (
        <IconServer className="size-3.5 shrink-0" aria-hidden />
      )}
      <span className="min-w-0 flex-1 truncate">
        {isLocal ? t("instances:localInstance") : item.name}
      </span>
      {!isLocal && <HealthDot health={health} testId={healthTestId} />}
      <span className="ml-auto flex items-center gap-1 text-[10px] text-muted-foreground">
        {!item.enabled && t("instances:instanceDisabledHint")}
      </span>
    </DropdownMenuItem>
  );
}

type HealthDotState = "healthy" | "unreachable" | "unsupported" | "unknown";

function healthDotState(health: InstanceHealth | undefined): HealthDotState {
  if (!health) return "unknown";
  if (!health.reachable) return "unreachable";
  if (!health.supported) return "unsupported";
  return "healthy";
}

const DOT_CLASS: Record<HealthDotState, string> = {
  healthy: "bg-emerald-500",
  unreachable: "bg-red-500",
  unsupported: "bg-amber-500",
  unknown: "bg-muted-foreground/40",
};

const DOT_LABEL_KEY: Record<HealthDotState, string> = {
  healthy: "instances:healthDotHealthy",
  unreachable: "instances:healthDotUnreachable",
  unsupported: "instances:healthDotUnsupported",
  unknown: "instances:healthDotUnknown",
};

function HealthDot({ health, testId }: { health: InstanceHealth | undefined; testId: string }) {
  const { t } = useTranslation();
  const state = healthDotState(health);
  return (
    <span
      data-testid={testId}
      data-state={state}
      role="img"
      aria-label={t(DOT_LABEL_KEY[state])}
      className={cn("size-1.5 shrink-0 rounded-full", DOT_CLASS[state])}
    />
  );
}
