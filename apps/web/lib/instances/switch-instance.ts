import { selectInstance } from "@/lib/api/domains/instances-api";
import { LOCAL_INSTANCE_ID } from "@/lib/state/slices/instances/types";

/**
 * Switches the panel to another instance: POST select against the LOCAL
 * backend (the registry lives there, not behind the instance proxy), then a
 * full page reload so the Go shell re-boots the SPA for that instance. A
 * targetPath (for example /t/:taskId) deep-links into the target instance;
 * for a remote instance the kandev_instance query pins that load to it.
 * Selecting the local instance clears the selection cookie, so its reload
 * carries no query pin: a query-less load stays local.
 */
export async function switchInstance(instanceId: string, targetPath?: string): Promise<void> {
  const { href } = await selectInstance(instanceId);
  if (instanceId === LOCAL_INSTANCE_ID) {
    window.location.assign(targetPath ?? href);
    return;
  }
  window.location.assign(targetPath ? instanceHref(targetPath, instanceId) : href);
}

function instanceHref(path: string, instanceId: string): string {
  return `${path}?kandev_instance=${encodeURIComponent(instanceId)}`;
}
