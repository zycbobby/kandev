#!/usr/bin/env bash
# make-deploy.test.sh — dry-run the root deploy target. Nested $(MAKE) is
# stubbed so this does not build Vite or rewrite a live unit.
# @covers AC-LAUNCHER-SOURCE-DEPLOY-001.2
# @covers AC-LAUNCHER-SOURCE-DEPLOY-001.6
# @covers AC-LAUNCHER-SOURCE-DEPLOY-003.1
# @covers AC-LAUNCHER-SOURCE-DEPLOY-003.3
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
if printf '%s\n' "$help_out" | grep -Eq '^[[:space:]]+deploy[[:space:]]'; then
	pass "make help lists deploy"
else
	fail "make help lists deploy"
fi
if printf '%s\n' "$help_out" | grep -F 'deploy' | grep -Eqi 'user-domain|user service|user systemd'; then
	pass "make help describes deploy as user-domain"
else
	fail "make help describes deploy as user-domain"
fi

# $(MAKE) lines still execute under -n. Replace nested make with a no-op so
# install-backend / runtime-bundle do not start a real build.
dry_out=""
if ! dry_out="$(
	make -C "$ROOT_DIR" --no-print-directory -n deploy \
		MAKE=':' \
		HOME_DIR='/tmp/kandev-deploy-home' \
		PORT=40131 \
		NO_BOOT_START=1 \
		2>/dev/null
)"; then
	fail "make -n deploy is available"
	dry_out=""
fi

if printf '%s\n' "$dry_out" | grep -Fq 'runtime-bundle'; then
	pass "deploy invokes runtime-bundle"
else
	fail "deploy invokes runtime-bundle"
fi
if printf '%s\n' "$dry_out" | grep -Fq 'scripts/deploy-user-service.sh'; then
	pass "deploy calls deploy-user-service.sh"
else
	fail "deploy calls deploy-user-service.sh"
fi
if printf '%s\n' "$dry_out" | grep -Fq -- "--checkout $ROOT_DIR" || printf '%s\n' "$dry_out" | grep -Fq -- "--checkout \"$ROOT_DIR\""; then
	pass "deploy passes the source checkout"
else
	fail "deploy passes the source checkout: $dry_out"
fi
if printf '%s\n' "$dry_out" | grep -Fq -- '--home-dir "/tmp/kandev-deploy-home"' || printf '%s\n' "$dry_out" | grep -Fq -- '--home-dir /tmp/kandev-deploy-home'; then
	pass "deploy forwards HOME_DIR"
else
	fail "deploy forwards HOME_DIR"
fi
if printf '%s\n' "$dry_out" | grep -Eq -- '--port(=| )40131'; then
	pass "deploy forwards PORT"
else
	fail "deploy forwards PORT"
fi
if printf '%s\n' "$dry_out" | grep -Fq -- '--no-boot-start'; then
	pass "deploy forwards NO_BOOT_START"
else
	fail "deploy forwards NO_BOOT_START"
fi
if ! printf '%s\n' "$dry_out" | grep -Fq -- '--system'; then
	pass "deploy does not pass --system"
else
	fail "deploy does not pass --system"
fi
if ! printf '%s\n' "$dry_out" | grep -Fq 'playwright'; then
	pass "deploy does not install Playwright"
else
	fail "deploy does not install Playwright"
fi
if ! printf '%s\n' "$dry_out" | grep -Fq 'KANDEV_WEB_DIST_DIR'; then
	pass "deploy does not set KANDEV_WEB_DIST_DIR"
else
	fail "deploy does not set KANDEV_WEB_DIST_DIR"
fi
if printf '%s\n' "$dry_out" | grep -Eq 'pnpm install'; then
	pass "deploy installs pnpm workspace deps"
else
	fail "deploy installs pnpm workspace deps"
fi

if [ "$status" -eq 0 ]; then
	echo "All make-deploy checks passed."
fi
exit "$status"
