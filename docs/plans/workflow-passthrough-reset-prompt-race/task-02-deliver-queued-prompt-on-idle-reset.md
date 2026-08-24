# Task 02: Deliver the queued prompt on an idle (WAITING_FOR_INPUT) reset

Status: `done` · Wave: 1 · `parallelism: sequential`

Implemented: `handleAgentReady`'s early state guard now routes a
`WAITING_FOR_INPUT` passthrough `agent.ready` (the freshly-restarted CLI's first
idle — a boot signal, not a turn end) to a new `deliverQueuedPassthroughPrompt`
helper, which reserves and delivers the queued workflow auto-start prompt
directly without turn-completion bookkeeping. Regression test:
`TestPassthroughResetAutoStart_DeliversQueuedPromptForWaitingSession`.

Follow-up to task-01. The prompt is queued, but for a `WAITING_FOR_INPUT`
passthrough session the queued prompt is never drained: `handleAgentReady`
drops the restarted CLI's first `agent.ready` because the session is not
`RUNNING`/`STARTING`.

## Goal

Make the deferred auto-start prompt drain on the restarted CLI's first
readiness signal regardless of the session's state at step entry — both
`RUNNING` (natural transition) and `WAITING_FOR_INPUT` (idle manual move). The
restarted CLI's first idle is a *boot* signal, not a *turn end*, and must not be
gated on `RUNNING`/`STARTING`.

## Confirmed root cause

`handleAgentReady` (`event_handlers_agent.go:478`) early-returns when
`session.State` is not `RUNNING`/`STARTING`. After a manual move of an idle
task into a reset + auto-start step, the session is `WAITING_FOR_INPUT`; the
fresh CLI's first idle arrives as `agent.ready` and is silently dropped, so the
queued prompt never reaches the CLI and the step stalls.

`handleAgentBootReady` (`event_handlers_agent.go:273`) is the correct drain path
(no `RUNNING` guard; it drains queued prompts for `WAITING_FOR_INPUT` sessions),
but the passthrough restart path (`restartPassthroughProcess`,
`manager_passthrough.go:867`) publishes only `AgentContextReset`, never a
boot-ready, so the fresh CLI's idle is reported as turn-end `agent.ready`.

## Files

- `apps/backend/internal/orchestrator/event_handlers_agent.go` — the
  `handleAgentReady` state guard (~478) and/or the passthrough delivery branch
  (~699); `handleAgentBootReady` (~273).
- `apps/backend/internal/orchestrator/event_handlers_workflow.go` — only if the
  chosen mechanism needs `markIdleAfterReset` (~3468) or the `processOnEnter`
  reset+queue branch (~2266) to arm the drain.
- `apps/backend/internal/agent/runtime/lifecycle/manager_passthrough.go` — only
  if the fix publishes a boot-ready-equivalent from the restart path (see
  Design point).
- `apps/backend/internal/orchestrator/event_handlers_passthrough_reset_race_test.go`
  — extend with the `WAITING_FOR_INPUT` delivery regression.

## Steps

1. Write the regression test (see below). Run it and confirm it **fails** for
   the expected reason: the prompt is queued but never delivered after the
   fresh CLI's readiness signal.
2. Implement the minimal fix (see Design point).
3. Run the targeted test and the package test (see Validation).

## Design point

The restarted CLI's first idle must route through boot semantics, not turn-end
semantics. Two candidate mechanisms; prefer the first:

1. **Boot-ready routing (preferred, matches the ACP precedent).** Have the
   passthrough restart path mark the fresh CLI's first idle as a boot signal
   (e.g. publish an `AgentBootReady`-equivalent from `restartPassthroughProcess`,
   or have the first `handlePassthroughTurnComplete` after a reset emit boot
   semantics), so the queue drains via `handleAgentBootReady`. This also
   resolves the non-signal-gated `move_to_next` premature-advance symptom noted
   in the plan. Touches `manager_passthrough.go` and possibly event routing.
2. **Ready-handler special-case (smaller, orchestrator-only).** In
   `handleAgentReady`, when a passthrough session is `WAITING_FOR_INPUT` and
   holds a reset-deferred workflow prompt, deliver it via the existing
   passthrough branch without the turn-completion bookkeeping (there is no turn
   to complete on a fresh boot). Must not regress the non-signal-gated
   premature-advance symptom; if it leaves that symptom in place, record it as a
   residual in `plan.md`.

## Regression test

- **GIVEN** a `WAITING_FOR_INPUT` passthrough session entering a step whose
  `on_enter` is `[reset_agent_context, auto_start_agent]`, **WHEN** the fresh
  CLI's readiness signal fires, **THEN** the queued prompt is delivered to stdin
  exactly once (and not before the signal).
- Keep the existing `RUNNING`-seed assertions green (prompt queued, not written
  inline; non-reset path still writes inline).

The existing harness seeds `RUNNING` (`seedSession`). Add a `WAITING_FOR_INPUT`
seed (either a seed variant or set the session state after seeding) and drive
the fresh CLI's readiness via the same trigger the lifecycle manager emits
(`handleAgentReady` / the boot-ready path, whichever the fix routes through).

## Acceptance criteria

- The regression test passes and asserts delivery-on-readiness for a
  `WAITING_FOR_INPUT` session, exactly once.
- `TestPassthroughResetAutoStart_QueuesPromptInsteadOfInlineWrite` and
  `TestPassthroughAutoStartNoReset_WritesInline` still pass (no regression to
  the inline path or the non-reset inline write).
- No new hardcoded user-facing copy (backend logs stay English).

## Validation

```bash
cd apps/backend
go test ./internal/orchestrator/ -run 'TestPassthroughResetAutoStart|TestPassthroughAutoStartNoReset' -count=1
go test ./internal/orchestrator/ -count=1
# if the fix touches the lifecycle manager:
go test ./internal/agent/runtime/lifecycle/ -run 'Passthrough' -count=1
golangci-lint run ./... --new-from-rev="origin/main" --timeout=5m
```
