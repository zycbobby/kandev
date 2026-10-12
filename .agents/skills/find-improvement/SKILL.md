---
name: find-improvement
description: >
  Turn the user's in-task feedback into harness fixes. Given a Kandev taskId,
  extract every adjustment, correction, and suggestion the user gave across the
  task's conversations, attribute each to a workflow step or skill deficiency,
  present a findings list, and apply only the confirmed ones: workflow step
  changes via the API, skill and doc changes as file edits. Use when the user
  gives a Kandev task id and asks to improve the related workflows and skills
  from their in-task feedback.
---

# Find Improvement

The task's own conversation is the evidence: every time the user had to correct
or redirect the agent mid-task, some workflow step or skill was missing that
constraint. Extract, attribute, confirm, then apply. Never skip the
confirmation gate.

## Phase 1: Collect (read-only)

1. Resolve the task. Use a raw id as-is; if given a title, resolve it with a
   read-only lookup first (`GET /api/v1/tasks` on the local backend, or the
   sqlite read documented in the debug-kandev skill). Record title,
   workflow_id, and state.
2. List the task's sessions (`list_task_sessions_kandev`), then page through
   each conversation (`get_task_conversation_kandev`, advancing with `after`
   until exhausted). Keep only user messages. Drop pure acknowledgments;
   keep every adjustment, correction, suggestion, and preference with its
   session and timestamp.
3. Read the current state of the likely targets:
   - Workflow: `list_workflow_steps_kandev` on the task's workflow — step
     prompts, events, `auto_advance_requires_signal`.
   - Skills: the skills the conversation invoked plus ones that should have
     fired but did not; the repo files under `.agents/skills/`
     (`.claude/skills` links here) and the AGENTS.md / CLAUDE.md guidance.

## Phase 2: Findings list (stop and wait)

Output one table row per feedback item: feedback (verbatim, trimmed) → root
cause (which constraint was missing) → target (step id + field, or file path)
→ proposed change → verification. Order by impact; mark rows you are not sure
about as uncertain. STOP and wait for confirmation. The user may cut or
rewrite rows; only confirmed rows move to phase 3.

## Phase 3: Apply

- Workspace guard: skill and doc edits require the kandev repo checkout. In
  any other workspace (a task worktree, another repo), emit file changes as a
  checklist instead of editing. Workflow API changes are not workspace-bound
  and apply normally.
- Workflow: MCP (`update_workflow_step_kandev`) or REST
  (`PUT /api/v1/workflow/steps/<id>`). Never write SQLite directly. If
  `auto_advance_requires_signal` changes, rewrite the step prompt's legacy
  auto-advance wording in the same update — the debug-kandev skill documents
  why the gate and the wording must move together.
- Skills and docs: edit files in place following the harness-improvement
  skill. Each edit traces to a confirmed row. No drive-by refactoring.

## Report

Changed step ids with the fields touched, changed files, and confirmed rows
intentionally left unapplied with the reason.
