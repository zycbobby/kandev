"use client";

import { useEffect, useState } from "react";
import { fetchFeatureFlags } from "@/lib/api/domains/features-api";
import { getLocalBackendConfig } from "@/lib/config";
import { useFeature } from "@/hooks/domains/features/use-feature";
import {
  defaultFeatureFlags,
  type FeatureName,
  type FeatureFlags,
} from "@/lib/state/slices/features/types";

// One local-features read per app session. The store's hydrated flags
// describe the ACTIVE instance's app-state, so while the panel views a
// remote instance they are the remote's flags, and surfaces that must follow
// the LOCAL backend (the instance picker, the notifications module) would
// hide behind a remote that does not enable the feature itself. The local
// backend's /api/v1/features is public, needs no credentials, and changes
// only with a restart, so a cached single read is authoritative for the
// session.
let localFlagsPromise: Promise<FeatureFlags> | undefined;

function readLocalFeatureFlags(): Promise<FeatureFlags> {
  localFlagsPromise ??= fetchFeatureFlags({
    baseUrl: getLocalBackendConfig().apiBaseUrl,
  }).catch(() => defaultFeatureFlags);
  return localFlagsPromise;
}

/** Drops the per-session cache so test suites can vary responses. */
export function resetLocalFeatureFlagsForTests(): void {
  localFlagsPromise = undefined;
}

/**
 * Read one feature flag from the LOCAL backend rather than the store's
 * hydrated flags. The hydrated value renders first (it is correct on a local
 * boot, where the shell payload carries the local backend's own flags); the
 * local read then overrides it, so a remote view cannot strand a local-only
 * surface behind the remote's flag state.
 */
export function useLocalFeatureFlag(name: FeatureName): boolean {
  const hydrated = useFeature(name);
  const [localEnabled, setLocalEnabled] = useState<boolean | null>(null);
  useEffect(() => {
    let active = true;
    void readLocalFeatureFlags().then((flags) => {
      if (active) setLocalEnabled(flags[name] === true);
    });
    return () => {
      active = false;
    };
  }, [name]);
  return localEnabled ?? hydrated;
}
