# Task 01: Defer passthrough auto-start prompt after context reset

Status: `done` · Wave: 1 · `parallelism: sequential`

Implemented: `processOnEnter`'s passthrough + auto-start branch now queues the
prompt (via `queueAutoStartPrompt`) when the step also carries
`reset_agent_context`, instead of writing inline. The fresh CLI's first idle
`agent.ready` delivers it through `handleAgentReady`'s passthrough branch
(`deliverPassthroughPrompt`). Regression tests: `TestPassthroughResetAutoStart_QueuesPromptInsteadOfInlineWrite`,
`TestPassthroughAutoStartNoReset_WritesInline`; E2E case
`passthrough_auto_start_via_stdin` updated to `ExpectQueued: true`.

## Goal

Stop a passthrough workflow step that combines `reset_agent_context` and
`auto_start_agent` from writing its auto-start prompt into a still-booting CLI
process. The prompt must be delivered exactly once, and only after the restarted
CLI signals it is promptable.

## Files

- `apps/backend/internal/orchestrator/event_handlers_workflow.go` — the
  `processOnEnter` passthrough + auto-start branch (currently line ~2266) and,
  if needed, `markIdleAfterReset` (~3452) and the `queueAutoStartPrompt*` /
  `deliverPassthroughPrompt` helpers.
- `apps/backend/internal/agent/runtime/lifecycle/manager_passthrough.go` — only
  if the readiness drain requires the restart path to publish a boot-ready
  signal (see "Design point" below).
- `apps/backend/internal/orchestrator/event_handlers_passthrough_reset_race_test.go`
  — new regression test.

## Steps

1. Write the regression test (see "Regression test" below). Run it and confirm
   it **fails** for the expected reason: the prompt is written inline before
   readiness.
2. Implement the minimal fix:
   - When `reset_agent_context` succeeded for a passthrough session that also
     has `auto_start_agent`, do not call `autoStartPassthroughPrompt` inline.
     Queue the prompt via the existing `queueAutoStartPrompt*` family so the
     fresh CLI's readiness drains it.
   - Ensure `markIdleAfterReset` leaves the session in a state the queue drain
     path can act on (or arm the drain explicitly) rather than returning early
     and letting the inline write proceed.
   - Keep the non-reset inline path and the `CREATED`-skip path unchanged.
3. Run the targeted test and the package test (see "Validation").

## Design point

Keying the deferred delivery off the restarted CLI's existing first
`agent.ready`/idle signal vs. publishing an explicit boot-ready from
`restartPassthroughProcess`. Prefer the smallest change that satisfies the spec;
do not add new detection machinery. If the queued-prompt drain already runs on
`agent.ready` for passthrough, queueing alone may suffice.

## Regression test

- **GIVEN** a `RUNNING` passthrough session entering a step whose `on_enter` is
  `[reset_agent_context, auto_start_agent]`, **THEN** `passthroughStdinCalls` is
  empty immediately after the transition, and after the fresh CLI's readiness
  signal the prompt is delivered exactly once.
- **GIVEN** a passthrough step with `auto_start_agent` and no reset, **THEN** the
  prompt is written inline (unchanged).

## Acceptance criteria

- The regression test passes and asserts both scenarios.
- The non-reset passthrough auto-start path is byte-for-byte behaviorally
  unchanged (no regression to `#669`).
- No new hardcoded user-facing copy (backend logs/diagnostics stay English;
  any UI copy would go through `t()`, but none is expected here).

## Validation

```bash
cd apps/backend
go test ./internal/orchestrator/ -run 'TestPassthroughResetAutoStart' -count=1
go test ./internal/orchestrator/ -count=1
golangci-lint run ./... --new-from-rev="origin/main" --timeout=5m
```
