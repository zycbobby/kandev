#!/usr/bin/env python3
"""Reverse-export runtime workflows to versionable YAML files."""

import json
import os
import re
import sys
import urllib.error
import urllib.request


def slug(name):
    value = re.sub(r"[^a-z0-9]+", "-", name.lower()).strip("-")
    return value or "workflow"


def get(url, path):
    try:
        with urllib.request.urlopen(url.rstrip("/") + path, timeout=10) as response:
            return response.read()
    except urllib.error.HTTPError as error:
        sys.exit(
            f"sync-workflow: backend at {url} returned HTTP {error.code} for {path}"
        )
    except Exception as error:
        sys.exit(f"sync-workflow: backend unreachable at {url}: {error}")


def main():
    if len(sys.argv) != 3:
        sys.exit("usage: sync-workflow.py <url> <out_dir>")

    url, out_dir = sys.argv[1:]
    os.makedirs(out_dir, exist_ok=True)

    workspaces = json.loads(get(url, "/api/v1/workspaces"))["workspaces"]
    workspaces = sorted(workspaces, key=lambda workspace: workspace["id"])
    used = set()
    count = 0

    for workspace in workspaces:
        workspace_id = workspace["id"]
        path = (
            "/api/v1/workflows?workspace_id="
            + str(workspace_id)
            + "&include_hidden=true&exclude_office=false"
        )
        workflows = json.loads(get(url, path))["workflows"]
        workflows = sorted(workflows, key=lambda workflow: workflow["id"])

        for workflow in workflows:
            base = slug(workflow["name"])
            workspace_slug = slug(workspace["name"])
            filename = base
            if filename in used:
                filename = f"{base}--{workspace_slug}"
                suffix = 2
                while filename in used:
                    filename = f"{base}--{workspace_slug}--{suffix}"
                    suffix += 1
            used.add(filename)

            body = get(url, f"/api/v1/workflows/{workflow['id']}/export")
            with open(os.path.join(out_dir, filename + ".yml"), "wb") as output:
                output.write(body)
            count += 1

    print(f"synced {count} workflow(s) to {out_dir}")


if __name__ == "__main__":
    main()
