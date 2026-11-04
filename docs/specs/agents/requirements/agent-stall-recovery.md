---
status: active
system: agents
created: 2026-07-29
updated: 2026-11-04
owners:
  - Kandev
---
# Agent Stall Recovery Requirements

## Overview

An agent turn can remain `RUNNING` after it stops emitting events. Silence is
ambiguous because a legitimate long-running tool may also be quiet, but some
agents know their provider stream failed and never close the ACP prompt.
Kandev must distinguish trustworthy terminal evidence from mere inactivity so
a failed turn is not presented as healthy work.

## Terminology

- **Turn event:** An agent frame proving the model is working on the current
  prompt: assistant text, reasoning, a tool call or update, a plan update, or
  a permission request.
- **Metadata frame:** Any other agent frame, such as a usage, context-window,
  available-commands, or session-info update; a booting or hung adapter can
  emit one with no turn event.
- **Never-started prompt:** A dispatched prompt with no observed turn event
  since dispatch.

## Requirements

### REQ-AGENTS-AGENT-STALL-RECOVERY-001: Agent Stall Recovery

**Intent:** An agent turn can remain `RUNNING` after it stops emitting events.
Silence is ambiguous because a legitimate long-running tool may also be quiet,
but some agents know their provider stream failed and never close the ACP
prompt. Kandev must distinguish trustworthy terminal evidence from mere
inactivity so a failed turn is not presented as healthy work.

#### Acceptance criteria

- **AC-AGENTS-AGENT-STALL-RECOVERY-001.1:** Kandev checks running prompts for inactivity once per 60 seconds.
- **AC-AGENTS-AGENT-STALL-RECOVERY-001.2:** After five minutes without agent events, Kandev creates at most one user-visible notice for that prompt generation.
- **AC-AGENTS-AGENT-STALL-RECOVERY-001.3:** The notice says Kandev is still waiting and includes the active top-level tool's display title or name when available. It does not assert that the tool failed and does not include raw command arguments.
- **AC-AGENTS-AGENT-STALL-RECOVERY-001.4:** The notice is a single compact inline row with muted neutral copy and a neutral **Cancel turn** action. It has no warning/error colors, alert icon, tinted background, or alert-card treatment.
- **AC-AGENTS-AGENT-STALL-RECOVERY-001.5:** On phones, **Cancel turn** remains inline and content-width rather than becoming a full-width row, while retaining a minimum 44px touch height. Activating it uses the existing `agent.cancel` request.
- **AC-AGENTS-AGENT-STALL-RECOVERY-001.6:** The notice remains visible and actionable while the affected prompt's `turn_id` is the active turn in a `RUNNING` session and no subsequent agent turn activity has been observed, including after a page reload. It is hidden when that turn resumes producing agent content, reasoning, tool calls or updates, plan updates, or permission requests, when it settles, or when a later turn becomes active. Queued user input and system status messages do not resolve an advisory notice.
- **AC-AGENTS-AGENT-STALL-RECOVERY-001.7:** Detection after genuine agent activity does not change task state, session state, prompt admission, or process liveness. A current five-minute snapshot with no genuine event since prompt dispatch is a never-started stall handled as a recoverable turn failure, not a launch failure.
- **AC-AGENTS-AGENT-STALL-RECOVERY-001.8:** The backend logs the first stall detected for a prompt generation and does not emit another notice or log entry on every watchdog check.
- **AC-AGENTS-AGENT-STALL-RECOVERY-001.9:** When an execution emits only metadata frames after a prompt is dispatched, the system shall not extend the five-minute inactivity threshold; only a turn event or newly delivered user input for the current prompt shall restart it.
- **AC-AGENTS-AGENT-STALL-RECOVERY-001.10:** A never-started classification shall stop the agent execution, record the outcome even when the stop fails, and decide the final session, turn, and task writes from durable rows because the stop removes the live execution.
- **AC-AGENTS-AGENT-STALL-RECOVERY-001.11:** When the never-started settlement succeeds, the session settles in `WAITING_FOR_INPUT` with a stall error naming the provider stall and the watchdog stop, the captured turn is completed or error-terminated, and the task does not enter `FAILED`; blocked evidence keeps `FAILED` with the uncertain submission and its open block.
- **AC-AGENTS-AGENT-STALL-RECOVERY-001.12:** A never-started stall persists one notice for the prompt generation under a deterministic message identity: it states the no-output duration and that the message was not delivered, offers resume or re-send when settled, and states the unsettled outcome when blocked.
- **AC-AGENTS-AGENT-STALL-RECOVERY-001.13:** After a successful settlement, Kandev attempts at most one automatic resume per stall episode through the existing auto-resume path: no historical prompt is dispatched, the archived, deferred-launch, auto-start, Office, automation, and passthrough guards apply, and a failed attempt is not retried.
- **AC-AGENTS-AGENT-STALL-RECOVERY-001.14:** A stall that observed at least one turn event since dispatch keeps the existing advisory notice and **Cancel turn** action, with no settlement, session or task state change, or automatic resume.
- **AC-AGENTS-AGENT-STALL-RECOVERY-001.15:** The recoverable outcome and automatic resume apply only to interactive, non-passthrough, non-Office durable-delivery sessions; others keep the existing stall handling.

## Migrated source detail

Decisions:

- [ADR-2026-07-29-agent-stall-user-controlled-recovery](../../../decisions/2026-07-29-agent-stall-user-controlled-recovery.md)
- [ADR-2026-08-02-agent-terminal-diagnostics-over-stderr](../../../decisions/2026-08-02-agent-terminal-diagnostics-over-stderr.md)
- [ADR-2026-08-07-allowlisted-provider-action-links](../../../decisions/2026-08-07-allowlisted-provider-action-links.md)
- [ADR-2026-08-08-provider-neutral-agent-error-recovery](../../../decisions/2026-08-08-provider-neutral-agent-error-recovery.md)
- [ADR-2026-08-18-never-started-agent-stall-terminal](../../../decisions/2026-08-18-never-started-agent-stall-terminal.md)
- [ADR-2026-09-02-terminal-stall-owns-process-teardown](../../../decisions/2026-09-02-terminal-stall-owns-process-teardown.md)
- [ADR-2026-11-04-never-started-stall-recovery](../../../decisions/2026-11-04-never-started-stall-recovery.md)

Implementation plans:

- [Agent stall recovery](../../../plans/agent-stall-recovery/plan.md)
- [Never-started stall teardown](../../../plans/never-started-stall-teardown/plan.md)
- [Stale stall notice](../../../plans/stale-stall-notice/plan.md)
- [Lost turn-completion cancel recovery](../../../plans/lost-turn-completion-cancel-recovery/plan.md)
- [OpenCode terminal error surfacing](../../../plans/opencode-terminal-error-surfacing/plan.md)
- [OpenCode actionable error links](../../../plans/opencode-actionable-error-links/plan.md)

## What

### Advisory inactivity recovery

The observable rules are the acceptance criteria of
REQ-AGENTS-AGENT-STALL-RECOVERY-001. The clock measures time since the last
turn event or user input, so a metadata-only adapter cannot hold a
never-started prompt open forever. A stall observed after
genuine turn activity stays advisory and leaves the turn and process running.
A never-started prompt stops the agent execution and settles its durable
submission: settled evidence leaves the session `WAITING_FOR_INPUT` with a
notice that the message was not delivered; blocked evidence keeps the `FAILED`
outcome, the uncertain submission, and its open block.

### Explicit completion signal recovery

- An accepted `step_complete_kandev` signal is stronger evidence than
  inactivity alone: it records that the agent requested the current step
  transition.
- If the signal is at least ten minutes old and the session is still `RUNNING`
  or `STARTING`, Kandev checks current prompt activity before reclaiming the
  session; Office and passthrough sessions are excluded.
- The watchdog cancels the captured execution, closes only the captured turn,
  moves the session to `WAITING_FOR_INPUT`, and applies the workflow transition
  through the normal signal reconciler.
- The watchdog validates the captured execution ID, prompt generation, and
  activity epoch; it never cancels a replacement execution or closes a
  successor turn.
- If cancellation finds no execution, Kandev may continue because no live
  process remains; other lifecycle or storage errors stop the reclaim and
  leave the durable signal for a later retry.
- A failed reconciliation does not lose the signal: later watchdog ticks retry
  a `WAITING_FOR_INPUT` session while the matching signal remains.

With the never-started classification, these are the two automatic
state-changing exceptions to the advisory inactivity rule; silence after a
turn event, without an accepted signal, remains advisory.

### Correlated terminal diagnostics

- When an agent emits a terminal provider diagnostic outside ACP, Kandev acts
  on it only when the diagnostic identifies the current agent type, active
  provider session, and active foreground prompt.
- OpenCode diagnostics come from the managed `opencode acp` process's
  error-only stderr stream; Kandev never reads OpenCode's private files under
  `~/.local/share/opencode/log`.
- A correlated OpenCode `stream error` ends the affected prompt through the
  existing agent-error lifecycle instead of waiting for the inactivity
  threshold; the session stops presenting itself as `RUNNING` and retains its
  recovery actions.
- Kandev preserves only allowlisted diagnostic fields: source agent, provider,
  model, sanitized message, occurrence time, and a derived reset time; raw log
  lines, workspace URLs, account or workspace identifiers, paths, credentials,
  and unrelated stderr never reach storage or the browser.
- A recognized OpenCode usage-limit message is classified as `quota_limited`:
  the recovery surface names the affected model when known, explains when
  capacity resets, and keeps sanitized details collapsed.
- Kanban treats `quota_limited` as user-actionable; a reset time never
  schedules a Kanban retry. Office may consume the same classification for
  provider fallback and scheduler recovery under its separate routing policy.
- The provider-limit copy is localized; desktop and phone expose the same
  failure explanation, details, and recovery actions.
- A diagnostic for a background or title request, another OpenCode session, an
  earlier prompt generation, or an already-settled prompt cannot fail the
  active turn.
- An unrecognized, malformed, or unavailable diagnostic stream is not proof of
  failure; the prompt continues with advisory inactivity recovery available.

### Allowlisted actionable diagnostics

- A validated OpenCode remediation URL may cross the agentctl boundary as a
  separate `remediation_url` field: never appended to the sanitized provider
  message and never sourced from an arbitrary raw error string.
- The only accepted shape is HTTPS on the exact `opencode.ai` host with path
  `/workspace/<safe-workspace-id>/go`, no userinfo, query, or fragment; the
  workspace identifier and complete URL are bounded before retention.
- The field comes from a structured ACP error field when OpenCode supplies one,
  or from a correlated structured stderr record; otherwise the short error is
  kept and no URL is reconstructed from private files.
- Kanban recovery cards, the persisted last-agent-error notice, and Office's
  failed-session entry expose the same destination as a localized, keyboard-
  and touch-accessible external link; the sanitized message and collapsed
  technical details are unchanged.
- A missing, malformed, untrusted, or unsupported URL produces no link and no
  URL-bearing fallback text; existing recovery actions and generic error copy
  remain available.

## Diagnostic contract

The internal normalized provider diagnostic contains only:

| Field | Meaning |
| --- | --- |
| `source` | Stable diagnostic source identifier, initially `opencode_stderr` |
| `provider_id` | Provider identifier reported by the agent, when present |
| `model_id` | Model identifier reported by the agent, when present |
| `message` | Sanitized provider message with URLs and identifiers removed |
| `occurred_at` | Parsed diagnostic timestamp |
| `reset_at` | Relative reset duration added to `occurred_at`; absolute reset timestamps are not accepted |
| `remediation_url` | Optional, exact-host OpenCode action URL accepted by the adapter-specific validator |

The persisted recovery metadata uses
`failure_kind: provider_quota_limited` plus the safe model and reset fields to
select localized presentation; `error_output` may carry the same bounded,
sanitized message for the collapsed technical-details surface.

## Failure modes

- If active tool identity is unavailable, the generic inactivity notice and
  cancel action still appear.
- If the agent acknowledges cancellation, normal cancellation reconciliation
  makes the session input-ready.
- If the agent never acknowledges cancellation, the bounded cancel-escalation
  path releases the prompt and reconciles the session.
- Cancel escalation clears every prompt-admission barrier for the cancelled
  turn, including a pending dispatch-only completion; a later prompt can
  dispatch without a backend restart.
- If notice-message persistence fails, the failure is logged without changing
  or terminating the running session.
- If OpenCode lacks error-only stderr, changes its log format, or emits an
  invalid line, Kandev ignores it and keeps normal ACP plus inactivity
  behavior.
- If a validated terminal diagnostic races with an ACP prompt response, only
  one terminal event settles the prompt generation.
- If sanitization removes all usable provider text, Kandev surfaces a generic
  OpenCode provider failure without exposing the raw diagnostic.
- If an explicit completion signal remains after a reclaim, the session stays
  input-ready and the watchdog retries reconciliation on a later tick.

## Persistence guarantees

- Advisory stall notices retain their existing persisted-message behavior;
  later same-turn agent activity hides them, including updates to earlier tool
  rows.
- Sanitized provider failures use the existing session error and recovery
  message persistence and survive reloads like other recoverable failures.
- A validated `remediation_url` is persisted only in structured recovery
  metadata, staying available after reloads and through the Office
  `session.state_changed` snapshot; it never appears in generic error text or
  raw stderr.
- Raw agent stderr remains a bounded, executor-local in-memory diagnostic and
  is not added to session metadata, messages, debug exports, or durable logs.

## Scenarios

- **GIVEN** a running prompt, **WHEN** no agent event arrives for five minutes,
  **THEN** one neutral notice names the active top-level tool when known and
  offers **Cancel turn** while the session stays `RUNNING`.
- **GIVEN** a notice already exists for the active prompt generation,
  **WHEN** later watchdog checks observe the same stall, **THEN** no duplicate
  notice or repeated stall log is emitted.
- **GIVEN** a persisted notice belongs to an earlier prompt generation,
  **WHEN** a later prompt is `RUNNING`, **THEN** the earlier notice is not
  rendered and a delayed stall event creates no new notice for it.
- **GIVEN** a visible stall notice, **WHEN** the user activates **Cancel
  turn**, **THEN** cancellation settles the turn and the session accepts new
  input without a backend restart.
- **GIVEN** a dispatch-only prompt never reports completion and cancellation
  escalates, **WHEN** the user sends a later prompt, **THEN** Kandev dispatches
  it without a backend restart.
- **GIVEN** a visible stall notice on a phone, **WHEN** the user taps **Cancel
  turn**, **THEN** the same outcome is reachable through an inline,
  content-width touch target of at least 44px.
- **GIVEN** no genuine agent event since dispatch, **WHEN** the settlement
  succeeds, **THEN** the execution is stopped and the session settles in
  `WAITING_FOR_INPUT` with a stall error and no running-only cancel action.
- **GIVEN** a prompt emits only usage, context-window, or available-commands
  frames, **WHEN** five minutes pass, **THEN** the watchdog still reports never
  started instead of restarting its clock on each metadata frame.
- **GIVEN** the never-started classification is applied, **WHEN** the
  settlement completes, **THEN** no agent process remains and a later stop
  request for that task finds none.
- **GIVEN** a quiet but legitimate long-running turn, **WHEN** the inactivity
  threshold passes and the user does not cancel, **THEN** Kandev leaves the
  turn and process running.
- **GIVEN** a current-step completion signal is at least ten minutes old and
  the tracked prompt also inactive for ten minutes, **WHEN** the session is
  still `RUNNING` or `STARTING`, **THEN** the watchdog settles the captured
  turn, moves the session to `WAITING_FOR_INPUT`, and applies the signal
  through the workflow engine.
- **GIVEN** a signalled session moved to `WAITING_FOR_INPUT` but a lookup
  fails, **WHEN** a later tick runs with the signal pending, **THEN**
  reconciliation retries without a second turn completion.
- **GIVEN** a successor execution or turn appears while the watchdog cancels
  the captured one, **WHEN** the watchdog resumes, **THEN** the successor is
  untouched and the signal stays pending.
- **GIVEN** the active OpenCode prompt emits a `stream error` for its own ACP
  session, **WHEN** the diagnostic says the model's five-hour usage limit was
  reached, **THEN** Kandev ends the running state and shows a localized
  quota-limit recovery message with the model and reset time.
- **GIVEN** quota-limit recovery is shown, **WHEN** the user expands technical
  details, **THEN** the sanitized provider message is visible with no OpenCode
  workspace URL or identifier.
- **GIVEN** OpenCode emits a title-generation error or an error for another
  session, **WHEN** a foreground prompt is active, **THEN** Kandev ignores that
  diagnostic and does not settle the foreground prompt.
- **GIVEN** a correlated diagnostic and the ACP response arrive concurrently,
  **WHEN** the turn settles, **THEN** exactly one terminal outcome is emitted
  for that prompt generation.
- **GIVEN** an OpenCode build without usable diagnostic stderr, **WHEN** its
  ACP prompt hangs, **THEN** the five-minute advisory notice remains the
  recovery path.
- **GIVEN** a correlated OpenCode diagnostic contains a valid remediation URL,
  **WHEN** the recovery surface settles, **THEN** Kanban and Office show a
  localized external link; the sanitized message contains no URL or
  workspace identifier.
- **GIVEN** a URL with the wrong host, scheme, path, query, fragment, or
  workspace identifier, **WHEN** it crosses the adapter boundary, **THEN**
  Kandev drops it and shows the generic recovery copy.
- **GIVEN** OpenCode 1.18.5 returns only its current short ACP failure,
  **WHEN** Kandev handles the error, **THEN** it shows the short failure
  without pretending the TUI-only URL was received.
- **GIVEN** provider-limit recovery renders on a phone, **WHEN** the user reads
  details or selects a recovery action, **THEN** the outcome is reachable
  through touch targets of at least 44px with no horizontal overflow.

## Out of scope

- Timing out, cancelling, or killing a turn that already produced a turn
  event, based only on inactivity.
- Making the stop path reach an execution for a task; that contract belongs
  to [task stop reachability](../../tasks/requirements/task-stop-reachability.md).
- Making the inactivity threshold user-configurable.
- Reading, tailing, or exposing an agent vendor's private log files.
- Treating arbitrary stderr text as trusted terminal evidence.
- Persisting raw stderr or raw OpenCode log records.
- Following or exposing arbitrary provider URLs, account URLs, or balance URLs;
  only the exact allowlisted OpenCode remediation route is in scope.
- Recovering a remediation URL that was never present in ACP or managed
  structured stderr.
- Automatically purchasing capacity, changing models, or scheduling a retry at
  the reset time.
- Repairing OpenCode's ACP implementation upstream; OpenCode should still
  return the provider failure directly from `session/prompt`.
