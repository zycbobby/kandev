#!/usr/bin/env bash
# kandev-kill.test.sh — exercise the guard rails and the kill path of
# kandev-kill against a stand-in backend: a shell script named `kandev` (so the
# "is this really a kandev backend" check passes) plus a stubbed `ss` reporting
# it as listening. Nothing real is signalled except the stand-in.
#
# The script runs through /bin/bash on purpose: that is bash 3.2 on macOS, the
# version that must keep working (no mapfile).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/scripts/kandev-kill"
SH="${TEST_BASH:-/bin/bash}"

status=0
pass() { printf 'ok    %s\n' "$1"; }
fail() { printf 'FAIL  %s\n' "$1" >&2; status=1; }

TMP="$(mktemp -d)"
TMP="$(cd "$TMP" && pwd -P)"
FAKE_PID=""
cleanup() {
	if [ -n "$FAKE_PID" ]; then kill "$FAKE_PID" 2>/dev/null || true; fi
	rm -rf "$TMP"
}
trap cleanup EXIT

BACKEND_PORT=49161
GUARDED_PORT=38429

mkdir -p "$TMP/bin" "$TMP/stubs"
cat >"$TMP/bin/kandev" <<'EOF'
#!/bin/sh
# Stay alive as itself: an `exec sleep` would replace the process image, and
# kandev-kill identifies the backend by its executable/cmdline.
trap 'exit 0' TERM INT
while :; do sleep 1; done
EOF
chmod +x "$TMP/bin/kandev"

start_fake() {
	"$TMP/bin/kandev" >/dev/null 2>&1 &
	FAKE_PID=$!
	# Detach from the job table: the kill assertion below expects the target to
	# die by signal, and a tracked job would print a "Terminated" notice.
	disown "$FAKE_PID" 2>/dev/null || true
}

# Stub `ss`: the filtered query answers for the requested port, the unfiltered
# one for the instance's own port. Both report the stand-in's PID.
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

run_kill() {
	PATH="$TMP/stubs:$PATH" "$SH" "$SCRIPT" "$@"
}

# --- Refuses to run with neither a port nor a pidfile ---
if run_kill >/dev/null 2>&1; then
	fail "no target exits non-zero"
else
	pass "no target exits non-zero"
fi

# --- A guarded production port is refused without --force ---
start_fake
if run_kill "$GUARDED_PORT" --yes >/dev/null 2>&1; then
	fail "guarded port $GUARDED_PORT is refused"
else
	pass "guarded port $GUARDED_PORT is refused"
fi
if kill -0 "$FAKE_PID" 2>/dev/null; then
	pass "the guarded-port refusal left the process running"
else
	fail "the guarded-port refusal left the process running"
fi
kill "$FAKE_PID" 2>/dev/null || true
wait "$FAKE_PID" 2>/dev/null || true
FAKE_PID=""

# --- A port running nothing kandev-like is refused ---
cat >"$TMP/bin/notkandev" <<'EOF'
#!/bin/sh
exec sleep 300
EOF
chmod +x "$TMP/bin/notkandev"
"$TMP/bin/notkandev" &
DECOY_PID=$!
cat >"$TMP/stubs/ss" <<EOF
#!/bin/sh
kill -0 $DECOY_PID 2>/dev/null || exit 0
printf 'LISTEN 0 4096 127.0.0.1:$BACKEND_PORT 0.0.0.0:* users:(("notkandev",pid=$DECOY_PID,fd=6))\n'
EOF
if run_kill "$BACKEND_PORT" --yes >/dev/null 2>&1; then
	fail "a non-kandev listener is refused"
else
	pass "a non-kandev listener is refused"
fi
if kill -0 "$DECOY_PID" 2>/dev/null; then
	pass "the non-kandev refusal left the process running"
else
	fail "the non-kandev refusal left the process running"
fi
kill "$DECOY_PID" 2>/dev/null || true
wait "$DECOY_PID" 2>/dev/null || true

# --- A real target on an unguarded port is terminated ---
start_fake
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
killed_pid="$FAKE_PID"
if run_kill "$BACKEND_PORT" --yes >/dev/null 2>&1; then
	pass "an unguarded kandev backend is terminated (exit 0)"
else
	fail "an unguarded kandev backend is terminated (exit 0)"
fi
if kill -0 "$killed_pid" 2>/dev/null; then
	fail "the target is gone after the kill"
else
	pass "the target is gone after the kill"
fi
FAKE_PID=""

exit "$status"
