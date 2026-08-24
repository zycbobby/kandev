#!/usr/bin/env bash
# sync-workflow.test.sh — exercise workflow export against a local HTTP stub.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
status=0

pass() {
	printf 'ok    %s\n' "$1"
}

fail() {
	printf 'FAIL  %s\n' "$1" >&2
	status=1
}

help_out="$(make -C "$ROOT_DIR" --no-print-directory help)"
if printf '%s\n' "$help_out" | grep -Eq '^[[:space:]]+sync-workflow[[:space:]]'; then
	pass "make help lists sync-workflow"
else
	fail "make help lists sync-workflow"
fi

dry_out=""
if dry_out="$(make -C "$ROOT_DIR" --no-print-directory -n sync-workflow 2>/dev/null)" && printf '%s\n' "$dry_out" | grep -Fq 'python3 scripts/sync-workflow.py'; then
	pass "make -n sync-workflow invokes sync-workflow.py"
else
	fail "make -n sync-workflow invokes sync-workflow.py"
fi

server_dir="$(mktemp -d)"
output_dir="$(mktemp -d)"
port_file="$server_dir/port"
server_pid=""
cleanup() {
	if [ -n "$server_pid" ]; then
		kill "$server_pid" 2>/dev/null || true
		wait "$server_pid" 2>/dev/null || true
	fi
	rm -rf "$server_dir" "$output_dir"
}
trap cleanup EXIT

python3 - "$port_file" <<'PY' &
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

port_file = sys.argv[1]
exports = {
    "wf-1": "Kanban",
    "wf-2": "PR Review",
    "wf-3": "Kanban",
}

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        parsed = urlparse(self.path)
        if parsed.path == "/api/v1/workspaces":
            body = {"workspaces": [
                {"id": "ws-1", "name": "Alpha", "owner_id": "", "task_prefix": "KAN"},
                {"id": "ws-2", "name": "Beta", "owner_id": "", "task_prefix": "KAN"},
                {"id": "ws-3", "name": "Empty", "owner_id": "", "task_prefix": "KAN"},
            ], "total": 3}
            self.send_json(body)
            return
        if parsed.path == "/api/v1/workflows":
            workspace_id = parse_qs(parsed.query).get("workspace_id", [""])[0]
            workflows = {
                "ws-1": [{"id": "wf-1", "name": "Kanban"}, {"id": "wf-2", "name": "PR Review"}],
                "ws-2": [{"id": "wf-3", "name": "Kanban"}],
                "ws-3": [],
            }.get(workspace_id, [])
            self.send_json({"workflows": workflows, "total": len(workflows)})
            return
        if parsed.path.startswith("/api/v1/workflows/") and parsed.path.endswith("/export"):
            workflow_id = parsed.path.split("/")[4]
            name = exports.get(workflow_id)
            if name is not None:
                body = ("version: 1\ntype: kandev_workflow\nworkflows:\n"
                        f"    - name: {name}\n      steps: []\n").encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/x-yaml")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
                return
        self.send_error(404)

    def send_json(self, value):
        body = json.dumps(value).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_args):
        pass

server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
with open(port_file, "w", encoding="ascii") as file:
    file.write(str(server.server_port))
    file.flush()
server.serve_forever()
PY
server_pid=$!
for _ in $(seq 1 100); do
	if [ -s "$port_file" ]; then break; fi
	if ! kill -0 "$server_pid" 2>/dev/null; then
		fail "stub server starts"
		break
	fi
	sleep 0.01
done
if [ ! -s "$port_file" ]; then
	fail "stub server starts"
fi

if [ -s "$port_file" ]; then
	stub_url="http://127.0.0.1:$(<"$port_file")"
	if python3 "$ROOT_DIR/scripts/sync-workflow.py" "$stub_url" "$output_dir" >/dev/null; then
		pass "sync-workflow exports from stub"
	else
		fail "sync-workflow exports from stub"
	fi

	expected_files=("kanban.yml" "pr-review.yml" "kanban--beta.yml")
	actual_files="$(find "$output_dir" -maxdepth 1 -type f -printf '%f\n' | sort)"
	expected_listing="$(printf '%s\n' "${expected_files[@]}" | sort)"
	if [ "$actual_files" = "$expected_listing" ]; then
		pass "sync-workflow writes expected filenames"
	else
		fail "sync-workflow writes expected filenames: $actual_files"
	fi

	for file in "${expected_files[@]}"; do
		if grep -Fq 'type: kandev_workflow' "$output_dir/$file"; then
			pass "$file contains workflow type"
		else
			fail "$file contains workflow type"
		fi
		name_count="$(grep -Ec '^[[:space:]]+- name:' "$output_dir/$file")"
		if [ "$name_count" -eq 1 ]; then
			pass "$file contains one workflow name"
		else
			fail "$file contains one workflow name"
		fi
	done

	if [ ! -e "$output_dir/empty.yml" ]; then
		pass "empty workspace produces no file"
	else
		fail "empty workspace produces no file"
	fi
fi

# Stop the live server, then reuse its now-closed port for the failure path.
if [ -n "$server_pid" ]; then
	kill "$server_pid" 2>/dev/null || true
	wait "$server_pid" 2>/dev/null || true
	server_pid=""
fi
if [ -n "${stub_url:-}" ]; then
	failure_stderr="$server_dir/failure.stderr"
	if python3 "$ROOT_DIR/scripts/sync-workflow.py" "$stub_url" "$server_dir/failure-output" >/dev/null 2>"$failure_stderr"; then
		fail "closed backend URL fails"
	else
		pass "closed backend URL fails"
	fi
	if grep -Fq "$stub_url" "$failure_stderr"; then
		pass "closed backend error includes URL"
	else
		fail "closed backend error includes URL"
	fi
fi

if [ "$status" -eq 0 ]; then
	echo "All sync-workflow checks passed."
fi
exit "$status"
