---
title: "Cross-Instance Stats Sync"
description: "Merge a second Kandev instance's activity into this instance's /stats page with a scheduled automation and the statsync push script."
---

# Cross-Instance Stats Sync

If you run more than one Kandev instance, each instance's `/stats` page only counts its own activity. Stats sync lets one instance (LOCAL) display merged statistics: a scheduled automation on the other instance (REMOTE) periodically pushes its raw activity rows to LOCAL, and LOCAL's `/stats` sections count both sides.

LOCAL runs current Kandev code and owns all configuration. REMOTE needs no code or configuration change beyond one scheduled automation.

Stats sync only merges statistics. To operate remote instances and receive their notifications from one panel, see the experimental [Multi-Instance Federation](multi-instance.md) guide.

## How it works

- REMOTE runs `scripts/statsync/push.py` on a schedule. The script reads REMOTE's own SQLite database read-only, extracts rows newer than its watermark, and POSTs them to LOCAL.
- LOCAL stores the rows in dedicated mirror tables. They never appear in task lists, boards, or task events; only the `/stats` sections read them.
- Every row is keyed by `source + original id`, so re-pushing the same rows is safe. The watermark advances only after LOCAL answers 2xx: a failed push is retried in full by the next run.
- Every Nth push (default 7) carries a full ID inventory instead of a delta, so rows deleted on REMOTE disappear from LOCAL's statistics after that run.

## Prerequisites

- LOCAL: a Kandev instance on a build with stats sync (this feature), reachable from REMOTE over the network during the push window.
- REMOTE: `python3` 3.8 or newer on its PATH (standard library only, no packages), and a Kandev home backed by SQLite. Postgres-backed instances are not supported in v1; the script exits with an explicit error.
- REMOTE's automation must run without waiting for permission prompts: use an executor of `local` or `worktree`, and pre-approve the script's command in the agent profile. See [Automations and MCP](automation-and-mcp.md) for scheduled automation setup.

## 1. Configure LOCAL

Add the shared secret (and optionally the workspace mapping) to LOCAL's `config.yaml`:

```yaml
statsync:
  ingestSecret: "choose-a-long-random-secret"
  workspaceMappings:
    remote-1: ws-main
```

The environment variable `KANDEV_STATSYNC_INGEST_SECRET` is the compatible alias for the secret. Restart LOCAL after changing the file.

- `ingestSecret`: required to enable the endpoint. While it is unset, LOCAL registers no route at all.
- `workspaceMappings`: maps the REMOTE instance's `--source` name to a LOCAL workspace id. A source without an entry is filed under LOCAL's primary workspace, so a single-instance setup can omit the mapping.

## 2. Copy the script to REMOTE

Copy `scripts/statsync/push.py` from the repository to any path on the REMOTE machine, for example `<kandev-home>/statsync/push.py`.

## 3. Create the scheduled automation on REMOTE

Create one scheduled automation on the REMOTE instance (configuration chat or workspace automation, see [Automations and MCP](automation-and-mcp.md)) with a cron schedule and timezone, for example nightly at 02:30 Asia/Shanghai, and a prompt that runs exactly this command:

```bash
python3 <kandev-home>/statsync/push.py \
  --db <kandev-home>/data/kandev.db \
  --url https://local.example.com \
  --secret <same-secret-as-LOCAL> \
  --source remote-1
```

Useful options:

- `--state`: where the watermark file lives. The default is `<kandev-home>/statsync-state.json`.
- `--overlap-seconds`: re-extraction overlap (default 60). Leave it alone unless clocks are badly skewed.
- `--reconcile-every`: attach a full ID inventory every Nth push (default 7).

## Limits and recovery

- A failed push (REMOTE cannot reach LOCAL, wrong secret, LOCAL down) leaves the watermark untouched. The next scheduled run re-pushes the whole accumulated delta; LOCAL de-duplicates, so nothing is double-counted.
- The script splits a large delta (a first push ships the instance's whole history) into POSTs of at most 16 MiB. The reconcile inventory travels in its own final POST. A single row bigger than one POST still fails the push, with the endpoint's real HTTP status: the ingest endpoint consumes the unread request body before rejecting, so senders see 413/401 instead of a connection reset.
- REMOTE databases on PostgreSQL are rejected by the preflight in v1.
- Rows deleted on REMOTE converge only on the reconcile push, so a deletion can remain visible for up to N pushes.
- Message content never leaves REMOTE: the payload carries counts, types, and timestamps, plus the agent profile snapshot trimmed to `model`, `model_name`, and `llm`.

## Ingest endpoint reference

`POST /api/v1/statsync/ingest` with header `X-Statsync-Secret`.

| Status | Meaning |
| --- | --- |
| 200 | Stored; body reports `upserts` and `deleted` counts. |
| 400 | Payload violates the contract (unknown `schema_version`, missing fields, non-RFC3339 timestamps). Nothing is written. |
| 401 | Missing or wrong secret. |
| 403 | The `source_instance` has no resolvable workspace mapping. |

The endpoint authenticates with the shared secret alone (compared in constant time) even when LOCAL has authentication enabled; it is on the same allowlist as other self-authenticating webhooks. See [Authentication](authentication.md).
