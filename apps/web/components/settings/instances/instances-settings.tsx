"use client";

import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Button } from "@kandev/ui/button";
import { useFeature } from "@/hooks/domains/features/use-feature";
import { useToast } from "@/components/toast-provider";
import { settingsActionClassName } from "@/components/settings/settings-control";
import { SettingsGroup } from "@/components/settings/settings-group";
import { SettingsPageTemplate } from "@/components/settings/settings-page-template";
import {
  createInstance,
  deleteInstance,
  getInstanceHealth,
  listInstances,
  updateInstance,
  type InstanceHealth,
  type RemoteInstance,
} from "@/lib/api/domains/instances-api";
import { InstanceForm, emptyInstanceForm, type InstanceFormState } from "./instance-form";
import { InstanceListRow } from "./instance-list-row";

// Registry reads are always fresh: health reports and the row set change on
// the backend between visits.
const NO_STORE = { cache: "no-store" as const };

function instanceToDraft(item: RemoteInstance): InstanceFormState {
  return {
    name: item.name,
    baseUrl: item.base_url,
    credentialSecretId: item.credential_secret_id ?? "",
    enabled: item.enabled,
  };
}

function draftToPayload(form: InstanceFormState) {
  return {
    name: form.name.trim(),
    base_url: form.baseUrl.trim(),
    credential_secret_id: form.credentialSecretId.trim(),
    enabled: form.enabled,
  };
}

function draftIsValid(form: InstanceFormState): boolean {
  return form.name.trim() !== "" && form.baseUrl.trim() !== "";
}

type InstancesRegistry = {
  items: RemoteInstance[];
  healthById: Record<string, InstanceHealth>;
  loading: boolean;
  load: () => Promise<void>;
  applyItem: (item: RemoteInstance) => void;
  removeItem: (id: string) => void;
  refreshHealth: (id: string) => Promise<void>;
};

/** Loads the registry plus each instance's last health report. */
function useInstancesRegistry(t: TFunction): InstancesRegistry {
  const { toast } = useToast();
  const [items, setItems] = useState<RemoteInstance[]>([]);
  const [healthById, setHealthById] = useState<Record<string, InstanceHealth>>({});
  const [loading, setLoading] = useState(true);

  const refreshHealth = useCallback(async (id: string) => {
    try {
      const health = await getInstanceHealth(id, NO_STORE);
      setHealthById((prev) => ({ ...prev, [id]: health }));
    } catch {
      // A failed health read keeps the previous report on screen; the next
      // page load retries it.
    }
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const loaded = await listInstances(NO_STORE);
      setItems(loaded);
      setHealthById({});
      await Promise.all(loaded.map((item) => refreshHealth(item.id)));
    } catch (err) {
      toast({ description: t("instances:loadFailed", { error: String(err) }), variant: "error" });
    } finally {
      setLoading(false);
    }
  }, [refreshHealth, t, toast]);

  useEffect(() => {
    void load();
  }, [load]);

  const applyItem = useCallback((item: RemoteInstance) => {
    setItems((prev) => {
      const index = prev.findIndex((candidate) => candidate.id === item.id);
      if (index === -1) return [...prev, item];
      const next = [...prev];
      next[index] = item;
      return next;
    });
  }, []);

  const removeItem = useCallback((id: string) => {
    setItems((prev) => prev.filter((candidate) => candidate.id !== id));
    setHealthById((prev) => {
      if (!(id in prev)) return prev;
      const next = { ...prev };
      delete next[id];
      return next;
    });
  }, []);

  return { items, healthById, loading, load, applyItem, removeItem, refreshHealth };
}

type InstanceDraft = {
  showCreate: boolean;
  editingId: string | null;
  form: InstanceFormState;
  baseline: InstanceFormState;
  isDirty: boolean;
  isValid: boolean;
  revision: string;
  updateForm: (patch: Partial<InstanceFormState>) => void;
  startCreate: () => void;
  startEdit: (item: RemoteInstance) => void;
  reset: () => void;
  discard: () => void;
};

/** The staged create/edit draft; the shared save contributor owns saving it. */
function useInstanceDraft(items: RemoteInstance[]): InstanceDraft {
  const [showCreate, setShowCreate] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [form, setForm] = useState<InstanceFormState>(emptyInstanceForm);

  const editing = items.find((item) => item.id === editingId) ?? null;
  const baseline = editing ? instanceToDraft(editing) : emptyInstanceForm;
  const formDiffers = JSON.stringify(form) !== JSON.stringify(baseline);
  const isDirty = showCreate || (editingId !== null && formDiffers);
  const reset = () => {
    setEditingId(null);
    setShowCreate(false);
    setForm(emptyInstanceForm);
  };

  return {
    showCreate,
    editingId,
    form,
    baseline,
    isDirty,
    isValid: draftIsValid(form),
    revision: `${showCreate ? "create" : (editingId ?? "none")}:${JSON.stringify(form)}`,
    updateForm: (patch) => setForm((prev) => ({ ...prev, ...patch })),
    startCreate: () => {
      setEditingId(null);
      setShowCreate(true);
      setForm(emptyInstanceForm);
    },
    startEdit: (item) => {
      setEditingId(item.id);
      setShowCreate(false);
      setForm(instanceToDraft(item));
    },
    reset,
    // Discard restores the saved baseline for an edit draft; a create draft
    // has no baseline, so discarding it closes the form.
    discard: () => {
      if (editingId !== null) setForm(baseline);
      else reset();
    },
  };
}

/**
 * The instance management settings page: the registry list with per-instance
 * health, plus the staged create/edit draft and immediate removal.
 */
export function InstancesSettings() {
  const multiInstanceEnabled = useFeature("multiInstance");
  if (!multiInstanceEnabled) return null;
  return <InstancesSettingsContent />;
}

type InstanceMutations = {
  save: () => Promise<void>;
  remove: (item: RemoteInstance) => Promise<void>;
};

/**
 * The registry write paths: create or update the staged draft, and remove an
 * instance. Save failures rethrow so the shared coordinator reports them.
 */
function useInstanceMutations(
  registry: InstancesRegistry,
  draft: InstanceDraft,
  t: TFunction,
): InstanceMutations {
  const { toast } = useToast();
  const { applyItem, refreshHealth, removeItem } = registry;

  const save = useCallback(async () => {
    try {
      if (draft.showCreate) {
        const created = await createInstance(draftToPayload(draft.form), NO_STORE);
        applyItem(created);
        void refreshHealth(created.id);
        draft.reset();
        toast({ description: t("instances:instanceAdded"), variant: "success" });
        return;
      }
      if (!draft.editingId) return;
      const saved = await updateInstance(draft.editingId, draftToPayload(draft.form), NO_STORE);
      applyItem(saved);
      void refreshHealth(saved.id);
      draft.reset();
      toast({ description: t("instances:instanceSaved"), variant: "success" });
    } catch (err) {
      toast({ description: t("instances:saveFailed", { error: String(err) }), variant: "error" });
      throw err;
    }
  }, [applyItem, draft, refreshHealth, t, toast]);

  const remove = useCallback(
    async (item: RemoteInstance) => {
      try {
        await deleteInstance(item.id, NO_STORE);
        removeItem(item.id);
        toast({ description: t("instances:instanceRemoved"), variant: "success" });
      } catch (err) {
        toast({
          description: t("instances:deleteFailed", { error: String(err) }),
          variant: "error",
        });
      }
    },
    [removeItem, t, toast],
  );

  return { save, remove };
}

function InstancesSettingsContent() {
  const { t } = useTranslation();
  const registry = useInstancesRegistry(t);
  const draft = useInstanceDraft(registry.items);
  const { save, remove } = useInstanceMutations(registry, draft, t);

  const invalidReason =
    draft.isDirty && !draft.isValid ? t("instances:nameAndBaseUrlRequired") : undefined;
  const draftOpen = draft.showCreate || draft.editingId !== null;

  return (
    <SettingsPageTemplate
      title={t("instances:navInstances")}
      showPageChrome={false}
      contentFrame="none"
      saveStatus="idle"
      isDirty={draft.isDirty}
      saveId="instances-registry-draft"
      saveRevision={draft.revision}
      canSave={!draft.isDirty || draft.isValid}
      invalidReason={invalidReason}
      onSave={save}
      onDiscard={draft.discard}
    >
      <SettingsGroup
        title={t("instances:groupTitle")}
        description={t("instances:groupDescription")}
        contentClassName="space-y-3 divide-y-0"
        action={
          <Button
            type="button"
            onClick={draft.startCreate}
            disabled={registry.loading || draftOpen}
            className={settingsActionClassName("cursor-pointer px-3 text-sm")}
          >
            {t("instances:addInstance")}
          </Button>
        }
      >
        {draftOpen && (
          <InstanceForm
            mode={draft.showCreate ? "create" : "edit"}
            form={draft.form}
            baseline={draft.baseline}
            disabled={registry.loading}
            onFormChange={draft.updateForm}
            onCancel={draft.reset}
          />
        )}
        {registry.loading && (
          <div className="rounded-lg border border-dashed border-border/70 p-6 text-sm text-muted-foreground">
            {t("instances:loadingInstances")}
          </div>
        )}
        {!registry.loading && registry.items.length === 0 && !draftOpen && (
          <div className="rounded-lg border border-dashed border-border/70 p-6 text-sm text-muted-foreground">
            {t("instances:noInstances")}
          </div>
        )}
        {registry.items.map((item) => (
          <InstanceListRow
            key={item.id}
            instance={item}
            health={registry.healthById[item.id] ?? null}
            isBusy={registry.loading}
            isEditing={draft.editingId === item.id}
            onEdit={draft.startEdit}
            onDelete={remove}
          />
        ))}
      </SettingsGroup>
    </SettingsPageTemplate>
  );
}
