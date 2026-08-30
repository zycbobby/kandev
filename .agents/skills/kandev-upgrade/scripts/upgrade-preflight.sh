#!/usr/bin/env bash
# Pre-flight for a Kandev self-upgrade: record the current running version and
# checkout ref, take a manual SQLite backup via the API, then prune the backups
# directory to the two most recent snapshots.
#
# Read-only except for the backup + prune the caller explicitly asked for.
# Never pass --system; this targets the user-domain service only.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd -P)"

# --- resolve live home dir (never trust ~/.kandev blindly: it may be a stale backup) ---
KH="${KANDEV_HOME_DIR:-}"
if [ -z "$KH" ]; then
  KH="$(systemctl --user show kandev.service -p Environment 2>/dev/null \
    | tr ' ' '\n' | sed -n 's/^KANDEV_HOME_DIR=//p')"
fi
KH="${KH:-$HOME/.kandev}"

# --- resolve backend port ---
PORT="${KANDEV_SERVER_PORT:-}"
if [ -z "$PORT" ]; then
  PORT="$("$REPO_ROOT/scripts/kandev-instances" 2>/dev/null | awk 'NR==2{print $2}')"
fi
PORT="${PORT:-38429}"

BASE="http://127.0.0.1:${PORT}"
BACKUPS_DIR="${KH}/data/backups"

echo "== pre-flight =="
echo "home_dir: ${KH}"
echo "backend : ${BASE}"

# 1. Record current running version and checkout ref/tag.
echo
echo "== current running version =="
curl -fsS -m 10 "${BASE}/health" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("version", "?"))' || true

echo "== current checkout (git describe / tag) =="
git -C "${REPO_ROOT}" describe --tags --always --dirty 2>/dev/null || true
git -C "${REPO_ROOT}" tag --points-at HEAD 2>/dev/null || true

# 2. Take a manual backup through the API (async job; poll until a new snapshot lands).
echo
echo "== taking manual backup =="
BEFORE="$(curl -fsS -m 10 "${BASE}/api/v1/system/backups" \
  | python3 -c 'import json,sys; print(" ".join(sorted(s["name"] for s in json.load(sys.stdin)["snapshots"])))')"

JOB="$(curl -fsS -m 10 -X POST "${BASE}/api/v1/system/backups" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin).get("job_id",""))')"
echo "backup job: ${JOB:-unknown}"

# Wait up to ~60s for a new snapshot to appear.
for _ in $(seq 1 20); do
  AFTER="$(curl -fsS -m 10 "${BASE}/api/v1/system/backups" \
    | python3 -c 'import json,sys; print(" ".join(sorted(s["name"] for s in json.load(sys.stdin)["snapshots"])))')"
  NEW="$(comm -13 <(printf '%s\n' "${BEFORE}" | tr ' ' '\n' | sort) \
                 <(printf '%s\n' "${AFTER}" | tr ' ' '\n' | sort) || true)"
  if [ -n "$NEW" ]; then
    echo "new snapshot: $(echo "$NEW" | tr '\n' ' ')"
    break
  fi
  sleep 3
done

# 3. Prune the backups directory to the two most recent snapshots (by mtime),
#    matching the backend's own retention convention.
echo
echo "== pruning backups to newest two =="
curl -fsS -m 10 "${BASE}/api/v1/system/backups" | python3 -c '
import json, sys
snaps = json.load(sys.stdin)["snapshots"]
snaps.sort(key=lambda s: s["mtime"], reverse=True)
for s in snaps[2:]:
    print(s["name"])
' | while read -r name; do
  [ -z "$name" ] && continue
  curl -fsS -m 30 -X DELETE "${BASE}/api/v1/system/backups/${name}" >/dev/null \
    && echo "deleted: ${name}"
done

echo
echo "== remaining backups =="
ls -1 "${BACKUPS_DIR}" 2>/dev/null | sort || true
echo
echo "pre-flight complete"
