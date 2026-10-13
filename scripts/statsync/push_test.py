#!/usr/bin/env python3
"""Tests for scripts/statsync/push.py: fixture DB + in-process HTTP stub.

Covers the three end-to-end paths from the change's E2E map:
1. nightly push -> 200 -> watermark advances; payload shape verified
   (path, secret header, uid namespacing, trimmed snapshot, no content);
2. push failure -> watermark untouched -> recovery re-pushes the same delta;
3. reconcile push -> full ID inventory attached -> watermark advances.
"""

import json
import sqlite3
import sys
import threading
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import push  # noqa: E402

FIXTURE_DDL = """
CREATE TABLE workspaces (id TEXT PRIMARY KEY);
CREATE TABLE repositories (
    id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, name TEXT NOT NULL,
    remote_url TEXT DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE workflow_steps (
    id TEXT PRIMARY KEY, workflow_id TEXT NOT NULL, position INTEGER NOT NULL
);
CREATE TABLE tasks (
    id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL DEFAULT '',
    workflow_step_id TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '',
    state TEXT DEFAULT 'TODO', origin TEXT DEFAULT 'manual',
    is_ephemeral INTEGER NOT NULL DEFAULT 0, archived_at TEXT,
    created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE task_repositories (
    id TEXT PRIMARY KEY, task_id TEXT NOT NULL, repository_id TEXT NOT NULL,
    created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE task_sessions (
    id TEXT PRIMARY KEY, task_id TEXT NOT NULL, started_at TEXT NOT NULL,
    completed_at TEXT, repository_id TEXT DEFAULT '',
    agent_profile_snapshot TEXT DEFAULT '{}', updated_at TEXT NOT NULL
);
CREATE TABLE task_session_turns (
    id TEXT PRIMARY KEY, task_session_id TEXT NOT NULL, started_at TEXT NOT NULL,
    completed_at TEXT, updated_at TEXT NOT NULL
);
CREATE TABLE task_session_messages (
    id TEXT PRIMARY KEY, task_session_id TEXT NOT NULL, turn_id TEXT NOT NULL,
    author_type TEXT NOT NULL DEFAULT 'user', type TEXT NOT NULL DEFAULT 'message',
    content TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);
CREATE TABLE task_session_commits (
    id TEXT PRIMARY KEY, session_id TEXT NOT NULL, commit_sha TEXT NOT NULL,
    committed_at TEXT NOT NULL, files_changed INTEGER DEFAULT 0,
    insertions INTEGER DEFAULT 0, deletions INTEGER DEFAULT 0
);
CREATE TABLE task_session_git_snapshots (
    id TEXT PRIMARY KEY, session_id TEXT, snapshot_type TEXT NOT NULL,
    files TEXT DEFAULT '{}', created_at TEXT NOT NULL
);
"""

# A timestamp deep in the past: the first run's watermark starts empty, so
# the whole fixture is the delta; later runs compare against it.
T0 = "2026-10-10 08:00:00.000000+00:00"
T1 = "2026-10-11 09:00:00.000000+00:00"
T2 = "2026-10-12 10:00:00.000000+00:00"


class IngestStub:
    """In-process stub of the LOCAL ingest endpoint.

    `statuses` optionally gives the response code per request index; the last
    entry repeats once the list is exhausted.
    """

    def __init__(self, status=200, statuses=None):
        self.requests = []
        self.status = status
        self.statuses = statuses
        self._server = HTTPServer(("127.0.0.1", 0), self._make_handler())
        self._thread = threading.Thread(target=self._server.serve_forever, daemon=True)
        self._thread.start()

    @property
    def url(self):
        return "http://127.0.0.1:%d" % self._server.server_port

    def _status_for(self, index):
        if not self.statuses:
            return self.status
        return self.statuses[index] if index < len(self.statuses) else self.statuses[-1]

    def _make_handler(self):
        stub = self

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                length = int(self.headers.get("Content-Length", "0"))
                stub.requests.append(
                    {
                        "path": self.path,
                        "secret": self.headers.get("X-Statsync-Secret"),
                        "length": length,
                        "body": json.loads(self.rfile.read(length)) if length else {},
                    }
                )
                self.send_response(stub._status_for(len(stub.requests) - 1))
                self.end_headers()
                self.wfile.write(b'{"upserts":1,"deleted":0}')

            def log_message(self, *args):
                pass

        return Handler

    def close(self):
        self._server.shutdown()
        self._server.server_close()


class PushTestBase(unittest.TestCase):
    def make_db(self, legacy_tasks=False):
        db_path = Path(self.tmp_dir) / ("kandev-%d.db" % len(self.dbs))
        self.dbs.append(db_path)
        conn = sqlite3.connect(db_path)
        conn.executescript(FIXTURE_DDL)
        if legacy_tasks:
            # An older build: no origin/is_ephemeral columns on tasks yet.
            conn.execute("ALTER TABLE tasks RENAME TO tasks_new")
            conn.executescript(
                """
                CREATE TABLE tasks (
                    id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL DEFAULT '',
                    workflow_step_id TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '',
                    state TEXT DEFAULT 'TODO', archived_at TEXT,
                    created_at TEXT NOT NULL, updated_at TEXT NOT NULL
                );
                INSERT INTO tasks (id, workspace_id, workflow_step_id, title, state, archived_at, created_at, updated_at)
                    SELECT id, workspace_id, workflow_step_id, title, state, archived_at, created_at, updated_at FROM tasks_new;
                DROP TABLE tasks_new;
                """
            )
        conn.commit()
        return db_path

    def seed_standard_fixture(self, db_path):
        conn = sqlite3.connect(db_path)
        task_columns = {
            row[1] for row in conn.execute("PRAGMA table_info(tasks)")
        }
        statements = [
            ("INSERT INTO workspaces VALUES ('ws-remote')", ()),
            (
                "INSERT INTO repositories VALUES ('repo-1', 'ws-remote', 'kandev', 'https://git.example/kandev', ?, ?)",
                (T0, T1),
            ),
            ("INSERT INTO workflow_steps VALUES ('step0', 'wf-1', 0), ('step1', 'wf-1', 1)", ()),
            (
                "INSERT INTO tasks (id, workspace_id, workflow_step_id, title, state%s, archived_at, created_at, updated_at)"
                " VALUES ('task-1', 'ws-remote', 'step1', 'Remote task', 'DONE'%s, NULL, ?, ?)"
                % (", is_ephemeral" if "is_ephemeral" in task_columns else "",
                   ", 0" if "is_ephemeral" in task_columns else ""),
                (T1, T1),
            ),
            (
                "INSERT INTO task_repositories VALUES ('tr-1', 'task-1', 'repo-1', ?, ?)",
                (T0, T0),
            ),
            (
                "INSERT INTO task_sessions (id, task_id, started_at, completed_at, repository_id, agent_profile_snapshot, updated_at)"
                " VALUES ('sess-1', 'task-1', ?, ?, 'repo-1', ?, ?)",
                (T1, T1, '{"model":"remote-model","model_name":"Remote","llm":"claude","instructions":"internal-only"}', T1),
            ),
            (
                "INSERT INTO task_session_turns (id, task_session_id, started_at, completed_at, updated_at)"
                " VALUES ('turn-1', 'sess-1', ?, ?, ?)",
                (T1, T1, T1),
            ),
            (
                "INSERT INTO task_session_messages (id, task_session_id, turn_id, author_type, type, content, created_at)"
                " VALUES ('msg-1', 'sess-1', 'turn-1', 'user', 'message', 'secret conversation content', ?)",
                (T1,),
            ),
            (
                "INSERT INTO task_session_commits (id, session_id, commit_sha, committed_at, files_changed, insertions, deletions)"
                " VALUES ('commit-1', 'sess-1', 'abc123', ?, 3, 20, 4)",
                (T2,),
            ),
            (
                "INSERT INTO task_session_git_snapshots (id, session_id, snapshot_type, files, created_at)"
                " VALUES ('snap-1', 'sess-1', 'pending', '{\"a.go\":{\"additions\":2}}', ?)",
                (T2,),
            ),
        ]
        for statement, args in statements:
            conn.execute(statement, args)
        conn.commit()
        conn.close()

    def setUp(self):
        import tempfile

        self.tmp_dir = tempfile.mkdtemp(prefix="statsync-test-")
        self.dbs = []
        self.state_path = Path(self.tmp_dir) / "statsync-state.json"

    def tearDown(self):
        import shutil

        shutil.rmtree(self.tmp_dir, ignore_errors=True)

    def run_push(self, db_path, stub, extra_args=None):
        argv = [
            "--db", str(db_path),
            "--url", stub.url,
            "--secret", "parity-secret",
            "--source", "remote-test",
            "--state", str(self.state_path),
        ] + (extra_args or [])
        return push.main(argv)

    def read_state(self):
        return json.loads(self.state_path.read_text())


class TestSuccessfulDeltaPush(PushTestBase):
    def test_push_posts_contract_and_advances_watermark(self):
        db_path = self.make_db()
        self.seed_standard_fixture(db_path)
        stub = IngestStub(status=200)
        self.addCleanup(stub.close)

        exit_code = self.run_push(db_path, stub)
        self.assertEqual(exit_code, 0)
        self.assertEqual(len(stub.requests), 1)
        request = stub.requests[0]
        self.assertEqual(request["path"], "/api/v1/statsync/ingest")
        self.assertEqual(request["secret"], "parity-secret")

        body = request["body"]
        self.assertEqual(body["schema_version"], 1)
        self.assertEqual(body["source_instance"], "remote-test")
        self.assertEqual(body["mode"], "delta")

        tasks = {row["uid"]: row for row in body["tasks"]}
        self.assertIn("remote-test:task-1", tasks)
        # The fixture task sits on its workflow's last step: best-effort
        # completed must be True even though it is not archived.
        self.assertTrue(tasks["remote-test:task-1"]["completed"])

        sessions = {row["uid"]: row for row in body["sessions"]}
        snapshot = sessions["remote-test:sess-1"]["agent_profile_snapshot"]
        self.assertEqual(
            snapshot,
            {"model": "remote-model", "model_name": "Remote", "llm": "claude"},
        )
        self.assertNotIn("instructions", snapshot)

        for message in body["messages"]:
            self.assertNotIn("content", message)
        self.assertEqual(len(body["messages"]), 1)
        self.assertEqual(body["commits"][0]["uid"], "remote-test:commit-1")
        self.assertEqual(len(body["git_snapshots"]), 1)

        state = self.read_state()
        self.assertEqual(state["push_count"], 1)
        self.assertEqual(state["watermarks"]["tasks"], T1)
        self.assertEqual(state["watermarks"]["task_session_messages"], T1)
        self.assertEqual(state["watermarks"]["task_session_commits"], T2)


class TestFailedPushRetainsWatermark(PushTestBase):
    def test_failure_keeps_state_then_recovery_repushes_same_delta(self):
        db_path = self.make_db()
        self.seed_standard_fixture(db_path)
        failing = IngestStub(status=500)
        self.addCleanup(failing.close)

        exit_code = self.run_push(db_path, failing)
        self.assertEqual(exit_code, 1)
        self.assertFalse(self.state_path.exists(), "a failed push must not create state")

        recovered = IngestStub(status=200)
        self.addCleanup(recovered.close)
        exit_code = self.run_push(db_path, recovered)
        self.assertEqual(exit_code, 0)
        self.assertEqual(len(recovered.requests), 1)
        body = recovered.requests[0]["body"]
        # The same delta is re-pushed in full: no rows were lost to the
        # failed attempt.
        self.assertEqual(len(body["tasks"]), 1)
        self.assertEqual(body["tasks"][0]["uid"], "remote-test:task-1")
        state = self.read_state()
        self.assertEqual(state["push_count"], 1)
        self.assertEqual(state["watermarks"]["tasks"], T1)


class TestReconcilePushCarriesInventory(PushTestBase):
    def test_nth_push_includes_inventory(self):
        db_path = self.make_db()
        self.seed_standard_fixture(db_path)
        stub = IngestStub(status=200)
        self.addCleanup(stub.close)

        exit_code = self.run_push(db_path, stub, ["--reconcile-every", "1"])
        self.assertEqual(exit_code, 0)
        # Row chunks go out as deltas; the reconcile envelope carrying the
        # inventory is the final POST.
        row_body = stub.requests[0]["body"]
        self.assertEqual(row_body["mode"], "delta")
        body = stub.requests[-1]["body"]
        self.assertEqual(body["mode"], "reconcile")
        inventory = body["inventory"]
        self.assertIn("remote-test:task-1", inventory["tasks"])
        self.assertIn("remote-test:repo-1", inventory["repositories"])
        self.assertIn("remote-test:sess-1", inventory["sessions"])
        self.assertEqual(inventory["commits"], ["remote-test:commit-1"])

    def test_periodic_reconcile_happens_every_nth_push(self):
        db_path = self.make_db()
        self.seed_standard_fixture(db_path)
        stub = IngestStub(status=200)
        self.addCleanup(stub.close)

        for expected_count in (1, 2):
            self.run_push(db_path, stub, ["--reconcile-every", "2"])
            state = self.read_state()
            self.assertEqual(state["push_count"], expected_count)
        modes = [request["body"]["mode"] for request in stub.requests]
        # Push 1 is a plain delta; push 2 is rows-as-delta plus the final
        # reconcile inventory envelope.
        self.assertEqual(modes, ["delta", "delta", "reconcile"])


class TestIncrementalExtraction(PushTestBase):
    def test_second_run_only_pushes_new_rows(self):
        db_path = self.make_db()
        self.seed_standard_fixture(db_path)
        stub = IngestStub(status=200)
        self.addCleanup(stub.close)
        self.run_push(db_path, stub)

        conn = sqlite3.connect(db_path)
        conn.execute(
            "INSERT INTO tasks (id, workspace_id, title, state, is_ephemeral, created_at, updated_at)"
            " VALUES ('task-2', 'ws-remote', 'Newer task', 'TODO', 0, ?, ?)",
            (T2, T2),
        )
        conn.commit()
        conn.close()

        self.run_push(db_path, stub)
        # The overlap window re-pushes rows within 60s of the watermark, so
        # the second run carries the new row plus the tail; the third run
        # (nothing new) must push nothing older than that tail.
        second_uids = [row["uid"] for row in stub.requests[1]["body"]["tasks"]]
        self.assertIn("remote-test:task-2", second_uids)
        self.run_push(db_path, stub)
        third_uids = [row["uid"] for row in stub.requests[2]["body"]["tasks"]]
        self.assertEqual(third_uids, ["remote-test:task-2"])

    def test_legacy_schema_without_new_columns_still_extracts(self):
        db_path = self.make_db(legacy_tasks=True)
        self.seed_standard_fixture(db_path)
        stub = IngestStub(status=200)
        self.addCleanup(stub.close)

        exit_code = self.run_push(db_path, stub)
        self.assertEqual(exit_code, 0)
        body = stub.requests[0]["body"]
        self.assertEqual(len(body["tasks"]), 1)
        self.assertEqual(body["tasks"][0]["uid"], "remote-test:task-1")

    def test_automation_rows_and_ephemeral_tasks_are_excluded(self):
        db_path = self.make_db()
        conn = sqlite3.connect(db_path)
        conn.execute(
            "INSERT INTO tasks (id, workspace_id, title, state, origin, is_ephemeral, created_at, updated_at)"
            " VALUES ('task-auto', 'ws-remote', 'Automation run', 'DONE', 'automation_run', 0, ?, ?)",
            (T1, T1),
        )
        conn.execute(
            "INSERT INTO tasks (id, workspace_id, title, state, origin, is_ephemeral, created_at, updated_at)"
            " VALUES ('task-eph', 'ws-remote', 'Ephemeral', 'DONE', 'manual', 1, ?, ?)",
            (T1, T1),
        )
        conn.commit()
        conn.close()

        stub = IngestStub(status=200)
        self.addCleanup(stub.close)
        self.run_push(db_path, stub)
        self.assertEqual(stub.requests[0]["body"]["tasks"], [])


def synthetic_envelope(row_count, pad_bytes, source="remote-1", inventory=None):
    """A delta envelope with heavy rows, built without touching a database."""
    categories = [contract["category"] for contract in push.TABLE_CONTRACTS]
    envelope = {
        "schema_version": push.SCHEMA_VERSION,
        "source_instance": source,
        "mode": "delta",
    }
    for category in categories:
        envelope[category] = [
            {"uid": "%s:%s-%d" % (source, category, i), "pad": "x" * pad_bytes}
            for i in range(row_count)
        ]
    if inventory is not None:
        envelope["inventory"] = inventory
    return envelope


class TestChunkEnvelope(PushTestBase):
    """Pure splitter tests: no HTTP, no database."""

    def test_chunks_fit_budget_and_cover_every_row_exactly_once(self):
        envelope = synthetic_envelope(row_count=40, pad_bytes=200)
        budget = 3000
        chunks = push.chunk_envelope(envelope, budget)
        self.assertGreater(len(chunks), 1)
        for category in envelope:
            if category in ("schema_version", "source_instance", "mode"):
                continue
            shipped = [row["uid"] for chunk in chunks for row in chunk[category]]
            self.assertEqual(shipped, [row["uid"] for row in envelope[category]])
        for chunk in chunks:
            self.assertEqual(chunk["schema_version"], push.SCHEMA_VERSION)
            self.assertEqual(chunk["source_instance"], envelope["source_instance"])
            self.assertEqual(chunk["mode"], "delta")
            self.assertNotIn("inventory", chunk)
            size = len(json.dumps(chunk))
            if size > budget:
                # Only a single unsplittable row may overflow the budget.
                carried = sum(len(rows) for rows in chunk.values())
                self.assertEqual(carried, 1, "multi-row chunk %d bytes > budget" % size)

    def test_single_oversized_row_gets_its_own_chunk(self):
        envelope = synthetic_envelope(row_count=1, pad_bytes=0)
        envelope["git_snapshots"] = [{"uid": "remote-1:huge", "pad": "x" * 5000}]
        chunks = push.chunk_envelope(envelope, 1000)
        shipped = [row["uid"] for chunk in chunks for row in chunk["git_snapshots"]]
        self.assertEqual(shipped, ["remote-1:huge"])

    def test_inventory_rides_last_as_reconcile(self):
        inventory = {
            "repositories": [], "tasks": ["remote-1:task-0"], "task_repositories": [],
            "sessions": [], "turns": [], "messages": [], "commits": [], "git_snapshots": [],
        }
        envelope = synthetic_envelope(row_count=4, pad_bytes=10, inventory=inventory)
        chunks = push.chunk_envelope(envelope, 10 ** 9)
        final = chunks[-1]
        self.assertEqual(final["mode"], "reconcile")
        self.assertEqual(final["inventory"], inventory)
        self.assertEqual(sum(len(rows) for rows in final.values() if isinstance(rows, list)), 0)
        for chunk in chunks[:-1]:
            self.assertEqual(chunk["mode"], "delta")
            self.assertNotIn("inventory", chunk)

    def test_empty_delta_still_posts_one_chunk(self):
        envelope = synthetic_envelope(row_count=0, pad_bytes=0)
        chunks = push.chunk_envelope(envelope, 3000)
        self.assertEqual(len(chunks), 1)
        self.assertEqual(chunks[0]["mode"], "delta")


class TestChunkedPushOverHttp(PushTestBase):
    def seed_many_snapshots(self, db_path, count, pad_bytes=150):
        conn = sqlite3.connect(db_path)
        conn.execute("INSERT INTO workspaces VALUES ('ws-remote')")
        for i in range(count):
            conn.execute(
                "INSERT INTO task_session_git_snapshots (id, session_id, snapshot_type, files, created_at)"
                " VALUES (?, NULL, 'pending', ?, ?)",
                ("snap-%d" % i, '{"f%d.go":{"pad":"%s"}}' % (i, "x" * pad_bytes), T1),
            )
        conn.commit()
        conn.close()

    def set_small_budget(self, budget):
        original = push.MAX_POST_BYTES
        push.MAX_POST_BYTES = budget
        self.addCleanup(setattr, push, "MAX_POST_BYTES", original)

    def test_large_delta_posts_multiple_chunks_and_advances_watermark(self):
        db_path = self.make_db()
        self.seed_many_snapshots(db_path, 40)
        self.set_small_budget(2000)
        stub = IngestStub(status=200)
        self.addCleanup(stub.close)

        exit_code = self.run_push(db_path, stub)
        self.assertEqual(exit_code, 0)
        self.assertGreater(len(stub.requests), 1)
        for request in stub.requests:
            self.assertLessEqual(request["length"], push.MAX_POST_BYTES)
            self.assertEqual(request["body"]["mode"], "delta")
        shipped = [
            row["uid"]
            for request in stub.requests
            for row in request["body"]["git_snapshots"]
        ]
        self.assertEqual(len(shipped), 40)
        self.assertEqual(len(set(shipped)), 40)
        state = self.read_state()
        self.assertEqual(state["push_count"], 1)
        self.assertEqual(state["watermarks"]["task_session_git_snapshots"], T1)

    def test_mid_chunk_failure_keeps_watermark_and_stops(self):
        db_path = self.make_db()
        self.seed_many_snapshots(db_path, 40)
        self.set_small_budget(2000)
        stub = IngestStub(statuses=[200, 500])
        self.addCleanup(stub.close)

        exit_code = self.run_push(db_path, stub)
        self.assertEqual(exit_code, 1)
        self.assertEqual(len(stub.requests), 2, "must stop at the first failed chunk")
        self.assertFalse(self.state_path.exists(), "a failed push must not create state")

    def test_reconcile_run_posts_row_chunks_then_inventory_last(self):
        db_path = self.make_db()
        self.seed_standard_fixture(db_path)
        self.set_small_budget(2000)
        stub = IngestStub(status=200)
        self.addCleanup(stub.close)

        exit_code = self.run_push(db_path, stub, ["--reconcile-every", "1"])
        self.assertEqual(exit_code, 0)
        modes = [request["body"]["mode"] for request in stub.requests]
        self.assertEqual(modes[-1], "reconcile")
        self.assertEqual(modes[:-1], ["delta"] * (len(modes) - 1))
        inventory = stub.requests[-1]["body"]["inventory"]
        self.assertIn("remote-test:task-1", inventory["tasks"])
        self.assertIn("remote-test:repo-1", inventory["repositories"])
        self.assertIn("remote-test:sess-1", inventory["sessions"])
        self.assertEqual(inventory["commits"], ["remote-test:commit-1"])


class TestPreflight(PushTestBase):
    def test_missing_database_fails_cleanly(self):
        stub = IngestStub(status=200)
        self.addCleanup(stub.close)
        with self.assertRaises(push.PreflightError):
            self.run_push(Path(self.tmp_dir) / "missing.db", stub)

    def test_postgres_url_is_rejected_explicitly(self):
        stub = IngestStub(status=200)
        self.addCleanup(stub.close)
        argv = [
            "--db", "postgres://remote/db",
            "--url", stub.url,
            "--secret", "s",
            "--source", "remote-test",
            "--state", str(self.state_path),
        ]
        with self.assertRaises(push.PreflightError) as ctx:
            push.main(argv)
        self.assertIn("PostgreSQL", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
