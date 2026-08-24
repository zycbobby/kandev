# Plan: Workflow Passthrough Reset Prompt Race

Status: `implemented`

## Validation recorded

```text
go test ./internal/orchestrator/ -run 'TestPassthroughResetAutoStart_QueuesPromptInsteadOfInlineWrite' -count=1  # FAILS before the fix (1 inline stdin write), passes after
go test ./internal/orchestrator/ -run 'TestWorkflowE2E' -count=1                                                   # pass
go test ./internal/orchestrator/ -count=1                                                                          # pass
gofmt -l <changed files>                                                                                            # clean
```

Known residual (out of scope, pre-existing): for a **non**-signal-gated step
that combines reset + auto-start + `on_turn_complete: move_to_next`, the fresh
CLI's first idle `agent.ready` still routes through `handleAgentReady` (turn-end
semantics) rather than a boot-ready signal, so a premature advance can occur.
This is the boot-vs-turn ambiguity the ACP path avoids by publishing
`AgentBootReady`; the passthrough restart path does not yet publish an
equivalent. The observed regression (signal-gated `Apply` stall) is fixed.


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

`docs/specs/workflow-passthrough-reset-prompt-race/spec.md` (new repair spec).

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
  orchestrator deferral fix.

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
