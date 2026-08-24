---
status: draft
created: 2026-08-24
owner: kandev
---

# Workflow Passthrough Reset Prompt Race

## Why

A workflow step whose `on_enter` combines `reset_agent_context` with
`auto_start_agent` is the documented pattern for "start this step with a fresh
agent conversation and an auto-dispatched prompt". For a **passthrough** (CLI)
agent, the context reset restarts the CLI subprocess, and the auto-start prompt
is then written straight to the fresh process's PTY stdin before that process
has finished booting. The prompt is lost, the agent idles with no work to do,
and the workflow stalls.

Observed on a local run: the reset restarted `npx @anthropic-ai/claude-code`
and the Apply-step prompt was written to stdin **~160 ms** after the new process
spawned, while the CLI took **~4.8 s** to reach its first `agent.ready`. The
prompt keystrokes landed in a booting TUI, were never consumed, and the task sat
at the step indefinitely.

## Broken behavior

`processOnEnter`'s passthrough + auto-start branch writes the step prompt
directly via `autoStartPassthroughPrompt` (→ `deliverPassthroughPrompt` →
`WritePassthroughStdin`) with no readiness gate. This is safe for the ordinary
case — the previous turn just ended and the PTY is already at its input prompt —
but not after a context reset, where the process was just restarted.

`restartPassthroughProcess` (`internal/agent/runtime/lifecycle/manager_passthrough.go`)
returns as soon as the replacement process is *spawned*, not *ready*. It
publishes `AgentContextReset` but not `AgentBootReady`, and does not wait for
the CLI's first idle signal.

`markIdleAfterReset` returns early for a passthrough session that also has
`auto_start_agent`, so no state flip or drain path is armed between the restart
and the prompt write.

This is unlike the ACP path, where `autoStartStepPrompt` calls
`queueAutoStartPromptIfRunning` and defers the prompt to the queue until
`agent.ready` / `agent.boot_ready` drains it.

## What

- A passthrough workflow step that combines `reset_agent_context` and
  `auto_start_agent` delivers its auto-start prompt **only after** the restarted
  CLI signals it is promptable (its first idle / ready signal), never into a
  still-booting process.
- The prompt is preserved across the restart: it is queued (or otherwise
  deferred) and drained by the same readiness path that delivers queued
  workflow prompts today, rather than written inline.
- The ordinary (non-reset) passthrough auto-start path is unchanged: a PTY that
  just finished its previous turn still gets its prompt written inline.
- A passthrough session that is `CREATED` (never prompted) is unchanged: no
  reset occurs, and the auto-start launch path behaves as it does today.

## Failure modes

- The restarted CLI never reaches readiness (crashes during boot, hangs before
  its first prompt): the deferred prompt stays queued and is not written to a
  dead process. Recovery follows the existing boot-ready drain / auto-resume
  path.
- The reset succeeds but the deferred prompt cannot be queued: the error is
  surfaced the same way other auto-start prompt failures are, and the session
  is left waiting for input rather than silently dropping the step.
- A signal-gated step (`auto_advance_requires_signal`): the step does not
  advance on the fresh CLI's first idle signal; it still waits for
  `step_complete_kandev`, exactly as today. Only the prompt *delivery* is
  deferred, not step advancement.

## Scenarios

- **GIVEN** a `RUNNING` passthrough session on a step whose `on_enter` has both
  `reset_agent_context` and `auto_start_agent`, **WHEN** the task transitions
  into that step, **THEN** the auto-start prompt is not written to the fresh
  process's stdin until that process reports ready, and the prompt reaches the
  agent exactly once.
- **GIVEN** a `RUNNING` passthrough session entering a step with
  `auto_start_agent` and **no** `reset_agent_context`, **WHEN** the step is
  entered, **THEN** the prompt is written inline to the still-idle PTY as
  today, with no queueing.
- **GIVEN** a passthrough session in `CREATED`, **WHEN** the task enters a step
  with `reset_agent_context` and `auto_start_agent`, **THEN** no restart occurs
  and the auto-start launch path is unchanged.
- **GIVEN** a passthrough step that combines reset and auto-start and is
  signal-gated, **WHEN** the fresh CLI reaches readiness, **THEN** the prompt is
  delivered but the step does not advance until the agent emits
  `step_complete_kandev`.

## Out of scope

- Changing the ACP reset path (`ResetAgentContext` session-level reset) or its
  `AgentBootReady` publication.
- Relaxing agentctl's running-process guard or changing restart semantics for
  non-passthrough agents.
- Making the idle-detector readiness window per-agent-configurable (already
  exists as `PassthroughConfig.IdleTimeout`; this spec does not require new
  detection machinery).
- Step advancement semantics for signal-gated steps (ADR 0015).

## Related

- `docs/specs/cli-mode-parity/spec.md` — idle-based passthrough prompt injection.
- `docs/specs/workflow-step-agent-start-ownership/spec.md` — the reset +
  auto-start combination and its other failure modes (double-start).
- `docs/decisions/0015-explicit-completion-signal-for-auto-advance.md`.
