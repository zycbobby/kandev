#!/usr/bin/env bash
# Publish a staged runtime bundle to the live user-domain Kandev home and
# reinstall that daemon. Never pass --system.
set -euo pipefail

usage() {
	echo "usage: $0 --bundle-dir DIR [--checkout DIR] [--home-dir DIR] [--port N] [--no-boot-start] [--force]" >&2
}

BUNDLE=""
CHECKOUT=""
HOME_DIR_FLAG=""
PORT_FLAG=""
NO_BOOT_START=0
FORCE=0

while [ "$#" -gt 0 ]; do
	case "$1" in
	--bundle-dir)
		BUNDLE="${2:-}"
		shift 2
		;;
	--checkout)
		CHECKOUT="${2:-}"
		shift 2
		;;
	--home-dir)
		HOME_DIR_FLAG="${2:-}"
		shift 2
		;;
	--port)
		PORT_FLAG="${2:-}"
		shift 2
		;;
	--no-boot-start)
		NO_BOOT_START=1
		shift
		;;
	--force)
		FORCE=1
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		usage
		exit 2
		;;
	esac
done

if [ -z "$BUNDLE" ]; then
	usage
	exit 2
fi

if [ -z "$CHECKOUT" ]; then
	CHECKOUT="$(pwd -P)"
fi

REMOTE_AGENTCTL_HELPERS=(
	agentctl-linux-amd64
	agentctl-linux-arm64
	agentctl-darwin-arm64
	agentctl-darwin-amd64
)

path_has_segment() {
	local needle="$1" candidate="$2"
	python3 -c 'import os, sys; raise SystemExit(0 if sys.argv[1] in os.path.realpath(sys.argv[2]).split(os.sep) else 1)' \
		"$needle" "$candidate"
}

is_under() {
	local parent="$1" child="$2"
	python3 -c 'import os, sys
parent=os.path.realpath(sys.argv[1])
child=os.path.realpath(sys.argv[2])
raise SystemExit(0 if child==parent or child.startswith(parent+os.sep) else 1)' \
		"$parent" "$child"
}

user_unit_path() {
	local config_home="${XDG_CONFIG_HOME:-$HOME/.config}"
	printf '%s\n' "$config_home/systemd/user/kandev.service"
}

user_plist_path() {
	printf '%s\n' "$HOME/Library/LaunchAgents/com.kdlbs.kandev.plist"
}

read_systemd_env() {
	local key="$1" file="$2"
	[ -f "$file" ] || return 0
	python3 -c '
import re, sys
key, path = sys.argv[1], sys.argv[2]
text = open(path, encoding="utf-8").read()
pattern = re.compile(r"^Environment=(?P<q>\"?)%s=(?P<value>.*?)(?P=q)\s*$" % re.escape(key), re.M)
matches = pattern.findall(text)
if matches:
    print(matches[-1][1])
' "$key" "$file"
}

read_plist_string() {
	local key="$1" file="$2"
	[ -f "$file" ] || return 0
	python3 -c '
import re, sys
key, path = sys.argv[1], sys.argv[2]
text = open(path, encoding="utf-8").read()
match = re.search(r"<key>%s</key>\s*<string>(.*?)</string>" % re.escape(key), text, re.S)
if match:
    print(match.group(1))
' "$key" "$file"
}

managed_file() {
	local file="$1"
	[ -f "$file" ] || return 1
	grep -Fq "managed by kandev" "$file"
}

require_complete_bundle() {
	local bundle="$1"
	local launcher="kandev"
	if [ ! -f "$bundle/bin/$launcher" ] && [ -f "$bundle/bin/kandev.exe" ]; then
		launcher="kandev.exe"
	fi
	if [ ! -f "$bundle/bin/$launcher" ]; then
		echo "Missing native launcher in $bundle/bin; build cmd/kandev first" >&2
		exit 1
	fi
	if [ ! -x "$bundle/bin/$launcher" ]; then
		echo "Runtime binary $launcher is not executable in $bundle/bin" >&2
		exit 1
	fi
	if [ ! -f "$bundle/bin/agentctl" ] && [ ! -f "$bundle/bin/agentctl.exe" ]; then
		echo "Missing agentctl in $bundle/bin; build cmd/agentctl first" >&2
		exit 1
	fi
	local helper
	for helper in "${REMOTE_AGENTCTL_HELPERS[@]}"; do
		if [ ! -f "$bundle/bin/$helper" ]; then
			echo "Missing remote agentctl helper $helper in $bundle/bin" >&2
			exit 1
		fi
	done
}

resolve_live_home() {
	if [ -n "$HOME_DIR_FLAG" ]; then
		printf '%s\n' "$HOME_DIR_FLAG"
		return
	fi
	local unit plist recorded=""
	unit="$(user_unit_path)"
	plist="$(user_plist_path)"
	if managed_file "$unit"; then
		recorded="$(read_systemd_env KANDEV_HOME_DIR "$unit")"
	elif managed_file "$plist"; then
		recorded="$(read_plist_string KANDEV_HOME_DIR "$plist")"
	fi
	if [ -n "$recorded" ]; then
		printf '%s\n' "$recorded"
		return
	fi
	printf '%s\n' "${HOME:?}/.kandev"
}

resolve_live_port() {
	if [ -n "$PORT_FLAG" ]; then
		printf '%s\n' "$PORT_FLAG"
		return
	fi
	local unit plist recorded=""
	unit="$(user_unit_path)"
	plist="$(user_plist_path)"
	if managed_file "$unit"; then
		recorded="$(read_systemd_env KANDEV_SERVER_PORT "$unit")"
		if [ -z "$recorded" ]; then
			recorded="$(read_systemd_env KANDEV_BACKEND_PORT "$unit")"
		fi
	elif managed_file "$plist"; then
		recorded="$(read_plist_string KANDEV_SERVER_PORT "$plist")"
		if [ -z "$recorded" ]; then
			recorded="$(read_plist_string KANDEV_BACKEND_PORT "$plist")"
		fi
	fi
	printf '%s\n' "$recorded"
}

resolve_live_db_path() {
	local live_home="$1"
	local unit plist recorded=""
	unit="$(user_unit_path)"
	plist="$(user_plist_path)"
	if managed_file "$unit"; then
		recorded="$(read_systemd_env KANDEV_DATABASE_PATH "$unit")"
	elif managed_file "$plist"; then
		recorded="$(read_plist_string KANDEV_DATABASE_PATH "$plist")"
	fi
	if [ -n "$recorded" ]; then
		case "$recorded" in
		/*) printf '%s\n' "$recorded" ;;
		*) printf '%s\n' "$live_home/$recorded" ;;
		esac
		return
	fi
	printf '%s\n' "$live_home/data/kandev.db"
}

refuse_isolated_home() {
	local live_home="$1" checkout="$2"
	if path_has_segment ".kandev-dev" "$live_home"; then
		echo "refusing live home $live_home: development home (.kandev-dev) cannot be the deployed daemon" >&2
		exit 1
	fi
	if is_under "$checkout" "$live_home"; then
		echo "refusing live home $live_home: path is the source checkout or inside it" >&2
		exit 1
	fi
}

service_is_active() {
	if [ "$(uname -s 2>/dev/null || true)" = "Darwin" ]; then
		command -v launchctl >/dev/null 2>&1 || return 1
		launchctl print "gui/$(id -u)/com.kdlbs.kandev" >/dev/null 2>&1
		return
	fi
	command -v systemctl >/dev/null 2>&1 || return 1
	systemctl --user is-active --quiet kandev.service >/dev/null 2>&1
}

check_deploy_idle() {
	local db_path="$1"
	if [ ! -e "$db_path" ]; then
		if service_is_active; then
			echo "refusing deploy: the user service is active but its database $db_path is missing" >&2
			echo "Pass --force to deploy anyway." >&2
			exit 1
		fi
		return
	fi
	local report="" rc=0
	report="$(python3 - "$db_path" <<'PY'
import os
import sqlite3
import sys
import urllib.parse

path = sys.argv[1]
required = ("tasks", "task_sessions", "agent_delivery_submissions", "session_recovery_blocks")
uri = "file:" + urllib.parse.quote(os.path.abspath(path), safe="/") + "?mode=ro"
try:
    conn = sqlite3.connect(uri, uri=True, timeout=5)
    try:
        conn.execute("PRAGMA query_only = ON")
        present = {row[0] for row in conn.execute("SELECT name FROM sqlite_master WHERE type = 'table'")}
        missing = [name for name in required if name not in present]
        if missing:
            raise sqlite3.DatabaseError("missing tables: " + ", ".join(missing))
        rows = conn.execute(
            """
            SELECT t.id, t.title, s.state, 'session'
            FROM task_sessions s
            JOIN tasks t ON t.id = s.task_id
            WHERE s.state IN ('STARTING', 'RUNNING') AND t.archived_at IS NULL
            UNION ALL
            SELECT t.id, t.title, d.state, 'submission'
            FROM agent_delivery_submissions d
            JOIN task_sessions s ON s.id = d.session_id
            JOIN tasks t ON t.id = s.task_id
            WHERE d.state IN ('prepared', 'accepted', 'dispatching', 'interrupted_unknown')
              AND t.archived_at IS NULL
            UNION ALL
            SELECT t.id, t.title, b.state, 'recovery block'
            FROM session_recovery_blocks b
            JOIN task_sessions s ON s.id = b.session_id
            JOIN tasks t ON t.id = s.task_id
            WHERE b.state = 'open' AND t.archived_at IS NULL
            ORDER BY 1
            """
        ).fetchall()
    finally:
        conn.close()
except Exception as exc:
    print(str(exc), file=sys.stderr)
    raise SystemExit(2)
if rows:
    for task_id, title, state, kind in rows:
        print('task %s "%s": %s %s' % (task_id, title, state, kind))
    raise SystemExit(3)
PY
)" || rc=$?
	if [ "$rc" -eq 0 ] && [ -z "$report" ]; then
		return
	fi
	if [ "$rc" -eq 3 ] && [ -n "$report" ]; then
		{
			printf 'refusing deploy: active work found in %s\n' "$db_path"
			printf '%s\n' "$report"
			printf 'Wait for it to finish, run `kandev service stop` first, or pass --force to deploy anyway.\n'
		} >&2
		exit 1
	fi
	echo "refusing deploy: cannot inspect $db_path; pass --force to deploy anyway" >&2
	exit 1
}

publish_bundle() {
	local bundle="$1" live_home="$2"
	local runtime_dir staging
	runtime_dir="$live_home/runtime"
	mkdir -p "$runtime_dir"
	staging="$(mktemp -d "$runtime_dir/.runtime-bundle.XXXXXX")"
	mkdir -p "$staging/bin"
	cp -a "$bundle/bin/." "$staging/bin/"
	rm -rf "$runtime_dir/bin"
	mv "$staging/bin" "$runtime_dir/bin"
	rm -rf "$staging"
}

require_complete_bundle "$BUNDLE"

LIVE_HOME="$(resolve_live_home)"
LIVE_PORT="$(resolve_live_port)"
refuse_isolated_home "$LIVE_HOME" "$CHECKOUT"

if [ "$FORCE" -eq 1 ]; then
	echo "deploy idle gate skipped (--force)"
else
	check_deploy_idle "$(resolve_live_db_path "$LIVE_HOME")"
fi

publish_bundle "$BUNDLE" "$LIVE_HOME"

LAUNCHER="$LIVE_HOME/runtime/bin/kandev"
if [ ! -x "$LAUNCHER" ] && [ -x "$LIVE_HOME/runtime/bin/kandev.exe" ]; then
	LAUNCHER="$LIVE_HOME/runtime/bin/kandev.exe"
fi

install_args=(service install --home-dir "$LIVE_HOME")
if [ -n "$LIVE_PORT" ]; then
	install_args+=(--port "$LIVE_PORT")
fi
if [ "$NO_BOOT_START" -eq 1 ]; then
	install_args+=(--no-boot-start)
fi

"$LAUNCHER" "${install_args[@]}"
"$LAUNCHER" service restart

printf 'published: %s\n' "$LAUNCHER"
printf 'home: %s\n' "$LIVE_HOME"
printf 'mode: user-domain\n'
