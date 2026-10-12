# Kandev on macOS

How a Kandev backend is installed, started, and observed on macOS. Read this before
resolving `$KH` or touching a running instance.

## Three ways a backend runs

| Mode | Started by | Home dir (`$KH`) | `MANAGER` in `kandev-instances` |
|---|---|---|---|
| user service | launchd LaunchAgent `com.kdlbs.kandev`, installed by `make service-install` | `KANDEV_HOME_DIR` in the plist | `launchd` |
| system service | `/Library/LaunchDaemons/com.kdlbs.kandev.plist`, installed by `sudo make service-install-system` | `/var/lib/kandev` unless overridden | `launchd` |
| dev / isolated | `make dev`, or `scripts/dev-isolated` | `<repo>/.kandev-dev`, or the temp dir `dev-isolated` prints | `-` |

Everything an instance owns lives under its home: `data/kandev.db`, `logs/`, `tasks/`,
`quick-chat/`, `supervisor/`. A `make service-install` on this platform also writes
`$KH/service/install.json` (manager, mode, home, bundle, launcher, installed version) and
the launcher CLI itself into `$KH/runtime/bin/`.

## Resolve the live instance

Start from the running process, which reports the home, ports, and manager in one shot:

```bash
make instances        # = scripts/kandev-instances --paths
```

If `HOME_DIR` shows `?` (see the trap below) or you want the installed definition, the
plist carries the same environment the service starts with:

```bash
plutil -p ~/Library/LaunchAgents/com.kdlbs.kandev.plist
plutil -extract EnvironmentVariables.KANDEV_HOME_DIR raw \
  ~/Library/LaunchAgents/com.kdlbs.kandev.plist
launchctl print gui/$UID/com.kdlbs.kandev        # state, program, pid
```

`~/.kandev` is only the default for a machine that never relocated its install. Do not
assume it: a service installed with `--home-dir` (or `HOME_DIR=` on `make service-install`)
keeps its live database somewhere else, and the `~/.kandev` copy is a stale snapshot whose
queries return confidently wrong answers.

## Service lifecycle

`launchctl` directly:

```bash
launchctl print gui/$UID/com.kdlbs.kandev                  # status, pid, program
launchctl kickstart -k gui/$UID/com.kdlbs.kandev           # restart
launchctl bootout gui/$UID/com.kdlbs.kandev                # stop (unload)
```

Through the launcher, which picks the right verb for the platform:
`make service-status | service-start | service-stop | service-restart | service-logs |
service-logs-follow | service-config`. `service logs` tails `<home>/logs/service.out` and
`service.err`, which is where launchd collects the service's stdout/stderr. `make deploy`
publishes the current checkout to the live home and reinstalls the agent.

For workflow debugging prefer the backend's own rolling log, `$KH/logs/backend-logs.log`
(with the two preceding UTC dailies beside it): it survives restarts, contains structured
application logs, and is in the same place on Linux.

## Dev instances

`make dev` builds the launcher and runs backend plus web in the foreground against
`<repo>/.kandev-dev`, so a dev instance never touches the live database. Its tasks, DB, and
logs are disposable. `make dev-prod-db` is the deliberate exception: dev mode against the
production database.

## Traps

- **Environment reads are not always available.** `scripts/kandev-instances` reads a
  process's environment to fill `HOME_DIR` and `MANAGER` (`ps eww` on this platform). macOS
  exposes it for launchd-managed backends, which is the case that matters for the live
  instance, but not for every process: a shell-spawned `make dev` backend can show `?`.
  Fall back to `REPO_PATH` and remember the dev home is `<repo>/.kandev-dev`.
- **`make service-config` prints the home dir the CLI would resolve right now** (flag, then
  config, then `KANDEV_HOME_DIR`, then `~/.kandev`). It does not read the installed plist,
  so on a relocated install it can print a path the running service does not use. Trust the
  plist and the running process instead.
- **Repo scripts run under bash 3.2** when Homebrew's bash is not first on `PATH`. The
  `kandev-*` scripts are kept compatible; if a new script fails with `declare: -A: invalid
  option` or `mapfile: command not found`, that is bash 4 syntax, not a data problem. Run it
  with `/opt/homebrew/bin/bash` to confirm.
- A foreground instance (`MANAGER` = `-`) is safe to restart; a `launchd` one is restarted
  by the manager, so mutating its data mid-run produces state the engine did not observe.
  Write through the API, per the skill's write policy.
