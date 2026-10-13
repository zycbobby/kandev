#!/usr/bin/env python3
"""Push this Kandev instance's raw statistics rows to a aggregating instance.

Run on the REMOTE instance (the data source) by a scheduled automation:

    python3 push.py \
        --db <kandev-home>/data/kandev.db \
        --url https://<local-instance>:38429 \
        --secret <shared-statsync-secret> \
        --source <this-instance-name>

The script reads its own SQLite database read-only, transforms the rows into
the versioned ingest contract (uid namespacing, snapshot trimming, no message
content), POSTs them to the LOCAL instance's /api/v1/statsync/ingest, and
advances its watermark state file only on a 2xx response. A failed push
leaves the watermark untouched, so the next run re-pushes the same delta and
the LOCAL side de-duplicates by uid.

Python 3 standard library only: the REMOTE machine must have python3, nothing
else. Postgres-backed instances are not supported in v1 (the preflight fails
with an explicit error).
"""

import argparse
import json
import sqlite3
import sys
import urllib.error
import urllib.request
from datetime import timedelta
from pathlib import Path

SCHEMA_VERSION = 1
DEFAULT_OVERLAP_SECONDS = 60
DEFAULT_RECONCILE_EVERY = 7
SEPARATOR = ":"

# Per-table extraction contracts: table -> (timestamp column order, row
# category, uid column). The watermark advances on the first available
# timestamp column; commits and snapshots only carry their event time.
TABLE_CONTRACTS = [
    {
        "table": "repositories",
        "timestamps": ["updated_at", "created_at"],
        "category": "repositories",
    },
    {
        "table": "tasks",
        "timestamps": ["updated_at", "created_at"],
        "category": "tasks",
    },
    {
        "table": "task_repositories",
        "timestamps": ["updated_at", "created_at"],
        "category": "task_repositories",
    },
    {
        "table": "task_sessions",
        "timestamps": ["updated_at", "started_at"],
        "category": "sessions",
    },
    {
        "table": "task_session_turns",
        "timestamps": ["updated_at", "started_at"],
        "category": "turns",
    },
    {
        "table": "task_session_messages",
        "timestamps": ["created_at"],
        "category": "messages",
    },
    {
        "table": "task_session_commits",
        "timestamps": ["committed_at"],
        "category": "commits",
    },
    {
        "table": "task_session_git_snapshots",
        "timestamps": ["created_at"],
        "category": "git_snapshots",
    },
]

# Columns each table contributes to the payload. The extractor intersects
# this list with the table's real columns (pragma_table_info) so an older
# source build that predates a later-added column (e.g. tasks.origin) simply
# omits the field instead of crashing.
TABLE_COLUMNS = {
    "repositories": ["id", "name", "remote_url"],
    "tasks": [
        "id", "workspace_id", "title", "state", "archived_at",
        "created_at", "updated_at", "workflow_step_id", "origin",
        "is_ephemeral",
    ],
    "task_repositories": ["id", "task_id", "repository_id", "created_at"],
    "task_sessions": [
        "id", "task_id", "started_at", "completed_at", "repository_id",
        "agent_profile_snapshot",
    ],
    "task_session_turns": ["id", "task_session_id", "started_at", "completed_at"],
    "task_session_messages": [
        "id", "task_session_id", "turn_id", "author_type", "type", "created_at",
    ],
    "task_session_commits": [
        "id", "session_id", "commit_sha", "committed_at",
        "files_changed", "insertions", "deletions",
    ],
    "task_session_git_snapshots": [
        "id", "session_id", "snapshot_type", "files", "created_at",
    ],
}


class PreflightError(RuntimeError):
    """The source database cannot be read; nothing was pushed."""


def parse_args(argv):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--db", required=True, help="path to this instance's kandev.db")
    parser.add_argument("--url", required=True, help="base URL of the LOCAL instance")
    parser.add_argument("--secret", required=True, help="shared statsync secret")
    parser.add_argument("--source", required=True, help="stable name of this instance")
    parser.add_argument(
        "--state",
        help="watermark state file (default: <kandev-home>/statsync-state.json, "
        "derived as the parent directory of the database's folder)",
    )
    parser.add_argument(
        "--overlap-seconds", type=int, default=DEFAULT_OVERLAP_SECONDS,
        help="re-extraction overlap window in seconds (default 60)",
    )
    parser.add_argument(
        "--reconcile-every", type=int, default=DEFAULT_RECONCILE_EVERY,
        help="attach a full ID inventory every Nth successful push (default 7)",
    )
    return parser.parse_args(argv)


def default_state_path(db_path):
    # <home>/data/kandev.db -> <home>/statsync-state.json
    return Path(db_path).resolve().parent.parent / "statsync-state.json"


def open_readonly(db_path):
    if str(db_path).startswith(("postgres://", "postgresql://")):
        raise PreflightError(
            "REMOTE extraction from PostgreSQL is not supported in v1; "
            "this script reads a local SQLite kandev.db"
        )
    path = Path(db_path)
    if not path.is_file():
        raise PreflightError("database %s does not exist" % path)
    try:
        conn = sqlite3.connect("file:%s?mode=ro" % path.as_posix(), uri=True)
    except sqlite3.Error as err:
        raise PreflightError("cannot open %s as SQLite: %s" % (path, err)) from err
    conn.row_factory = sqlite3.Row
    return conn


def table_columns(conn, table):
    rows = conn.execute("PRAGMA table_info(%s)" % table).fetchall()
    return [row["name"] for row in rows]


def to_rfc3339(value):
    """Normalize one stored SQLite timestamp to the wire format."""
    if value is None:
        return None
    text = str(value)
    try:
        parsed = datetime_fromisoformat(text)
    except ValueError:
        return text
    return parsed.isoformat()


def datetime_fromisoformat(text):
    # Local import keeps the top of the file honest about stdlib usage.
    from datetime import datetime

    parsed = datetime.fromisoformat(text)
    if parsed.tzinfo is None:
        # Stored values are UTC wall clock; make that explicit for the wire.
        from datetime import timezone

        parsed = parsed.replace(tzinfo=timezone.utc)
    return parsed


def minus_seconds(text, seconds):
    """Watermark minus the overlap, formatted in the watermark's own style.

    The boundary is compared against stored TEXT timestamps, so it must keep
    the same fractional-second style as the watermark it derives from —
    comparing a '.000000' boundary against a no-fraction stored value would
    misorder the two at the same second.
    """
    parsed = datetime_fromisoformat(text)
    boundary = parsed - timedelta(seconds=seconds)
    if "." in text:
        return boundary.strftime("%Y-%m-%d %H:%M:%S.%f") + "+00:00"
    return boundary.strftime("%Y-%m-%d %H:%M:%S") + "+00:00"


def completed_flag(conn, has_workflow_columns, row):
    """Best-effort completed boolean: archived, or current step is the last."""
    archived = row["archived_at"] if "archived_at" in row.keys() else None
    if archived is not None:
        return True
    if not has_workflow_columns:
        return None
    last = conn.execute(
        """
        SELECT 1 FROM workflow_steps current_step
        WHERE current_step.id = ?
          AND current_step.position = (
              SELECT MAX(all_steps.position) FROM workflow_steps all_steps
              WHERE all_steps.workflow_id = current_step.workflow_id
          )
        """,
        (row["workflow_step_id"],),
    ).fetchone()
    return last is not None


def extract_table(conn, contract, columns, watermark, overlap_seconds, source):
    """Extract one table's delta rows as payload rows plus the new watermark."""
    table = contract["table"]
    available = [name for name in TABLE_COLUMNS[table] if name in columns]
    ts_column = next(
        (name for name in contract["timestamps"] if name in columns), None
    )
    if "id" not in available or ts_column is None:
        # A table this old build does not meaningfully have; skip it.
        return [], None
    if ts_column not in available:
        # The watermark column is not a payload column (e.g. task_sessions
        # watermarks on updated_at but only ships started_at/completed_at);
        # it still has to be selected to track the watermark.
        available.append(ts_column)

    select_list = ", ".join(available)
    where = " WHERE is_ephemeral = 0 AND COALESCE(origin, '') != 'automation_run'" \
        if table == "tasks" and "origin" in columns else ""
    args = []
    if watermark:
        where = (" WHERE %s >= ?" % ts_column) if not where else where + " AND %s >= ?" % ts_column
        args.append(minus_seconds(watermark, overlap_seconds))
    query = "SELECT %s FROM %s%s ORDER BY %s ASC" % (select_list, table, where, ts_column)
    rows = conn.execute(query, args).fetchall()

    has_workflow_columns = table == "tasks" and "workflow_step_id" in columns
    payload_rows = []
    newest = watermark
    for row in rows:
        payload_rows.append(
            build_payload_row(conn, contract["category"], table, row, source, has_workflow_columns, columns)
        )
        raw = row[ts_column]
        if raw is not None and (newest is None or str(raw) > str(newest)):
            newest = raw
    return payload_rows, newest


def build_payload_row(conn, category, table, row, source, has_workflow_columns, columns):
    uid = source + SEPARATOR + str(row["id"])
    payload = {"uid": uid}
    if category == "tasks":
        payload.update(
            {
                "orig_workspace_id": str(row["workspace_id"] or ""),
                "title": row["title"] or "",
                "state": row["state"] or "",
                "archived_at": to_rfc3339(row["archived_at"]),
                "completed": completed_flag(conn, has_workflow_columns, row),
                "created_at": to_rfc3339(row["created_at"]),
                "updated_at": to_rfc3339(row["updated_at"]),
            }
        )
    elif category == "repositories":
        payload.update({"name": row["name"] or "", "remote_url": row["remote_url"] or ""})
    elif category == "task_repositories":
        payload.update(
            {
                "task_uid": source + SEPARATOR + str(row["task_id"]),
                "repository_id": source + SEPARATOR + str(row["repository_id"]),
                "created_at": to_rfc3339(row["created_at"]),
            }
        )
    elif category == "sessions":
        payload.update(
            {
                "task_uid": source + SEPARATOR + str(row["task_id"]),
                "started_at": to_rfc3339(row["started_at"]),
                "completed_at": to_rfc3339(row["completed_at"]),
                "repository_id": source + SEPARATOR + row["repository_id"] if row["repository_id"] else "",
                "agent_profile_snapshot": trim_snapshot(row["agent_profile_snapshot"]),
            }
        )
    elif category == "turns":
        payload.update(
            {
                "session_uid": source + SEPARATOR + str(row["task_session_id"]),
                "started_at": to_rfc3339(row["started_at"]),
                "completed_at": to_rfc3339(row["completed_at"]),
            }
        )
    elif category == "messages":
        payload.update(
            {
                "session_uid": source + SEPARATOR + str(row["task_session_id"]),
                "turn_id": source + SEPARATOR + str(row["turn_id"]) if row["turn_id"] else "",
                "author_type": row["author_type"] or "",
                "type": row["type"] or "",
                "created_at": to_rfc3339(row["created_at"]),
            }
        )
    elif category == "commits":
        payload.update(
            {
                "session_uid": source + SEPARATOR + str(row["session_id"]),
                "commit_sha": row["commit_sha"] or "",
                "committed_at": to_rfc3339(row["committed_at"]),
                "files_changed": row["files_changed"],
                "insertions": row["insertions"],
                "deletions": row["deletions"],
            }
        )
    elif category == "git_snapshots":
        payload.update(
            {
                "session_uid": source + SEPARATOR + str(row["session_id"]),
                "snapshot_type": row["snapshot_type"] or "",
                "files": parse_json_object(row["files"]),
                "created_at": to_rfc3339(row["created_at"]),
            }
        )
    return payload


def trim_snapshot(raw):
    """Keep only the analytics-relevant snapshot keys; content never travels."""
    parsed = parse_json_object(raw)
    if parsed is None:
        return None
    return {
        key: parsed[key] for key in ("model", "model_name", "llm") if key in parsed
    }


def parse_json_object(raw):
    if raw is None or raw == "":
        return None
    try:
        parsed = json.loads(raw)
    except (TypeError, ValueError):
        return None
    return parsed if isinstance(parsed, dict) else None


def extract_payload(conn, source, state, overlap_seconds, include_inventory):
    envelope = {
        "schema_version": SCHEMA_VERSION,
        "source_instance": source,
        "mode": "reconcile" if include_inventory else "delta",
    }
    for contract in TABLE_CONTRACTS:
        table = contract["table"]
        columns = table_columns(conn, table)
        rows, newest = extract_table(
            conn, contract, columns, state.get("watermarks", {}).get(table),
            overlap_seconds, source,
        )
        envelope[contract["category"]] = rows
        state.setdefault("watermarks", {})[table] = newest
    if include_inventory:
        envelope["inventory"] = extract_inventory(conn, source)
    return envelope


def extract_inventory(conn, source):
    inventory = {}
    for contract in TABLE_CONTRACTS:
        table = contract["table"]
        columns = table_columns(conn, table)
        if "id" not in columns:
            inventory[contract["category"]] = []
            continue
        rows = conn.execute("SELECT id FROM %s" % table).fetchall()
        inventory[contract["category"]] = [
            source + SEPARATOR + str(row["id"]) for row in rows
        ]
    return inventory


def load_state(path):
    try:
        return json.loads(path.read_text())
    except FileNotFoundError:
        return {"watermarks": {}, "push_count": 0}
    except ValueError as err:
        raise SystemExit("state file %s is not valid JSON: %s" % (path, err))


def save_state(path, state):
    path.write_text(json.dumps(state, indent=2, sort_keys=True) + "\n")


def push(url, secret, payload):
    """POST the payload; returns (status, body) with status None on a
    network-level failure — both leave the caller's watermark untouched."""
    request = urllib.request.Request(
        url.rstrip("/") + "/api/v1/statsync/ingest",
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json", "X-Statsync-Secret": secret},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            body = response.read().decode("utf-8", "replace")
            return response.status, body
    except urllib.error.HTTPError as err:
        return err.code, err.read().decode("utf-8", "replace")
    except urllib.error.URLError as err:
        return None, "network error: %s" % err.reason


def main(argv=None):
    args = parse_args(argv if argv is not None else sys.argv[1:])
    if sys.version_info < (3, 8):
        raise SystemExit("python3 3.8+ is required (found %s)" % sys.version.split()[0])

    state_path = Path(args.state) if args.state else default_state_path(args.db)
    state = load_state(state_path)

    conn = open_readonly(args.db)
    try:
        push_count = int(state.get("push_count", 0)) + 1
        include_inventory = push_count % max(args.reconcile_every, 1) == 0
        envelope = extract_payload(conn, args.source, state, args.overlap_seconds, include_inventory)
    finally:
        conn.close()

    status, body = push(args.url, args.secret, envelope)
    if status is None or status < 200 or status >= 300:
        print(
            "push failed (%s): %s" % (status if status is not None else "no response", body.strip()),
            file=sys.stderr,
        )
        return 1

    state["push_count"] = push_count
    save_state(state_path, state)
    print(
        "pushed %s (HTTP %d); watermark advanced to %s"
        % ("reconcile" if include_inventory else "delta", status, state_path)
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
