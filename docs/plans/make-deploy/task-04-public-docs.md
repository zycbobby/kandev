---
id: "04-public-docs"
title: "Document source-checkout deploy"
status: done
wave: 3
depends_on:
  - "03-makefile-deploy-target"
plan: "plan.md"
requirements:
  - REQ-LAUNCHER-SOURCE-DEPLOY-001
  - REQ-LAUNCHER-SOURCE-DEPLOY-002
acceptance_criteria:
  - AC-LAUNCHER-SOURCE-DEPLOY-001.6
  - AC-LAUNCHER-SOURCE-DEPLOY-002.3
system_design:
  - ../../specs/launcher/system-design/source-deploy.md
---

# Task 04: Document source-checkout deploy

## Summary

Teach operators that `make deploy` updates the live user-domain daemon from a
source checkout, keeps data in the existing home, and is distinct from
`make dev` and `make service-install`.

## In scope

- `docs/public/run-as-a-service.md` source-checkout section (currently the
  `make service-install` block).
- A short pointer in `docs/public/contributing.md` under local run commands.
- Keep `make service-install` documented as the checkout-local installer.

## Out of scope

- Makefile or installer behavior changes.
- System-service, Docker, Kubernetes, or release-channel docs.
- Spec or plan edits unless a command name drifted during implementation.

## Acceptance

- Public docs name `make deploy` as the source-checkout command that publishes
  to the live user-domain runtime and reinstalls that daemon.
- They state that `make dev` stays on `.kandev-dev` and that frontend assets
  are embedded in the published binary.
- They contrast `make service-install` (ExecStart can remain under
  `dist/kandev`) with `make deploy` (`<live-home>/runtime`).

## Verification

```bash
node --test scripts/validate-public-docs.test.mjs
node scripts/validate-public-docs.mjs
```

Also grep the public pages for `make deploy` so the new heading is not
dropped from `docs/public/meta.json` navigation if a new page is added (prefer
editing the existing run-as-a-service page instead of adding a page).

## Files likely touched

- `docs/public/run-as-a-service.md`
- `docs/public/contributing.md`

## Dependencies

Task 03, so the documented flags and target name match the Makefile.

## Risks

- Do not document `KANDEV_WEB_DIST_DIR` as supported deploy configuration
  (`docs/public/configuration.md` already classifies it as internal).

## Parallelism

`sequential`

## Inputs

- System design: Related decisions (public how-to belongs on run-as-a-service).
- Current source-checkout section: `docs/public/run-as-a-service.md` around
  "Run a source checkout as a service".
- `/docs-maintainer` workflow and `docs/public/README.md`.

## Results

- Updated `docs/public/run-as-a-service.md` and `docs/public/contributing.md`.
- `node --test scripts/validate-public-docs.test.mjs` — 61 passed.
- `node scripts/validate-public-docs.mjs` — 41 published pages validated.
