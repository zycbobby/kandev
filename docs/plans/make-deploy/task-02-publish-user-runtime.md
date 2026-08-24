---
id: "02-publish-user-runtime"
title: "Publish runtime and reinstall the user-domain service"
status: done
wave: 1
depends_on: []
plan: "plan.md"
requirements:
  - REQ-LAUNCHER-SOURCE-DEPLOY-001
  - REQ-LAUNCHER-SOURCE-DEPLOY-002
acceptance_criteria:
  - AC-LAUNCHER-SOURCE-DEPLOY-001.1
  - AC-LAUNCHER-SOURCE-DEPLOY-001.3
  - AC-LAUNCHER-SOURCE-DEPLOY-001.4
  - AC-LAUNCHER-SOURCE-DEPLOY-001.5
  - AC-LAUNCHER-SOURCE-DEPLOY-001.7
  - AC-LAUNCHER-SOURCE-DEPLOY-002.1
  - AC-LAUNCHER-SOURCE-DEPLOY-002.2
  - AC-LAUNCHER-SOURCE-DEPLOY-002.4
system_design:
  - ../../specs/launcher/system-design/source-deploy.md
---

# Task 02: Publish runtime and reinstall the user-domain service

## Summary

Add a script that copies a staged runtime bundle to
`<live-home>/runtime` and runs that copy's `kandev service install` without
`--system`. Isolation and home/port preservation live here so the Makefile can
stay a thin orchestrator.

## In scope

- `scripts/deploy-user-service.sh` (final name may follow repo script style).
- Resolve live home from `HOME_DIR`, else the managed user unit/plist, else
  `$HOME/.kandev`.
- Refuse checkout paths and any home with a `.kandev-dev` segment.
- Atomically publish `bin/` into `<live-home>/runtime/bin`.
- Invoke `<live-home>/runtime/bin/kandev service install` with `--home-dir`,
  optional `--port` / `--no-boot-start`, then `service restart` so a running
  unit loads the new binary.
- Isolated tests with fake `HOME` and a fake staging `kandev` that records argv.

## Out of scope

- Root Makefile `deploy` target and `make help`.
- Playwright, Vite, or `runtime-bundle` production builds.
- System-service (`--system`) install.

## Acceptance

- A complete staging bundle is published to `<live-home>/runtime/bin/kandev`,
  then that binary is invoked as `service install` without `--system`.
- An existing managed user unit's home and listener are reused unless `HOME_DIR`
  or `PORT` is set. A missing unit uses `$HOME/.kandev`.
- Incomplete bundles, checkout homes, and `.kandev-dev` homes exit non-zero
  without replacing a previous `<live-home>/runtime/bin`.

## Verification

```bash
bash scripts/deploy-user-service.test.sh
```

## Files likely touched

- `scripts/deploy-user-service.sh`
- `scripts/deploy-user-service.test.sh`

## Dependencies

None.

## Risks

- Resolving home from the real `~/.kandev` would mutate the operator's live
  daemon. Tests must export a temp `HOME` and write the fake unit under that
  tree.
- `kandev service config` does not read the installed unit. Parse
  `Environment=KANDEV_HOME_DIR=` (systemd) or the launchd plist equivalent.
- `enable --now` on an active unit may not restart it. Follow install with
  `service restart`.

## Parallelism

`parallel-safe`

## Inputs

- System design: Stable runtime location, Control flow, Failure and recovery.
- Atomic replace pattern: root `Makefile` `runtime-bundle` (`mktemp` then `mv`).
- Required bundle members: `scripts/release/package-bundle.sh` /
  `scripts/release/runtime-bundle.test.sh`.
- Unit path: `linuxUnitPath(false)` → `~/.config/systemd/user/kandev.service`.

## Results

- RED: `bash scripts/deploy-user-service.test.sh` failed because `scripts/deploy-user-service.sh` did not exist.
- GREEN: script publishes to `<live-home>/runtime`, reuses managed unit home/port, refuses checkout and `.kandev-dev`, and never passes `--system`.
- `bash scripts/deploy-user-service.test.sh` — pass.
