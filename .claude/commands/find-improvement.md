---
description: Turn a finished Kandev task's user feedback into workflow and skill improvements.
argument-hint: "<taskId>"
allowed-tools: Bash Read Edit Write Grep Glob
model: inherit
effort: high
---

Use `.agents/skills/find-improvement/SKILL.md`; the root `AGENTS.md`/`CLAUDE.md`
single-session model workflow applies. Present the findings list and stop for
the user's confirmation before applying anything. Workflow changes go through
the kandev MCP tools or the REST API, never direct SQLite writes. Do not commit
unless asked.
