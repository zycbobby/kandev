import { selectInstance } from "@/lib/api/domains/instances-api";
import { LOCAL_INSTANCE_ID } from "@/lib/state/slices/instances/types";

/**
 * Switches the panel to another instance: POST select against the LOCAL
 * backend (the registry lives there, not behind the instance proxy), then a
 * full page reload so the Go shell re-boots the SPA for that instance. A
 * targetPath (for example /t/:taskId) deep-links into the target instance;
 * the kandev_instance query keeps that load on the target instance.
 */
export async function switchInstance(instanceId: string, targetPath?: string): Promise<void> {
  if (instanceId === LOCAL_INSTANCE_ID) {
    // The local instance has no registry row to select, and a remote
    // selection cookie may still be set; the shell treats an unknown id
    // (including 'local') as the local boot, so the query pins this load to
    // local without a select round-trip.
    window.location.assign(instanceHref(targetPath ?? "/", instanceId));
    return;
  }
  const { href } = await selectInstance(instanceId);
  window.location.assign(targetPath ? instanceHref(targetPath, instanceId) : href);
}

function instanceHref(path: string, instanceId: string): string {
  return `${path}?kandev_instance=${encodeURIComponent(instanceId)}`;
}
