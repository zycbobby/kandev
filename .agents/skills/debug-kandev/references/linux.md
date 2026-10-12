# Kandev on Linux

How a Kandev backend is installed, started, and observed on a Linux host. Read this
before resolving `$KH` or touching a running instance.

## Three ways a backend runs

| Mode | Started by | Home dir (`$KH`) | `MANAGER` in `kandev-instances` |
|---|---|---|---|
| user service | systemd user unit `kandev.service`, installed by `make service-install` | whatever the unit sets as `KANDEV_HOME_DIR` (default `~/.kandev`) | `systemd` |
| system service | `/etc/systemd/system/kandev.service`, installed by `sudo make service-install-system` | `/var/lib/kandev` unless overridden | `systemd` |
| dev / isolated | `make dev`, or `scripts/dev-isolated` | `<repo>/.kandev-dev`, or the temp dir `dev-isolated` prints | `-` |

Everything an instance owns lives under its home: `data/kandev.db`, `logs/`, `tasks/`,
`quick-chat/`, `supervisor/`.

## Resolve the live instance

The unit is the source of truth, not the default path:

```bash
systemctl --user show kandev.service -p Environment   # KANDEV_HOME_DIR, KANDEV_SERVER_PORT
systemctl --user cat kandev.service                   # full unit text
```

Cross-check against the running process, which also tells you the port and whether the
row you are looking at is the service-managed one:

```bash
make instances        # = scripts/kandev-instances --paths
```

A relocated install (`Environment=KANDEV_HOME_DIR=/media/.../kandev-home`) leaves
`~/.kandev` behind as a snapshot of an older run: 0 tasks, a small `kandev.db`, and a
stale `logs/backend-logs.log`. Querying it produces confident wrong answers, so when the
unit and `~/.kandev` disagree, the unit wins.

## Service lifecycle

`systemctl` directly:

```bash
systemctl --user status kandev.service
systemctl --user restart kandev.service
journalctl --user-unit kandev.service -n 200 --no-pager     # service stdout/stderr
journalctl --user-unit kandev.service -f                    # follow
```

Through the launcher, which wraps the same calls and works identically on macOS:
`make service-status | service-start | service-stop | service-restart | service-logs |
service-logs-follow | service-config`. `make deploy` publishes the current checkout to the
live home and reinstalls the unit; `make service-install` does a full install.

The backend also writes its own rolling log to `$KH/logs/backend-logs.log` (with the two
preceding UTC dailies next to it). That file is usually the better debugging source than
the journal, because it survives restarts and contains the structured application logs.

## Dev instances

`make dev` builds the launcher and runs backend plus web in the foreground against
`<repo>/.kandev-dev`, so a dev instance never touches the live database. Its tasks, DB, and
logs are all disposable. `make dev-prod-db` is the deliberate exception: dev mode against
the production database.

## Traps

- Two instances can listen on the same port on different hosts, and one host can run
  several at once. Always resolve the port from the instance you mean
  (`scripts/kandev-instances --raw`), never from a remembered number.
- `make service-config` prints the home dir the *CLI* would resolve right now (flag, then
  config, then `KANDEV_HOME_DIR`, then `~/.kandev`). It does not read the installed unit,
  so on a relocated install it can print a path the running service does not use. Trust the
  unit and the running process instead.
- A foreground instance (`MANAGER` = `-`) is safe to restart; a `systemd` one is restarted
  by the manager, so mutating its data mid-run produces state the engine did not observe.
  Write through the API, per the skill's write policy.
