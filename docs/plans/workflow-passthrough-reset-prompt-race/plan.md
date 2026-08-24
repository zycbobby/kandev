# Plan: Workflow Passthrough Reset Prompt Race

Status: `implemented` — task-01 (inline-write race) and task-02 (idle delivery
leg) are both done.

## Follow-up (2026-08-24): deliver the queued prompt on an idle reset

The first fix (task-01) queues the prompt but does not make it drain for the
`WAITING_FOR_INPUT` case. Confirmed on a local run (task
`Add Makefile reload for daemon updates`, session `cc78b3a3`, timestamps +0800):

- The backend restarted (~13:13:45), lazy-recovering the passthrough session as
  a `fresh-start resume` (no resume token). The session sat `WAITING_FOR_INPUT`.
- A manual `Review → Apply` move at 13:15:23 ran `reset_agent_context`
  (CLI killed, `exit 143`) then `queueAutoStartPrompt` (`message queued`,
  `content_length 593`).
- The restarted CLI reached `agent.ready` at 13:15:28 and again at 13:15:47, but
  the queued prompt was **never delivered**: no `deliverPassthroughPrompt`, no
  new `task_session_messages` row (the last user message is an earlier 295-byte
  prompt), and the session stuck `WAITING_FOR_INPUT` with the task at
  `state=REVIEW` / `workflow_step_id=Apply`.

Root cause: `handleAgentReady` drops `agent.ready` when `session.State` is not
`RUNNING`/`STARTING` (`event_handlers_agent.go:478`). A fresh CLI's first idle
after a reset is a *boot* signal, not a *turn end* — there is no turn to
complete — but it routes through turn-end semantics and is gated on that state.
The lifecycle manager already documents this (`manager_events.go:141-148`) and
works around it for the wakeup-turn case, but the passthrough reset path has no
equivalent. `handleAgentBootReady` (`event_handlers_agent.go:273`) is the correct
drain path (no `RUNNING` guard; it drains queued prompts for `WAITING_FOR_INPUT`
sessions), but `restartPassthroughProcess` publishes only `AgentContextReset`,
never a boot-ready, so the fresh CLI's idle is reported as `agent.ready`.

The existing test seeds `RUNNING` (`seedSession`), so `markIdleAfterReset` skips
the flip and the delivery gap is invisible; it also never asserts delivery.

See `task-02-deliver-queued-prompt-on-idle-reset.md`.


## Validation recorded

```text
go test ./internal/orchestrator/ -run 'TestPassthroughResetAutoStart_QueuesPromptInsteadOfInlineWrite' -count=1  # FAILS before the fix (1 inline stdin write), passes after
go test ./internal/orchestrator/ -run 'TestWorkflowE2E' -count=1                                                   # pass
go test ./internal/orchestrator/ -count=1                                                                          # pass
gofmt -l <changed files>                                                                                            # clean
```

Known residual (now tracked as task-02): the fresh CLI's first idle
`agent.ready` routes through `handleAgentReady` (turn-end semantics) rather than
a boot-ready signal. This is the boot-vs-turn ambiguity the ACP path avoids by
publishing `AgentBootReady`; the passthrough restart path does not yet publish
an equivalent. Two symptoms follow: a signal-gated step stalls because the
queued prompt is never delivered (the `WAITING_FOR_INPUT` case above), and a
non-signal-gated `move_to_next` step can advance prematurely. The original
note claimed the signal-gated `Apply` stall was fixed — that was wrong; it is
the exact failure the follow-up reproduces.


## Confirmed root cause

A workflow step whose `on_enter` combines `reset_agent_context` and
`auto_start_agent`, on a **passthrough** (CLI) session, loses its auto-start
prompt because the prompt is written inline to PTY stdin before the freshly
restarted CLI finishes booting.

The sequence (evidence from a local run, timestamps +0800):

1. `processOnEnter` runs `reset_agent_context` first. For a passthrough session
   this goes `ResetAgentContext` → `RestartAgentProcess` →
   `restartPassthroughProcess`, which kills the old CLI and spawns a fresh one.
2. `restartPassthroughProcess` returns as soon as the new process is *spawned*
   (`manager_passthrough.go:867`), publishing `AgentContextReset` but **not**
   `AgentBootReady` and not waiting for the CLI's first idle.
3. `markIdleAfterReset` (`event_handlers_workflow.go:3452`) returns early for
   `isPassthrough && has auto_start_agent`, so no state flip or drain is armed.
4. The `hasAutoStart && isPassthrough` branch (`event_handlers_workflow.go:2266`)
   calls `autoStartPassthroughPrompt` → `deliverPassthroughPrompt` →
   `WritePassthroughStdin` immediately.
5. The CLI reaches its first `agent.ready` ~4.8 s later; the ~160 ms-earlier
   prompt keystrokes landed in a booting TUI and were never consumed. The agent
   idles with nothing to do; a signal-gated step then stalls forever because
   `step_complete_kandev` is never emitted.

The ACP path already avoids this: `autoStartStepPrompt` →
`queueAutoStartPromptIfRunning` queues the prompt when the session is not
promptable, and it drains on `agent.ready` / `agent.boot_ready`. The passthrough
path has neither the queue nor the readiness gate.

## Affected spec

`docs/specs/tasks/requirements/workflow-passthrough-reset-prompt-race.md` (new repair spec).

## Fix approach

Defer the passthrough auto-start prompt after a context reset until the
restarted CLI reports readiness, reusing the existing queue + readiness-drain
machinery instead of writing inline. Two pieces:

1. **Orchestrator** — in `processOnEnter`, when the step both reset the agent
   context and auto-starts, and the session is passthrough, do not call
   `autoStartPassthroughPrompt` inline. Queue the prompt (via the same
   `queueAutoStartPrompt*` family used by the ACP path) so the fresh CLI's
   readiness signal drains it.
2. **Readiness drain** — confirm (or, if missing, add) that a freshly restarted
   passthrough process produces a signal that drains the queued prompt. The
   restarted CLI already emits `agent.ready` on first idle (observed); verify
   the queued-prompt drain runs on that signal for passthrough. If a
   boot-ready-equivalent is required to distinguish "fresh boot" from
   "turn end", publish it from the passthrough restart path.

The exact plumbing is the implementer's call, but it must satisfy the spec's
scenarios: the prompt is delivered exactly once, and only after readiness; the
non-reset inline path and the `CREATED`-skip path are unchanged.

## Regression test (red before, green after)

New test file `apps/backend/internal/orchestrator/event_handlers_passthrough_reset_race_test.go`.

Using the existing harness (`setupTestRepo`, `seedSession`, `newMockStepGetter`,
`createTestService`, and `mockAgentManager` with `isPassthrough: true`,
`passthroughStdinCalls`, `restartProcessCalls`):

- Seed a `RUNNING` passthrough session on a step whose `on_enter` is
  `[reset_agent_context, auto_start_agent]` with a non-empty prompt.
- Drive the transition into the step.
- Assert `passthroughStdinCalls` is empty immediately after the transition
  (the prompt must **not** be written inline into the restarted process), then
  simulate the fresh CLI's readiness signal and assert the prompt is delivered
  exactly once and the session advances/returns to a promptable state.

A companion assertion keeps the non-reset path honest: a passthrough step with
`auto_start_agent` and no reset still writes inline (no regression to
`#669`-style behavior).

## Tasks

- `task-01-defer-prompt-after-passthrough-reset.md` — regression test + the
  orchestrator deferral fix (done).
- `task-02-deliver-queued-prompt-on-idle-reset.md` — make the queued prompt
  drain on the fresh CLI's first readiness for a `WAITING_FOR_INPUT` session
  (follow-up).

## Validation

```bash
cd apps/backend
go test ./internal/orchestrator/ -run 'TestPassthroughResetAutoStart' -count=1
go test ./internal/orchestrator/ -count=1
golangci-lint run ./... --new-from-rev="origin/main" --timeout=5m
```

## Waves / parallelism

Single wave, sequential. One task owns the test + fix; no shared schema,
migration, generated contract, lockfile, or package config is touched.

## Risks and out of scope

- **Readiness-signal choice** is the one open design point: keying off the
  existing `agent.ready`/idle for passthrough vs. publishing an explicit
  boot-ready from the restart path. Prefer the smallest change that satisfies
  the spec; do not add new detection machinery.
- Do not alter ACP reset behavior, agentctl's running-process guard, or
  signal-gated step advancement (ADR 0015).
- `restartPassthroughProcess` and the `CREATED`-skip path are otherwise out of
  scope except where the drain fix requires touching them.
