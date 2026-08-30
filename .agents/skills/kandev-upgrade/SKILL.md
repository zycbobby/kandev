---
name: kandev-upgrade
description: >
  Upgrade the local self-hosted Kandev instance in place: record the current running
  version and checkout tag, take a manual SQLite backup (keeping only the two most
  recent snapshots), fetch and merge upstream/main into the local main branch (surfacing
  conflicts instead of resolving them), run `make deploy`, then self-check the live service.
  Use whenever the user asks to upgrade/update Kandev, pull the latest code, merge upstream,
  deploy the latest build, or run `make deploy` — even if they don't say "skill". This is a
  single-session, user-started operation on the live service; do not delegate the deploy or
  the merge.
---

# Kandev self-upgrade

Upgrade the local user-domain Kandev deployment from `upstream/main` and redeploy it.
The three phases are ordered and each must finish (or stop deliberately) before the next
starts. Everything runs in the primary session; the merge and deploy touch the live install
and are never handed to a subagent.

## Environment facts (verify, don't assume)

- Repo root: `/media/zuo/AigoData/GameCode/kandev`. Remote `upstream` is the canonical
  `kdlbs/kandev`; `origin` is the user's fork. Branch is `main`.
- The live service is a **systemd user unit**. Its data dir is whatever
  `KANDEV_HOME_DIR` the unit sets — on this machine that is
  `/media/zuo/AigoData/kandev-home`, and `~/.kandev` is a **stale backup, not live data**.
  Always resolve the home dir from the unit, never from `~/.kandev` alone.
- Backend port is resolved via `scripts/kandev-instances` (default `38429`). Do not
  hardcode the port on a multi-instance box.

## Phase 1 — record and back up

Run `scripts/upgrade-preflight.sh`. It resolves the live home dir and port itself, prints the
current running version (`/health`) and the checkout's `git describe` + tag, takes a manual
backup through `POST /api/v1/system/backups`, waits for the new snapshot to appear, then
deletes all but the two most recent snapshots (matching the backend's own retention).
Report the "before" version and tag to the user; keep the backup name if they later want a
rollback.

## Phase 2 — fetch and merge

```bash
cd /media/zuo/AigoData/GameCode/kandev
git fetch upstream
git log --oneline main..upstream/main | head -40   # show the user what's incoming
git merge upstream/main --no-edit
```

- If the merge is clean, report the new merge commit and move on.
- **If there are conflicts: stop.** List the conflicted files (`git diff --name-only
  --diff-filter=U`), explain which upstream changes collide with local commits, and ask the
  user how to proceed. Never auto-resolve or `--abort` without being told. The skill's job
  here is to surface, not to silently pick a side.
- A dirty working tree means uncommitted local work exists: stop and ask before merging
  anything on top of it.

## Phase 3 — deploy and self-check

`make deploy` builds the backend, installs web deps, bundles the runtime, and reinstalls the
user-domain service — it briefly stops and restarts the daemon, so tell the user that first.
Run it from the repo root in the background and watch the log rather than blocking silently:

```bash
cd /media/zuo/AigoData/GameCode/kandev
nohup make deploy > /tmp/kandev-deploy.log 2>&1 &
# then monitor /tmp/kandev-deploy.log until it prints "User-domain service deployed" or an error
```

The deploy script (`scripts/deploy-user-service.sh`) publishes to the live home it resolves
from the systemd unit and refuses `.kandev-dev` or checkout paths, so it will not clobber a
dev instance.

Self-check after the log shows success:

```bash
systemctl --user is-active kandev.service          # expect: active
curl -s http://127.0.0.1:<port>/health             # expect: status ok + a new version
git describe --tags --always                        # must equal the version /health reports
```

Report the before → after version, the merge commit, and the health-check result. If the
user was chasing a specific upstream fix, optionally confirm it is an ancestor of the
deployed commit:

```bash
git merge-base --is-ancestor <fix-sha> HEAD && echo "fix included" || echo "fix missing"
```

## Notes and boundaries

- Upgrading the binary does **not** repair already-broken runtime data (e.g. a
  `workspace_reuse_unsafe` task environment written by an older build). Say so if relevant;
  fixing old rows is a separate, opt-in task (reset environment or manual DB repair), never
  part of this upgrade flow.
- The backend takes an automatic SQLite snapshot on the next boot when the version changes;
  the manual Phase-1 backup is a belt-and-braces copy you control and can prune.
- Do not run `make deploy` concurrently with another session's deploy or dev watcher against
  the same live home.
