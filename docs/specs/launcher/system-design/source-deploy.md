---
status: draft
system: launcher
requirements:
  - REQ-LAUNCHER-SOURCE-DEPLOY-001
  - REQ-LAUNCHER-SOURCE-DEPLOY-002
  - REQ-LAUNCHER-SOURCE-DEPLOY-003
created: 2026-08-24
owners:
  - kandev
---

# Source-checkout user-service deploy system design

## Purpose and boundaries

This design specifies how a Kandev source checkout publishes a production
runtime to the operator's existing user-domain daemon. It covers the `make
deploy` entrypoint, where the published bundle lives, how that bundle is
installed through `kandev service install` without `--system`, and how
frontend assets stay inside the binary.

It does not change `make dev`, system-service identity, release packaging, or
the public `kandev` command surface beyond invoking the existing user-domain
installer.

## Requirement mapping

| Requirement | Design section |
| --- | --- |
| `REQ-LAUNCHER-SOURCE-DEPLOY-001` | [Components and responsibilities](#components-and-responsibilities), [Control flow](#control-flow), [Failure and recovery](#failure-and-recovery) |
| `REQ-LAUNCHER-SOURCE-DEPLOY-002` | [Stable runtime location](#stable-runtime-location), [Control flow](#control-flow) |
| `REQ-LAUNCHER-SOURCE-DEPLOY-003` | [Frontend packaging](#frontend-packaging) |

## Components and responsibilities

| Component | Responsibility |
| --- | --- |
| Root Makefile `deploy` target | Operator-facing entry. Builds a production runtime from the current checkout, publishes it to the stable runtime directory, then runs user-domain `service install`. Listed by `make help`. |
| Existing `build-web`, `sync-embedded-web`, and `runtime-bundle` targets | Produce the production Vite SPA, copy it into `apps/backend/internal/webapp/embedded/generated`, and compile `dist/kandev` with `kandev` plus `agentctl` helpers. Checkout `dist/kandev` is build staging only. |
| Stable runtime directory | Checkout-independent copy of the runtime bundle. Default: `<live-home>/runtime` (typically `~/.kandev/runtime`). The live unit's `ExecStart` and `KANDEV_BUNDLE_DIR` point here. |
| Native launcher `kandev service install` | Existing user-domain installer in `apps/backend/internal/launcher`. Deploy invokes it from the stable `bin/kandev` so `executablePath()` records the published binary, not the checkout staging path. No `--system`. |
| User systemd unit | `~/.config/systemd/user/kandev.service`, managed marker already used by the installer. On macOS, the same user-domain path is the existing LaunchAgent installer; Linux user systemd is the required target. |

`make service-install` remains the current checkout-local installer (it runs
`dist/kandev/bin/kandev service install`, so `ExecStart` can stay inside the
tree). `make deploy` is the command that satisfies isolation: it publishes
first, then installs from the published binary.

Optional Makefile overrides already used by service install remain valid on
deploy: `PORT`, `HOME_DIR`, `NO_BOOT_START`. Deploy must not invent a second
flag language.

## Data and contracts

### Public operator contract

```text
make deploy
make deploy PORT=<port> HOME_DIR=<path>
```

- `make deploy` is source-checkout only. There is no `kandev deploy` CLI.
- User-domain only. The target never passes `--system`.
- After success, `kandev service status`, `logs`, `restart`, and `config`
  without `--system` continue to operate the same unit.

### Live unit contract after deploy

The rewritten user unit shall:

- Execute the published `bin/kandev --headless` under the stable runtime
  directory.
- Set `KANDEV_BUNDLE_DIR` to that stable bundle.
- Set `KANDEV_HOME_DIR` to the live service home (existing home on reinstall).
- Omit `KANDEV_WEB_DIST_DIR`.
- Keep `KANDEV_RUNNING_AS_SERVICE=true` and the existing managed marker.

### Stable runtime location

Default published bundle:

```text
<live-home>/runtime/bin/kandev
<live-home>/runtime/bin/agentctl
<live-home>/runtime/bin/agentctl-*
```

`<live-home>` is the home already recorded by the managed user unit when one
exists. Otherwise it is the user-service default from
`resolveServiceHome` (`KANDEV_HOME_DIR`, else `~/.kandev`).

Rejected locations:

- `<checkout>/dist/kandev` as `ExecStart`. A task worktree, `make clean`, or
  branch switch would take down the live daemon.
- `/usr/local` or other root-owned prefixes. User-domain deploy must not
  require `sudo`.
- The development home `.kandev-dev`.

Checkout `dist/kandev` stays a staging directory. Publish replaces
`<live-home>/runtime/bin` atomically, matching the existing
`runtime-bundle` staging pattern (`mktemp` then `mv`).

## Frontend packaging

Deploy uses the production embed path already decided in
[ADR-0022](../../../decisions/0022-embedded-vite-assets.md):

1. `make build-web` writes `apps/web/dist`.
2. `make sync-embedded-web` copies that tree into
   `apps/backend/internal/webapp/embedded/generated`.
3. The backend compile embeds those files through
   `apps/backend/internal/webapp/embedded`.
4. The published unit does not set `KANDEV_WEB_DIST_DIR`.

`KANDEV_WEB_DIST_DIR` remains an unsupported override for previews and tests.
Deploy must not promote it into a live-service contract. A later `pnpm build`
in the checkout must not change the live UI until the next `make deploy`.

The backend still prefers a filesystem dist when `KANDEV_WEB_DIST_DIR` is set
or when `apps/web/dist` exists relative to the process working directory
(`webAssetsFS` in `apps/backend/internal/backendapp/helpers.go`). The
published unit must not use the checkout as its working directory. systemd
user units currently inherit `/` unless set; deploy should set the unit
working directory to the live home (launchd already does) so a leftover
relative `apps/web/dist` cannot shadow the embed.

## Control flow

```text
make deploy
  -> ensure Go and pnpm workspace dependencies for the production build
     (not `make install`; that target also installs Playwright browsers)
  -> build-web
  -> sync-embedded-web
  -> runtime-bundle into <checkout>/dist/kandev
  -> resolve live home from existing user unit/config, else user-service default
  -> refuse if live home is the checkout or `.kandev-dev`
  -> atomically publish dist/kandev/bin into <live-home>/runtime/bin
  -> <live-home>/runtime/bin/kandev service install
       (no --system; pass HOME_DIR/PORT only when the operator set them)
  -> user systemd (or macOS LaunchAgent) reloads and starts the unit
```

Reinstall preservation:

- If `~/.config/systemd/user/kandev.service` is a Kandev-managed unit, read its
  `KANDEV_HOME_DIR` and listener-related environment and reuse them unless the
  operator passed `HOME_DIR` or `PORT`.
- Do not pass `--home-dir` pointing at the checkout.
- Do not enable lingering or create host users.

`make dev` is unchanged: it keeps using `<repo>/.kandev-dev` and its own ports
as specified by [go-dev-launcher](../../go-dev-launcher/spec.md).

## Failure and recovery

| Failure | Behavior |
| --- | --- |
| Missing Linux systemd user manager, or service install unsupported on the OS | Exit non-zero with the existing installer message. Do not write a system unit or a Windows service. |
| Web or backend build failure | Exit non-zero. Do not publish and do not rewrite the unit. |
| Incomplete staging bundle (missing `bin/kandev` or required `agentctl` helpers) | Exit non-zero before replacing `<live-home>/runtime/bin`. |
| Publish failure | Leave the previous `<live-home>/runtime/bin` in place when a previous deploy exists. |
| `service install` failure after a successful publish | Exit non-zero. Data in the live home is untouched. The operator can inspect `kandev service status` / `logs` and retry `make deploy`. |
| Existing unmanaged unit (no managed marker) | Keep the current installer behavior: back up to `*.bak` and replace only after that backup succeeds. |

Deploy does not poll `/health`. Readiness remains the existing service
workflow: `kandev service status` and `logs`, then `GET /health` on the URL
those logs print.

## Persistence

- Live SQLite data stays in the live home (`<live-home>/data/kandev.db` unless
  configured otherwise). Deploy never copies, migrates, or points that
  database at `.kandev-dev`.
- `service/install.json` under the live home remains the installer metadata
  file already written by `kandev service install`.
- Published binaries under `<live-home>/runtime` are replaceable artifacts, not
  a second configuration store.

## Security

- User-domain only: no `sudo`, no writes under `/etc/systemd/system` or
  `/var/lib/kandev`.
- The published runtime directory is owned by the installing user.
- Deploy does not change authentication, bind address, or TLS. Those stay in
  the live `config.yaml` and the public run-as-a-service guidance.
- The unit environment stays the small fixed set the installer already writes.
  Deploy does not copy arbitrary shell exports from the checkout into the
  daemon.

## Observability

On success, deploy prints:

- The published executable path.
- The live Kandev home.
- That the install is user-domain (not `--system`).

Existing `kandev service status`, `logs`, and `config` remain the inspection
commands. No new metrics are required.

## Related decisions

- [ADR-0022 Embedded Vite assets](../../../decisions/0022-embedded-vite-assets.md):
  production SPA is embedded; `KANDEV_WEB_DIST_DIR` is not a supported deploy
  contract.
- [ADR-0021 Go-served SPA with boot state](../../../decisions/0021-go-served-spa-with-boot-state.md):
  production `run` / `start` / service serve the SPA from Go, not Node.
- Legacy [native Kandev CLI](../../native-kandev-cli/spec.md) remains
  authoritative for `kandev service install` flags and system-service
  identity.
- Legacy [Go dev launcher](../../go-dev-launcher/spec.md) remains
  authoritative for `.kandev-dev` isolation.

When this capability is implemented, public how-to text belongs on
[Run as a service](../../../public/run-as-a-service.md) and the Makefile help
listed in contributor docs. Those pages are updated in the same change as the
target, not in this design-only turn.
