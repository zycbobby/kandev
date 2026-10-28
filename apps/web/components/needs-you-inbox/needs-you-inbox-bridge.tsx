"use client";

import { useNeedsYouInboxController } from "@/hooks/domains/needs-you-inbox/use-needs-you-inbox-controller";
import { useRemoteInboxController } from "@/hooks/domains/needs-you-inbox/use-remote-inbox-controller";

/**
 * Mounts the Needs-you Inbox refresh triggers at the app shell, so they run
 * regardless of route: the local workspace pages and, when both flags allow,
 * every registered remote instance page. Renders nothing; mirrors
 * WebSocketConnector and the other app-shell bridges.
 */
export function NeedsYouInboxBridge() {
  useNeedsYouInboxController();
  useRemoteInboxController();
  return null;
}
