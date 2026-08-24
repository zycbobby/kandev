---
id: "01-systemd-working-directory"
title: "Set systemd working directory to the service home"
status: done
wave: 1
depends_on: []
plan: "plan.md"
requirements:
  - REQ-LAUNCHER-SOURCE-DEPLOY-003
acceptance_criteria:
  - AC-LAUNCHER-SOURCE-DEPLOY-003.2
system_design:
  - ../../specs/launcher/system-design/source-deploy.md
---

# Task 01: Set systemd working directory to the service home

## Summary

Make the Linux user and system unit run with working directory equal to the
Kandev home, matching launchd. That keeps `webAssetsFS` from serving a relative
`apps/web/dist` instead of the embedded SPA.

## In scope

- Add `WorkingDirectory=` to `renderSystemdUnit` using `input.HomeDir`.
- Assert the rendered unit sets that directory and still omits
  `KANDEV_WEB_DIST_DIR`.

## Out of scope

- Makefile `deploy`, publish path, and public docs.
- Changing `webAssetsFS` fallback order.
- Launchd plist changes (`WorkingDirectory` is already `input.HomeDir`).

## Acceptance

- Rendered systemd units contain `WorkingDirectory=<home>` for both user and
  system inputs.
- Rendered units do not contain `KANDEV_WEB_DIST_DIR`.
- Existing ExecStart, bundle, and config-handoff assertions in
  `service_test.go` still pass.

## Verification

```bash
cd apps/backend && go test ./internal/launcher -count=1 -run 'TestRenderSystemdUnit'
```

## Files likely touched

- `apps/backend/internal/launcher/service.go` (`renderSystemdUnit`)
- `apps/backend/internal/launcher/service_test.go`

## Dependencies

None.

## Risks

- Setting `WorkingDirectory` on `--system` units as well as user units. That is
  intended: `/var/lib/kandev` (or the configured system home) is the correct
  process cwd.

## Parallelism

`parallel-safe`

## Inputs

- System design: Frontend packaging (working directory must be the live home).
- `renderLaunchdPlist` already writes `WorkingDirectory` from `input.HomeDir`.
- `webAssetsFS` in `apps/backend/internal/backendapp/helpers.go`.

## Results

- RED: `TestRenderSystemdUnitSetsWorkingDirectoryToHome` failed; units lacked `WorkingDirectory`.
- GREEN: `renderSystemdUnit` writes `WorkingDirectory=<home>` and still omits `KANDEV_WEB_DIST_DIR`.
- `cd apps/backend && go test ./internal/launcher -count=1 -run 'TestRenderSystemdUnit'` — pass.
