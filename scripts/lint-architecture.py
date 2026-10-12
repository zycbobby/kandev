#!/usr/bin/env -S uv run --no-project --python 3.12 --script
"""Enforce reviewed architecture budgets and compatibility expiry metadata."""

from architecture_lint.cli import main


if __name__ == "__main__":
    raise SystemExit(main())
