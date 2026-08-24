---
status: draft
system: launcher
created: 2026-08-24
owners:
  - kandev
---

# Source-checkout user-service deploy requirements

## Overview

Operators who develop Kandev on the same machine they use as a product need one
source-checkout command that rebuilds the current tree and updates the live
user-domain daemon. Development (`make dev`) must stay on its isolated home,
port, and Vite process. The live daemon must keep its existing home and data
and must not start serving the checkout's working tree.

## Terminology

- **Source checkout:** A git working tree of this repository that can run the
  root Makefile.
- **User-domain daemon:** The Kandev process installed by `kandev service
  install` without `--system`. On Linux this is the user systemd unit
  `kandev.service`.
- **Live service:** The currently installed user-domain daemon and the Kandev
  home and database that daemon uses.
- **Development mode:** `make dev` / `kandev dev`, which uses the isolated
  development home described by the Go dev launcher specification.

## Requirements

### REQ-LAUNCHER-SOURCE-DEPLOY-001: Source-checkout user-service deploy

**Intent:** Give a source-checkout operator a single command that builds the
current tree and installs or replaces the live user-domain daemon.

**User story:** As an operator who develops and runs Kandev on the same
machine, I want `make deploy` so I can update the daemon I actually use without
mixing it with `make dev`.

#### Acceptance criteria

- **AC-LAUNCHER-SOURCE-DEPLOY-001.1:** When the operator runs `make deploy`
  from a Kandev source checkout on Linux with a working systemd user manager,
  the system shall build a production runtime from that checkout and install or
  replace the user-domain daemon (`kandev service install` without `--system`).
- **AC-LAUNCHER-SOURCE-DEPLOY-001.2:** When deploy succeeds, the live service
  shall run a production `kandev --headless` process built from that checkout.
  It shall not run development mode and shall not start a Vite development
  server.
- **AC-LAUNCHER-SOURCE-DEPLOY-001.3:** When a managed user-domain daemon
  already exists, deploy shall preserve that daemon's existing Kandev home and
  listener configuration unless the operator explicitly overrides them.
- **AC-LAUNCHER-SOURCE-DEPLOY-001.4:** When no user-domain daemon exists,
  deploy shall install a new user-domain service using the existing user-service
  home default (`~/.kandev` unless configuration or environment already selects
  another home).
- **AC-LAUNCHER-SOURCE-DEPLOY-001.5:** When deploy is invoked, the system shall
  not install or replace a system (`--system`) unit, a Windows service, a
  Docker container, or a Kubernetes workload.
- **AC-LAUNCHER-SOURCE-DEPLOY-001.6:** When `make help` is shown, it shall list
  `deploy` as the source-checkout command that builds the current tree and
  updates the user-domain daemon.
- **AC-LAUNCHER-SOURCE-DEPLOY-001.7:** When the production build fails, deploy
  shall exit non-zero and shall leave the previously installed live service
  binary and unit in place.

### REQ-LAUNCHER-SOURCE-DEPLOY-002: Live service isolation from development

**Intent:** Keep the live daemon's binary, data, and ports separate from
development mode and from the source checkout that produced the build.

**User story:** As an operator, I want the daemon I use as a product to keep
its own binary, home, and port so `make dev` and later checkout builds cannot
quietly change it.

#### Acceptance criteria

- **AC-LAUNCHER-SOURCE-DEPLOY-002.1:** When deploy succeeds, the live service
  executable and runtime bundle shall live outside the source checkout. A later
  `make clean`, branch switch, or worktree deletion shall not remove that
  running executable.
- **AC-LAUNCHER-SOURCE-DEPLOY-002.2:** When deploy succeeds, the live service
  shall keep using the live Kandev home and database. It shall not switch to
  the development home (`.kandev-dev`) and shall not use the source checkout as
  `KANDEV_HOME_DIR`.
- **AC-LAUNCHER-SOURCE-DEPLOY-002.3:** When the operator later runs `make dev`
  in the same checkout, that process shall keep the existing development
  isolation (isolated home, port, and Vite) and shall not replace the deployed
  user-domain unit, binary, or live home.
- **AC-LAUNCHER-SOURCE-DEPLOY-002.4:** When the source checkout is a Kandev
  task worktree, deploy shall still update the operator's user-domain daemon
  and shall not use that worktree as the live runtime location.

### REQ-LAUNCHER-SOURCE-DEPLOY-003: Embedded production SPA

**Intent:** Ship the production web UI inside the deployed binary so frontend
assets are not a second live directory and cannot drift from the checkout's
un-deployed `dist`.

**User story:** As an operator, I want deploy to include the production UI in
the same binary as the backend so I do not manage a separate live frontend tree.

#### Acceptance criteria

- **AC-LAUNCHER-SOURCE-DEPLOY-003.1:** When deploy builds the runtime, the
  production Vite SPA shall be included in the deployed `kandev` binary.
- **AC-LAUNCHER-SOURCE-DEPLOY-003.2:** When the deployed user-domain daemon
  starts, it shall serve that embedded SPA and shall not configure
  `KANDEV_WEB_DIST_DIR` on the service unit.
- **AC-LAUNCHER-SOURCE-DEPLOY-003.3:** When the operator later builds or
  changes frontend assets in the source checkout without running deploy, the
  live service shall continue to serve the previously deployed embedded SPA.

## Out of scope

- System-service install (`kandev service install --system`) and `/var/lib/kandev`.
- Docker, Kubernetes, remote hosts, and the PR preview sprite deployer.
- Homebrew, npm, Scoop, and other release-channel upgrades.
- A public `kandev deploy` command for installs that are not a source checkout.
- Changing `make dev` isolation, `make start`, or Desktop/Tauri packaging.
- Serving the live UI from a filesystem directory beside the binary.
- Windows Service Control Manager.
- First-time creation of host users, lingering configuration, or TLS/auth
  hardening. Those remain existing service-install and operations concerns.
