---
title: "Multi-Instance Federation"
description: "Register remote Kandev instances, operate their tasks from one panel, and receive their session notifications."
status: experimental
---

# Multi-Instance Federation

If you run more than one Kandev instance (for example one on your laptop and one on a workstation), multi-instance federation lets a single panel manage them: register the remote instances on the local one, switch between them from the sidebar, and see every instance's session notifications without keeping a browser tab open on each.

Multi-instance federation is an **experimental runtime feature toggle** and is disabled in every shipped profile. Enable the **Multi-instance federation** toggle in **Settings > System > Feature Toggles** (or set `KANDEV_FEATURES_MULTI_INSTANCE=true`) and restart; see [Rollout](#rollout).

## Prerequisites

- **Local instance:** a Kandev build with this feature, restarted after the toggle is enabled.
- **Remote instance:** reachable over HTTP from the local backend, and running a Kandev version at or above the local instance's minimum supported version (currently 0.98.0). A remote below the floor shows an unsupported-version error in its health banner instead of working partially.
- The remote needs **no flag or configuration of its own** to be a valid target; the endpoints federation uses are always available on a supported version.

## Security model

- The browser talks only to the local instance. For each enabled remote, the local backend opens one dedicated local proxy port and forwards that instance's API and WebSocket traffic. No existing CORS or WebSocket origin policy is weakened.
- Registering an instance **without a credential** is only for trusted networks: it federates a remote that runs with authentication off, and the proxied surface inherits that exposure.
- The recommended credential is a per-instance personal access token. Create a `kandev_pat_...` token on the remote instance ([Authentication](authentication.md)), store it as a secret ([Security](security.md)), and reference the secret ID in the instance form.
- When local authentication is enabled, browsers authenticate against the local instance: the switch mints a short-lived bridge credential, and the local backend presents the registered token to the remote. No remote login prompt appears.

## 1. Register a remote instance

Open **Settings > System > Instances** and choose **Add instance**:

- **Name:** shown in the sidebar picker and the browser tab title.
- **Base URL:** the remote backend's HTTP origin, including its port. Use the address you open the remote Kandev UI at.
- **Credential secret ID:** optional. A secret holding the remote API token. Leave it empty when the remote runs without authentication.
- **Enabled:** disabled instances keep their registration but stop proxying and event relaying.

Health is checked every 90 seconds. The settings page shows the remote as reachable with its version, an unsupported-version error, or unreachable. The registry is per user: your instances are not visible to other accounts.

## 2. Switch instances

The sidebar header has an instance picker beside the workspace picker. The local instance is always the first entry.

- Switching performs a full page reload. The panel then renders the remote instance's board, chat, and task detail, and you operate its tasks directly: prompts, moves, and stops run on the remote through the local proxy.
- Exactly one instance is active at a time; there is no merged board.
- Disabled instances are listed but cannot be selected; enable them in settings first.
- If the remote is unreachable, the proxy returns an explicit error naming the instance instead of hanging or showing stale data.
- With authentication enabled, the bridge credential is reissued by switching. If a remote view stops working after a local backend restart, switch to the instance again.

## 3. Remote notifications

Each enabled instance has a relay that keeps its own event subscription to the remote and reconciles missed events roughly once a minute, independent of which instance the panel is currently viewing.

- Remote events use the same notification logic as local ones: your notification settings (per-event subscriptions, providers, sound, browser and desktop notifications) decide how a remote event alerts you, exactly as they do for the same kind of local event.
- There is no dedicated remote-notifications module in the sidebar. A notification from a remote instance shows the instance's name as a label.
- Remote notifications offer no open action. To reach a remote task that is waiting on you, open its row in your Inbox ([Remote rows in your Inbox](#remote-rows-in-your-inbox)). For informational events, switch to the instance from the sidebar picker.
- A remote event that needs you (a blocked question or a task waiting at a manual step) also refreshes that instance's Inbox section.
- Events missed while a remote was unreachable are recovered by the reconciliation poll after reconnection, without duplicates.

What is relayed:

- A remote session finishing a turn, a remote session requesting clarification, a remote task entering a manual step that waits for you, and the Office inbox event are the live events today.
- Live delivery requires the remote to have a notification provider subscribed to those events. Without one, occurrences still arrive through the roughly one-minute reconciliation window.
- The relayed event set is a closed list. The Office inbox event is in the set, but events without an occurrence identity are not relayed; the remote's inbox-item path currently never fires in production.

## Host-bound actions while viewing a remote

Some actions bind to the machine serving the panel. While a remote instance is active, they show an explicit unavailable-for-remote explanation instead of acting:

- open in editor and open folder commands;
- the file tree's open-in-editor action;
- port-forward open actions;
- the preview panel, whose URLs are host-proxied;
- the tunnel start toggle.

Arbitrary TCP port proxying across instances is not provided.

## Remote rows in your Inbox

With both **Multi-instance federation** and the needs-you inbox enabled, your
local Inbox also lists what is waiting on you from every enabled registered
remote instance, in one section per instance: blocked questions, answerable
right there without switching panels, and tasks waiting at a manual step.
Answering a question forwards your answer to that instance through your local
backend, the remote task resumes, and the row disappears on the next read. A
waiting row offers open task, dismiss, and snooze, and disappears on the next
read once the remote no longer reports the wait. Dismissing, snoozing, and
restoring a remote row are stored on that instance, not locally, and each
row's open-task control switches the panel to the instance and opens the
task. An unreachable remote, one that needs a personal access token, one
running an older version without the merged read, or one with its own inbox
disabled shows a named, retryable state in that section instead of rows, and
never affects your local rows or the other sections. The sidebar count
includes the remote rows that have loaded.

## Limits

- No cross-instance task view or task migration: one active instance at a time, and remote data never enters the local instance's task tables.
- Absolute URLs in remote responses (share links and similar) are not rewritten: they point at the remote instance's own origin and leave the federated panel when followed.

## Rollout

The feature ships off in every profile and stays off until an all-user release. To enable it on a selected install:

1. Turn on **Multi-instance federation** in **Settings > System > Feature Toggles** (an admin override), or set `KANDEV_FEATURES_MULTI_INSTANCE=true` in the environment.
2. Restart the backend: the flag is restart-required.
3. Verify: register a remote instance and check its health banner, switch to it, operate one task, and confirm a notification appears when a remote session finishes a turn.
4. To disable it again, turn the toggle off (and restart) or unset the environment variable. Registrations are kept.

Related guides: [Feature Status](feature-status.md), [Authentication](authentication.md), [Security](security.md), and [Cross-Instance Stats Sync](stats-sync.md), the other cross-instance capability.
