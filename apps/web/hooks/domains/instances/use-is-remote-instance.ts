import { useAppStore } from "@/components/state-provider";
import { LOCAL_INSTANCE_ID } from "@/lib/state/slices/instances/types";

/**
 * True while the panel views a remote instance: the shell-injected active
 * instance id differs from 'local'. Host-bound actions (open in
 * editor/folder, port-proxy previews) must degrade behind this flag, because
 * their command would run on the remote host rather than the machine serving
 * the panel.
 */
export function useIsRemoteInstance(): boolean {
  const activeId = useAppStore((s) => s.instances.activeId);
  return activeId !== LOCAL_INSTANCE_ID;
}
