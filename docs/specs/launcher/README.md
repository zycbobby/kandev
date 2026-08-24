---
status: draft
system: launcher
specification_version: 1
migration: in_progress
owners:
  - kandev
---

# Launcher system

## Purpose

The launcher system owns how operators start, install, and update a Kandev
process from a source checkout or an installed runtime: `make dev`, `make start`,
`kandev service`, and source-checkout deploy of the live user-domain daemon.

## Ownership

This system owns operator-facing launch entrypoints, the native `kandev`
launcher commands that those entrypoints invoke, user-domain service install
and reinstall from a source checkout, and the contract that a deployed
production process stays isolated from development mode.

## Exclusions

- Task worktrees, databases, and session lifecycle belong to the
  [task system](../tasks/README.md).
- Homebrew, npm, and Scoop release packaging remain in their legacy
  specifications until migrated.
- Desktop/Tauri packaging belongs to the desktop application specifications.
- Docker and Kubernetes deployment belong to the public operations documents
  and are not a launcher-system install path.
- System (`--system`) service account and home-ownership rules remain in the
  legacy [native Kandev CLI](../native-kandev-cli/spec.md) specification until
  that document is migrated here.
- `make dev` isolation details remain in the legacy
  [Go dev launcher](../go-dev-launcher/spec.md) specification until migrated.

## Specification map

### Requirements

- [Source-checkout user-service deploy](requirements/source-deploy.md)

### System design

- [Source-checkout user-service deploy](system-design/source-deploy.md)

## Migration status

This system is new. Only the source-deploy capability is specified in the
system layout. Legacy [native-kandev-cli](../native-kandev-cli/spec.md) and
[go-dev-launcher](../go-dev-launcher/spec.md) remain authoritative for the
public `kandev` command surface, existing `kandev service` behavior, and
`make dev` until they are extracted into this system.

## Related systems

- [Native Kandev CLI](../native-kandev-cli/spec.md): public `kandev` commands
  and `kandev service install` (user and system).
- [Go dev launcher](../go-dev-launcher/spec.md): `make dev` and `.kandev-dev`
  isolation.
- [Startup configuration parity](../platform/startup-configuration-parity.md):
  config file discovery and environment/YAML precedence.
- [Homebrew Core](../homebrew-core/spec.md): source-built runtime bundle for
  package managers, not `make deploy`.
