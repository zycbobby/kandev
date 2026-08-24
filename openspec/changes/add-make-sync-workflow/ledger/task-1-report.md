# Task 1 report: `scripts/sync-workflow.py`

## Implemented

Added the executable Python 3 stdlib CLI `scripts/sync-workflow.py`.

- Fetches workspaces, then workflows for each workspace, using the required list query parameters.
- Sorts workspaces and workflows by ID.
- Exports each workflow as raw response bytes to a uniquely predictable `.yml` filename.
- Preserves existing files and only writes or overwrites generated workflow files.
- Reports HTTP and unreachable-backend failures to stderr with the base URL and exits non-zero.
- Prints the synchronized workflow count on success.

## Tests

Commands and observed output:

```text
$ python3 -m py_compile scripts/sync-workflow.py
# exit 0

$ rm -rf /tmp/sync-smoke; python3 scripts/sync-workflow.py http://localhost:38429 /tmp/sync-smoke
synced 8 workflow(s) to /tmp/sync-smoke
# exit 0

$ find /tmp/sync-smoke -maxdepth 1 -type f -name '*.yml' -printf '%f\\n' | sort
development.yml
dev-openspec-workflow.yml
explore.yml
fix.yml
kanban.yml
spec-driven-development.yml
spec.yml
tdd.yml

$ python3 scripts/sync-workflow.py http://localhost:1 /tmp/sync-failure
# exit 1
# stderr: sync-workflow: backend unreachable at http://localhost:1: <urlopen error [Errno 111] Connection refused>
```

## Files changed

- `scripts/sync-workflow.py` (new, executable)
- `openspec/changes/add-make-sync-workflow/ledger/task-1-report.md` (this report)

## Self-review

Read the complete script after the smoke tests and reviewed the implementation against the task brief. Fixed an initial Python f-string quoting syntax error before verification. No remaining concerns identified.

## Concerns

None.
