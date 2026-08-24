---
id: "03-makefile-deploy-target"
title: "Add the Makefile deploy target"
status: done
wave: 2
depends_on:
  - "01-systemd-working-directory"
  - "02-publish-user-runtime"
plan: "plan.md"
requirements:
  - REQ-LAUNCHER-SOURCE-DEPLOY-001
  - REQ-LAUNCHER-SOURCE-DEPLOY-003
acceptance_criteria:
  - AC-LAUNCHER-SOURCE-DEPLOY-001.2
  - AC-LAUNCHER-SOURCE-DEPLOY-001.6
  - AC-LAUNCHER-SOURCE-DEPLOY-003.1
  - AC-LAUNCHER-SOURCE-DEPLOY-003.3
system_design:
  - ../../specs/launcher/system-design/source-deploy.md
---

# Task 03: Add the Makefile deploy target

## Summary

Wire `make deploy` as the operator command: production web embed, runtime
bundle in checkout `dist/kandev`, then the publish script. Help lists it.
`make service-install` is unchanged.

## In scope

- Root `Makefile` `deploy` target and `help` text.
- Production dependency install without Playwright (`install-web` stays as-is).
- Reuse `build-web`, `sync-embedded-web`, and `runtime-bundle`.
- Forward `PORT`, `HOME_DIR`, and `NO_BOOT_START` through
  `SERVICE_INSTALL_FLAGS`.
- Makefile dry-run tests and hook them into `make test-scripts`.

## Out of scope

- Changing `make service-install` so its ExecStart leaves the checkout.
- Public website docs (task 04).
- Running a real Vite/Go production build in CI for this target.

## Acceptance

- `make help` lists `deploy` as the source-checkout command that updates the
  user-domain daemon.
- The `deploy` recipe builds the embedded production SPA (`build-web`,
  `sync-embedded-web`, `runtime-bundle`), does not install Playwright, does
  not pass `--system`, and does not set `KANDEV_WEB_DIST_DIR`.
- After the bundle exists, the recipe calls the task-02 publish script so
  `ExecStart` is `<live-home>/runtime/bin/kandev`, not
  `<checkout>/dist/kandev`.

## Verification

```bash
bash scripts/make-deploy.test.sh
```

The harness should assert `make help` and a dry-run of `deploy` (follow
`scripts/dev-prod-db-path.test.sh` / `scripts/release/runtime-bundle.test.sh`
so nested `$(MAKE)` does not start a real build). Also run
`bash scripts/deploy-user-service.test.sh` to keep the publish contract green.

## Files likely touched

- `Makefile` (`help`, `deploy`, `test-scripts`)
- `scripts/make-deploy.test.sh`

## Dependencies

Task 01 (unit working directory lands in the installed binary) and task 02
(publish script exists).

## Risks

- `make -n deploy` still executes lines that call `$(MAKE)`. The dry-run
  harness must stub `MAKE` or inspect only the deploy recipe.
- `service-bundle` depends on `install`, which installs Playwright. `deploy`
  must not use that target as-is.
- Do not run a real `make deploy` against the developer `~/.kandev` from this
  task worktree.

## Parallelism

`sequential`

## Inputs

- System design: Components, Control flow, Public operator contract.
- Existing `SERVICE_*` variables and `service-install` recipe in the root
  `Makefile`.
- `runtime-bundle` already runs `build-web` and `sync-embedded-web`.

## Results

- RED: `bash scripts/make-deploy.test.sh` failed; `make help` and `make -n deploy` had no deploy target.
- GREEN: root `deploy` builds via `runtime-bundle`, installs pnpm deps without Playwright, and calls `scripts/deploy-user-service.sh` without `--system`.
- `bash scripts/make-deploy.test.sh` — pass.
- `bash scripts/deploy-user-service.test.sh` — pass.
