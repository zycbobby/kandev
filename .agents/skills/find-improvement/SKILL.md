---
name: find-improvement
description: >
  Turn session evidence into harness fixes. Collect from one Kandev task
  (given its id) or sweep one day of workspace sessions (the nightly review
  automation): extract every user adjustment, correction, and suggestion,
  cluster them into recurring patterns, attribute each pattern to a workflow
  step or skill deficiency, present a findings list, and apply only the
  confirmed ones: workflow step changes via the API, skill and doc changes
  as file edits. Use when the user gives a Kandev task id and asks to
  improve the related workflows and skills, asks for a review of today's
  sessions, or when the nightly review automation fires.
---

# Find Improvement

The conversation record is the evidence: every time the user had to correct
or redirect the agent mid-task, some workflow step or skill was missing or
mis-triggered that constraint. Collect, cluster, attribute, confirm, then
apply. Never skip the confirmation gate.

## Phase 1: Collect (read-only)

Resolve the evidence scope first.

**One task.** Resolve the id (read-only lookup via
`GET /api/v1/workspaces/<id>/tasks` on the local backend, or the sqlite
read documented in the debug-kandev skill; use a raw id as-is). Record
title, workflow_id, and state. Page through its conversations
(`list_task_sessions_kandev` + `get_task_conversation_kandev`, or
`GET /tasks/<id>/sessions` with `GET /task-sessions/<id>/turns`).

**Daily sweep.** Every user message since local midnight in the target
workspace, excluding automation-originated tasks (the nightly runs' own
prompts are not user feedback):

```sql
SELECT t.id, t.title, ts.id AS session_id, m.created_at, m.content
FROM task_session_messages m
JOIN task_sessions ts ON ts.id = m.task_session_id
JOIN tasks t          ON t.id = ts.task_id
WHERE t.workspace_id = '<workspace-id>'
  AND t.origin != 'automation_run'
  AND m.author_type = 'user'
  AND m.created_at >= date('now', 'localtime')
ORDER BY m.created_at;
```

Both scopes keep only user messages. Drop pure acknowledgments; keep every
adjustment, correction, suggestion, and preference with its task, session,
and timestamp. Pull surrounding agent turns only where a message's meaning
depends on them.

**Cluster before attributing.** The same correction in several tasks is a
modeling failure, not a coincidence. Group identical-pattern feedback;
recurring groups outrank one-offs. A one-off is weak evidence: it may still
justify a row, marked uncertain.

Read the current state of the likely targets before attributing:

- Workflow: `list_workflow_steps_kandev` or
  `GET /api/v1/workflows/<id>/workflow/steps` over the workflows the
  affected tasks ran on: step prompts, events, `auto_advance_requires_signal`.
- Skills: the skills the conversations invoked plus ones that should have
  fired but did not; the repo files under `.agents/skills/`
  (`.claude/skills` links here) and the AGENTS.md / CLAUDE.md guidance. A
  skill attributed to another repository resolves through that task's
  registered local path; mark targets you cannot read instead of guessing
  their content.

## Phase 2: Findings list (stop and wait)

One table row per pattern: evidence (verbatim, trimmed, with task
references) → root cause (which constraint was missing or mis-triggered)
→ target (step id + field, or file path) → proposed change → fix shape →
verification. Order by impact; mark rows you are not sure about as
uncertain.

Every proposed change names exactly one fix shape:

- **Rescope**: the guidance already exists but did not fire. Fix the
  trigger: a skill description, a step prompt's opening instruction, a
  move into path-scoped AGENTS.md. No new content.
- **Restructure**: same knowledge, better shape. Decompose a bloated
  skill, merge near-duplicates, move detail into `references/`, reorder
  steps or rewrite an event chain.
- **Remove**: delete superseded, duplicate, or contradicted constraints.
- **Model**: a genuinely new class of behavior, expressed once at its
  narrowest home. The only shape allowed to add length.

A fix that appends one more sentence to silence one session's complaint is
a monkey-patch; re-attribute before proposing it. Guidance that exists but
did not fire is a Rescope. A recurring new class is a Model. A constraint
list that keeps growing is a Restructure or a Remove. Prefer changes that
net-shrink what an agent must read: a sharper trigger, a better
decomposition, or a deletion carries the same constraint as a longer
prompt, without the comprehension cost.

STOP and wait for confirmation. The user may cut or rewrite rows; only
confirmed rows move to phase 3. The nightly sweep ends its run here: the
findings report is the deliverable, in the language the user's feedback
used, with the date in its heading.

## Phase 3: Apply

- Workspace guard: skill and doc edits require the kandev repo checkout. In
  any other workspace (a task worktree, another repo), emit file changes as a
  checklist instead of editing. Workflow API changes are not workspace-bound
  and apply normally.
- Workflow: MCP (`update_workflow_step_kandev`) or REST
  (`PUT /api/v1/workflow/steps/<id>`). Never write SQLite directly. If
  `auto_advance_requires_signal` changes, rewrite the step prompt's legacy
  auto-advance wording in the same update; the debug-kandev skill documents
  why the gate and the wording must move together.
- Skills and docs: edit files in place following the harness-improvement
  skill. Each edit traces to a confirmed row and stays within that row's
  fix shape. No drive-by refactoring.

## Report

Changed step ids with the fields touched, changed files, and confirmed rows
intentionally left unapplied with the reason.
