#!/usr/bin/env bash
# kandev-instances.test.sh — drive the instance lister against a real process
# standing in for a kandev backend, with `ss` stubbed so the test needs no
# listening socket. The process facts (environment, executable, cwd) come from
# the real OS, which is what exercises the /proc-on-Linux vs ps/lsof-on-macOS
# split this script has to get right.
#
# The script runs through /bin/bash on purpose: that is bash 3.2 on macOS, the
# version that must keep working (no declare -A, no mapfile).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/scripts/kandev-instances"
SH="${TEST_BASH:-/bin/bash}"

status=0
pass() { printf 'ok    %s\n' "$1"; }
fail() { printf 'FAIL  %s\n' "$1" >&2; status=1; }

TMP="$(mktemp -d)"
# Physical path: macOS resolves /var → /private/var, and the cwd the script
# reports comes from the kernel, so the expectation must use the same form.
TMP="$(cd "$TMP" && pwd -P)"
FAKE_PID=""
cleanup() {
	if [ -n "$FAKE_PID" ]; then kill "$FAKE_PID" 2>/dev/null || true; fi
	rm -rf "$TMP"
}
trap cleanup EXIT

BACKEND_PORT=49151
AGENTCTL_PORT=49152

# The stand-in backend: a real process whose cwd is a checkout path, carrying the
# environment a running backend would have. Only the listening socket is faked.
mkdir -p "$TMP/repo/apps/backend/bin" "$TMP/stubs"
(
	cd "$TMP/repo/apps/backend/bin" || exit 1
	KANDEV_HOME_DIR="$TMP/home" \
		KANDEV_SERVICE_MANAGER=systemd \
		KANDEV_AGENT_STANDALONE_PORT="$AGENTCTL_PORT" \
		exec /bin/sleep 300
) &
FAKE_PID=$!

# Stub `ss`: a filtered query answers for the requested port, the unfiltered one
# for the instance's own port. Both report the stand-in's PID, and only while it
# is still running, so a dead instance disappears from the listing on its own.
cat >"$TMP/stubs/ss" <<EOF
#!/bin/sh
kill -0 $FAKE_PID 2>/dev/null || exit 0
port=$BACKEND_PORT
for arg in "\$@"; do
	case "\$arg" in
	sport*:*) port="\${arg##*:}" ;;
	esac
done
printf 'LISTEN 0 4096 127.0.0.1:%s 0.0.0.0:* users:(("kandev",pid=$FAKE_PID,fd=6))\n' "\$port"
EOF
chmod +x "$TMP/stubs/ss"

run_list() {
	PATH="$TMP/stubs:$PATH" "$SH" "$SCRIPT" "$@"
}

# --- The row for the stand-in carries every resolved field ---
raw="$(run_list --raw)"
row="$(printf '%s\n' "$raw" | awk -F'\t' -v p="$FAKE_PID" '$1==p')"
if [ -n "$row" ]; then
	pass "--raw lists the running instance (pid $FAKE_PID)"
else
	fail "--raw lists the running instance (pid $FAKE_PID): got '$raw'"
fi

check_field() {
	local index="$1" want="$2" label="$3" got
	got="$(printf '%s\n' "$row" | awk -F'\t' -v i="$index" '{print $i}')"
	if [ "$got" = "$want" ]; then
		pass "$label = $want"
	else
		fail "$label = $want (got '$got')"
	fi
}
check_field 2 "$BACKEND_PORT" "backend port"
check_field 3 "$BACKEND_PORT" "web port falls back to the backend port"
check_field 6 "$TMP/repo" "REPO_PATH from the process cwd"

# --- The process environment supplies HOME_DIR, MANAGER and the agentctl port ---
# macOS only exposes a process's environment when the process is not a freshly
# spawned child of the calling shell (launchd-spawned backends do report it, a
# child of the test harness does not). Skip those fields rather than assert a
# platform quirk; Linux reads them from /proc and always runs them.
if ps eww -p "$FAKE_PID" 2>/dev/null | tr ' ' '\n' | grep -q '^KANDEV_HOME_DIR='; then
	check_field 4 "$AGENTCTL_PORT" "agentctl port from KANDEV_AGENT_STANDALONE_PORT"
	check_field 5 "$TMP/home" "HOME_DIR from KANDEV_HOME_DIR"
	check_field 7 "systemd" "MANAGER from KANDEV_SERVICE_MANAGER"

	# --- --paths derives the data paths from the home dir ---
	paths="$(run_list --paths)"
	for needle in "db:   $TMP/home/data/kandev.db" "log:  $TMP/home/logs/backend-logs.log"; do
		if printf '%s\n' "$paths" | grep -Fq -- "$needle"; then
			pass "--paths prints '$needle'"
		else
			fail "--paths prints '$needle' (got: $paths)"
		fi
	done

	# --- --raw --paths appends the same paths as columns ---
	raw_paths_row="$(run_list --raw --paths | awk -F'\t' -v p="$FAKE_PID" '$1==p')"
	if [ "$(printf '%s' "$raw_paths_row" | awk -F'\t' '{print $8}')" = "$TMP/home/data/kandev.db" ] &&
		[ "$(printf '%s' "$raw_paths_row" | awk -F'\t' '{print $9}')" = "$TMP/home/logs/backend-logs.log" ]; then
		pass "--raw --paths appends db and log columns"
	else
		fail "--raw --paths appends db and log columns (got '$raw_paths_row')"
	fi
else
	printf 'skip  environment of a spawned child is not readable on this OS\n'
fi

# --- --raw stays machine-readable: no table header ---
if printf '%s\n' "$raw" | grep -q '^PID'; then
	fail "--raw omits the table header"
else
	pass "--raw omits the table header"
fi

# --- An instance that is gone disappears from the listing ---
kill "$FAKE_PID" 2>/dev/null || true
wait "$FAKE_PID" 2>/dev/null || true
dead_pid="$FAKE_PID"
FAKE_PID=""
after="$(run_list --raw)"
if printf '%s\n' "$after" | awk -F'\t' -v p="$dead_pid" '$1==p' | grep -q .; then
	fail "a dead instance is not listed"
else
	pass "a dead instance is not listed"
fi

# --- --help documents the modes ---
help_out="$(run_list --help)"
for flag in --raw --paths; do
	if printf '%s\n' "$help_out" | grep -Fq -- "$flag"; then
		pass "--help documents $flag"
	else
		fail "--help documents $flag"
	fi
done

# --- Unknown flags fail loudly instead of listing anyway ---
if run_list --nope >/dev/null 2>&1; then
	fail "an unknown flag exits non-zero"
else
	pass "an unknown flag exits non-zero"
fi

exit "$status"
