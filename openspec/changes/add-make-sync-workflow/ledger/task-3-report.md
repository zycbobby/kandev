# Task 3 report: sync-workflow script tests

## Implemented

- Added `scripts/sync-workflow.test.sh`, following the existing `pass`/`fail` shell harness style with `set -euo pipefail`, repository-root discovery, status aggregation, and explicit final exit status.
- Added Makefile `test-scripts` wiring immediately after `make-deploy.test.sh`.
- The test uses only Bash and Python 3. Its embedded Python `http.server` binds to an OS-selected high port and serves workspace, workflow, and YAML export responses without `curl` or `jq`.

## Coverage

- Confirms `make help` lists `sync-workflow`.
- Confirms `make -n sync-workflow` invokes `python3 scripts/sync-workflow.py`.
- Exercises the stub API with Alpha, Beta, and Empty workspaces.
- Confirms the generated set is exactly `kanban.yml`, `pr-review.yml`, and `kanban--beta.yml`.
- Confirms every generated YAML contains `type: kandev_workflow` and exactly one workflow name.
- Confirms the empty workspace produces no file.
- Stops and waits for the stub, then reuses its closed port to verify a nonzero failure and URL-bearing stderr.
- Cleanup uses both `kill` and `wait`.

## Validation

- `bash scripts/sync-workflow.test.sh` passed.
- A second independent `bash scripts/sync-workflow.test.sh` passed, confirming repeatability.
- `make test-scripts` reached the existing `dev-prod-db-path.test.sh` suite but failed there because this environment's inherited `HOME_DIR`/Make variable differs from that test's expected Windows-probe value. The new sync-workflow test itself passed when run through its direct command.

## Scope

Only `Makefile` and `scripts/sync-workflow.test.sh` are intended for the Task 3 commit. OpenSpec files remain unstaged.

## Review fix

- Replaced GNU-only `find -printf` with a portable `(cd "$output_dir" && ls -1 | sort)` listing.
- Updated the HTTP stub to return a truncated workflow list unless both `include_hidden=true` and `exclude_office=false` are present.
- Added stale-file preservation coverage by pre-creating `stale.yml`.
- Added idempotent overwrite coverage by running the sync twice and checking the second listing.
- Increased stub startup polling from 1 second to approximately 5 seconds.

Covering command:

- `bash scripts/sync-workflow.test.sh && bash scripts/sync-workflow.test.sh`

Both runs passed all checks. The output included the new `stale workflow is preserved`, `sync-workflow overwrites idempotently`, and `sync-workflow preserves listing on repeat` assertions, followed by `All sync-workflow checks passed.`
