---
created: 2026-08-24
status: draft
requirements:
  - REQ-LAUNCHER-SOURCE-DEPLOY-001
  - REQ-LAUNCHER-SOURCE-DEPLOY-002
  - REQ-LAUNCHER-SOURCE-DEPLOY-003
system_design:
  - ../../specs/launcher/system-design/source-deploy.md
legacy_specs: []
---

# Implementation Plan: Source-checkout user-service deploy

## Overview

Add `make deploy` so a source checkout can rebuild a production runtime, publish
it outside the tree, and reinstall the operator's user-domain daemon. Delivery
order is: fix the systemd unit working directory so embedded SPA assets cannot
be shadowed, then add a testable publish-and-install script, then wire the
Makefile entry, then update public operator docs.

`make service-install` stays the checkout-local installer. `make deploy` is the
isolation-preserving path.

## Scope

### In scope

- Linux user systemd (`kandev service install` without `--system`).
- Publishing `<checkout>/dist/kandev` to `<live-home>/runtime`.
- Embedded production SPA (ADR-0022). Preserving existing live home and listener
  unless the operator passes `HOME_DIR` or `PORT`.
- `make help` listing and public how-to for the new target.

### Out of scope

- System (`--system`) install, Docker, Kubernetes, remote hosts, Windows SCM.
- `kandev deploy` CLI, Homebrew/npm/Scoop, Desktop/Tauri, `make dev` changes.
- Serving live UI from a filesystem directory or setting `KANDEV_WEB_DIST_DIR`.

## Technical approach

### 1. Systemd working directory

`renderSystemdUnit` in `apps/backend/internal/launcher/service.go` currently
omits `WorkingDirectory`. Launchd already sets it to `input.HomeDir`. Add
`WorkingDirectory=<home>` so `webAssetsFS` cannot pick a relative
`apps/web/dist` over the embed. Keep omitting `KANDEV_WEB_DIST_DIR`. Extend
`TestRenderSystemdUnit*` in `service_test.go`.

### 2. Publish-and-install script

Add `scripts/deploy-user-service.sh` (name may match existing script style).
It owns live-home resolution, isolation checks, atomic publish, and the
user-domain install invocation:

1. Resolve live home: operator `HOME_DIR`, else `Environment=KANDEV_HOME_DIR`
   from a managed `~/.config/systemd/user/kandev.service` (or the launchd
   plist on Darwin), else `~/.kandev`.
2. Refuse a home that is the checkout, lives under the checkout, or contains
   a `.kandev-dev` segment.
3. Require a complete staging bundle (`bin/kandev`, `bin/agentctl`, the four
   remote helpers), matching `runtime-bundle` / `package-bundle.sh`.
4. Atomically replace `<live-home>/runtime/bin` using the same `mktemp` then
   `mv` pattern as the `runtime-bundle` target in the root `Makefile`.
5. Run `<live-home>/runtime/bin/kandev service install` with no `--system`.
   Pass `--home-dir` for the resolved live home. Pass `--port` only when the
   operator set `PORT` or the existing managed unit recorded a listener.
6. Print the published executable, live home, and user-domain mode.

Tests use a fake `HOME`, a temp unit file, and a fake staging `kandev` that
records argv. They must not touch the operator's real `~/.kandev`.

### 3. Makefile `deploy` target

Root `Makefile`:

- `deploy` depends on Go deps and `pnpm install` in `apps/` without
  `playwright install` (`make install` / `install-web` stay unchanged).
- Then `runtime-bundle` into `$(SERVICE_BUNDLE_DIR)` (`dist/kandev`).
- Then `scripts/deploy-user-service.sh` with `SERVICE_INSTALL_FLAGS`
  (`PORT`, `HOME_DIR`, `NO_BOOT_START`) already used by `service-install`.
- Never pass `--system`. Never export `KANDEV_WEB_DIST_DIR`.
- `make help` lists `deploy` under Service Commands.
- `make test-scripts` runs the new script tests.

Dry-run coverage follows `scripts/release/runtime-bundle.test.sh` and
`scripts/dev-prod-db-path.test.sh`: assert the recipe, do not build Vite.

### 4. Public docs

Update `docs/public/run-as-a-service.md` (source-checkout section around the
current `make service-install` block) and a short pointer in
`docs/public/contributing.md`. Contrast `make deploy` (stable runtime under
the live home) with `make service-install` (checkout `dist/kandev`).

## Tests

| AC | Evidence |
| --- | --- |
| `AC-LAUNCHER-SOURCE-DEPLOY-001.1` | `scripts/deploy-user-service.test.sh` plus Makefile dry-run: user-domain `service install`, no `--system` |
| `AC-LAUNCHER-SOURCE-DEPLOY-001.2` | Script records `kandev --headless` via install; recipe uses `runtime-bundle` / embed, not `dev` |
| `AC-LAUNCHER-SOURCE-DEPLOY-001.3` | Script test: existing unit home/port reused unless `HOME_DIR`/`PORT` set |
| `AC-LAUNCHER-SOURCE-DEPLOY-001.4` | Script test: missing unit installs with default `~/.kandev` under fake `HOME` |
| `AC-LAUNCHER-SOURCE-DEPLOY-001.5` | Dry-run and script argv never contain `--system` |
| `AC-LAUNCHER-SOURCE-DEPLOY-001.6` | `make help` contains `deploy` |
| `AC-LAUNCHER-SOURCE-DEPLOY-001.7` | Incomplete staging bundle exits before replacing `<home>/runtime/bin` |
| `AC-LAUNCHER-SOURCE-DEPLOY-002.1` | Published path is `<home>/runtime/bin/kandev`, not `<checkout>/dist/kandev` |
| `AC-LAUNCHER-SOURCE-DEPLOY-002.2` | Refuses checkout and `.kandev-dev` homes; install gets `--home-dir` of live home |
| `AC-LAUNCHER-SOURCE-DEPLOY-002.3` | No `Makefile` `dev` changes; documented as out of scope |
| `AC-LAUNCHER-SOURCE-DEPLOY-002.4` | Checkout under `/.kandev/tasks/` still publishes to the live home, not the worktree |
| `AC-LAUNCHER-SOURCE-DEPLOY-003.1` | `deploy` runs `build-web`, `sync-embedded-web`, `runtime-bundle` |
| `AC-LAUNCHER-SOURCE-DEPLOY-003.2` | Unit renderer and install argv omit `KANDEV_WEB_DIST_DIR`; systemd `WorkingDirectory` is the live home |
| `AC-LAUNCHER-SOURCE-DEPLOY-003.3` | Live `ExecStart` is the published binary, so a later checkout `pnpm build` cannot change it |

## E2E tests

No Playwright project. This is a Makefile/systemd operator path, not a UI
flow. End-to-end evidence is the isolated shell tests with a fake `HOME` and
the Makefile dry-run harness.

## Work orders

- [x] [Task 01: Set systemd working directory to the service home](task-01-systemd-working-directory.md)
- [x] [Task 02: Publish runtime and reinstall the user-domain service](task-02-publish-user-runtime.md)
- [x] [Task 03: Add the Makefile deploy target](task-03-makefile-deploy-target.md)
- [x] [Task 04: Document source-checkout deploy](task-04-public-docs.md)

```text
wave 1  task-01 systemd WD          task-02 publish script
            \                           /
wave 2       \                         /
              +---- task-03 Makefile deploy
                          |
wave 3                    +---- task-04 public docs
```

Task 01 and task 02 are parallel-safe (disjoint files). Task 03 depends on
both so the install path emits the new unit and the Makefile calls the
script. Task 04 depends on task 03 so the docs describe the shipped target.

## Verification results

- Task 01: `cd apps/backend && go test ./internal/launcher -count=1 -run 'TestRenderSystemdUnit'` pass.
- Task 02: `bash scripts/deploy-user-service.test.sh` pass.
- Task 03: `bash scripts/make-deploy.test.sh` pass.
- Task 04: `node --test scripts/validate-public-docs.test.mjs` pass; `node scripts/validate-public-docs.mjs` pass.

## Risks

- Tests that resolve `~/.kandev` against the real home can overwrite the
  operator's live daemon. Every script test must set `HOME` to a temp directory
  and point the unit path at that tree.
- `kandev service config` currently prints `resolveServiceHome`, not the
  installed unit's `KANDEV_HOME_DIR`. Deploy must parse the managed unit/plist
  or it will retarget a customized live home.
- Adding `WorkingDirectory` to every systemd unit, including `--system`, is a
  behavior change. It matches launchd and is required so relative
  `apps/web/dist` cannot shadow the embed.
- Reinstall uses `enable --now`, which may not restart an already-running
  unit (`docs/public/run-as-a-service.md`). The publish script should
  `service install` then `service restart` so the new binary is loaded.
- A full `make deploy` in this task worktree would publish into the host
  user's live home. Implementers must not run the real target against the
  developer instance during TDD; use the fake-`HOME` tests.
