"use client";

import { useTranslation } from "react-i18next";
import { Button } from "@kandev/ui/button";
import { Input } from "@kandev/ui/input";
import { Switch } from "@kandev/ui/switch";
import { SettingsInfo } from "@/components/settings/settings-info";
import { SettingsRow } from "@/components/settings/settings-group";
import { settingsActionClassName } from "@/components/settings/settings-control";

/** The editable fields of one remote instance, staged as a settings draft. */
export type InstanceFormState = {
  name: string;
  baseUrl: string;
  credentialSecretId: string;
  enabled: boolean;
};

export const emptyInstanceForm: InstanceFormState = {
  name: "",
  baseUrl: "",
  credentialSecretId: "",
  enabled: true,
};

// A wire-shaped example interpolated into help copy, not written into the
// catalog: it is an example value, not prose.
const BASE_URL_EXAMPLE = "http://192.168.1.20:43110";

type InstanceFormProps = {
  mode: "create" | "edit";
  form: InstanceFormState;
  baseline: InstanceFormState;
  disabled: boolean;
  onFormChange: (patch: Partial<InstanceFormState>) => void;
  onCancel: () => void;
};

/** Renders the instance create/edit draft; the shared save bar owns saving. */
export function InstanceForm({
  mode,
  form,
  baseline,
  disabled,
  onFormChange,
  onCancel,
}: InstanceFormProps) {
  const { t } = useTranslation();
  const nameIsDirty = form.name.trim() !== baseline.name.trim();
  const baseUrlIsDirty = form.baseUrl.trim() !== baseline.baseUrl.trim();
  const credentialIsDirty = form.credentialSecretId.trim() !== baseline.credentialSecretId.trim();
  const enabledIsDirty = form.enabled !== baseline.enabled;

  return (
    <div className="space-y-1 rounded-lg border border-border/70 bg-background p-4">
      <InstanceFormHeader mode={mode} disabled={disabled} onCancel={onCancel} />
      <SettingsRow
        label={t("instances:name")}
        description={t("instances:nameDescription")}
        controlId="instance-name"
        isDirty={nameIsDirty}
        control={
          <Input
            id="instance-name"
            value={form.name}
            onChange={(event) => onFormChange({ name: event.target.value })}
            disabled={disabled}
            data-settings-dirty={nameIsDirty}
          />
        }
      />
      <SettingsRow
        label={t("instances:baseUrl")}
        description={t("instances:baseUrlDescription")}
        controlId="instance-base-url"
        isDirty={baseUrlIsDirty}
        control={
          <Input
            id="instance-base-url"
            value={form.baseUrl}
            onChange={(event) => onFormChange({ baseUrl: event.target.value })}
            disabled={disabled}
            data-settings-dirty={baseUrlIsDirty}
          />
        }
        info={
          <SettingsInfo label={t("instances:baseUrl")}>
            <p>{t("instances:baseUrlHelp", { example: BASE_URL_EXAMPLE })}</p>
          </SettingsInfo>
        }
      />
      <SettingsRow
        label={t("instances:credentialSecretId")}
        description={t("instances:credentialSecretIdDescription")}
        controlId="instance-credential-secret"
        isDirty={credentialIsDirty}
        control={
          <Input
            id="instance-credential-secret"
            value={form.credentialSecretId}
            onChange={(event) => onFormChange({ credentialSecretId: event.target.value })}
            disabled={disabled}
            data-settings-dirty={credentialIsDirty}
          />
        }
        info={
          <SettingsInfo label={t("instances:credentialSecretId")}>
            <p>{t("instances:credentialSecretIdHelp")}</p>
          </SettingsInfo>
        }
      />
      <SettingsRow
        label={t("instances:enabled")}
        description={t("instances:enabledDescription")}
        controlId="instance-enabled"
        touchTarget="switch"
        isDirty={enabledIsDirty}
        control={
          <Switch
            id="instance-enabled"
            checked={form.enabled}
            onCheckedChange={(enabled) => onFormChange({ enabled })}
            disabled={disabled}
            data-settings-dirty={enabledIsDirty}
            className="shrink-0 cursor-pointer"
          />
        }
      />
    </div>
  );
}

type InstanceFormHeaderProps = {
  mode: "create" | "edit";
  disabled: boolean;
  onCancel: () => void;
};

/** The draft's title row; the shared save bar owns the save action. */
function InstanceFormHeader({ mode, disabled, onCancel }: InstanceFormHeaderProps) {
  const { t } = useTranslation();
  return (
    <div className="flex min-w-0 items-center justify-between gap-2 pb-2">
      <div className="text-sm font-medium text-foreground">
        {mode === "create" ? t("instances:newInstance") : t("instances:editInstance")}
      </div>
      <Button
        type="button"
        variant="ghost"
        onClick={onCancel}
        disabled={disabled}
        className={settingsActionClassName("cursor-pointer px-3 text-sm")}
      >
        {t("common:cancel")}
      </Button>
    </div>
  );
}
