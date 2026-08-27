# runtime-debugging Specification

## Purpose

规范 `/debug-kandev` skill（`.agents/skills/debug-kandev/`）指导 agent 排查本地 Kandev 运行时的方式：定位数据根目录与 SQLite 只读查询自由，但一切数据修改走后端 API（REST 或 MCP），直接写 DB 仅限两类显式例外（无写 API 的审计表、后端停机修复）。约束动机：直写绕过事件发布、orchestrator step 缓存失效与队列 admission 重评估，制造「DB 已改、运行时未变」的次生故障。
## Requirements
### Requirement: API-first data mutation

When the debug skill instructs an agent to modify Kandev runtime data (workflow steps, tasks, task plans, sessions, agent/executor profiles), it MUST direct the mutation through a backend API surface (REST or MCP) instead of writing SQLite directly, and MUST state why: direct writes bypass event publication (WS UI fanout), the orchestrator's compiled-step cache invalidation, and queue-admission re-evaluation.

#### Scenario: workflow step configuration change

- **WHEN** the agent needs to change a step's `prompt`, `events`, or `auto_advance_requires_signal`
- **THEN** the skill points to `PUT /api/v1/workflow/steps/<id>` (or the MCP `update_workflow_step_kandev` tool), not a SQLite `UPDATE`

#### Scenario: task-level change

- **WHEN** the agent needs to move a stuck task, change its state, archive, or delete it
- **THEN** the skill points to the REST endpoints (`POST /tasks/:id/move`, `PATCH /tasks/:id`, `POST /tasks/:id/archive`, `DELETE /tasks/:id`) or the MCP equivalents (`move_task_kandev`, `update_task_state_kandev`, `archive_task_kandev`, `delete_task_kandev`)

#### Scenario: task plan maintenance

- **WHEN** a debug flow needs to create, read, update, or delete a task plan
- **THEN** the skill points to the MCP task-plan tools (`create/get/update/delete_task_plan_kandev`) rather than the `task_plans` table

### Requirement: direct DB writes limited to explicit exceptions

The skill MUST permit direct SQLite writes only in two explicitly documented cases: (a) append-only audit tables that have no write API by design, and (b) data repair while the backend is not running. For case (b) the skill MUST warn that validation and event publication were bypassed and the result MUST be re-verified after the backend restarts.

#### Scenario: audit tables have no write path

- **WHEN** the target data is `task_step_transitions` or `session_step_history`
- **THEN** the skill offers no edit recipe (no API exists and the engine owns appends) and treats them as read-only evidence

#### Scenario: backend not running

- **WHEN** no API is reachable and a data repair is unavoidable
- **THEN** the skill requires confirming the backend is stopped, documents the bypass warning, and prefers restarting the backend and using the API whenever feasible

### Requirement: instance discovery over hardcoded endpoint

The skill MUST resolve the backend port at debug time (via `scripts/kandev-instances`, falling back to the `server.port` default 38429) instead of hardcoding `127.0.0.1:38429`, and MUST document that requests carry `Authorization: Bearer $KANDEV_API_TOKEN` when the auth runtime flag is enabled.

#### Scenario: port discovery

- **WHEN** the agent is about to call any REST write endpoint
- **THEN** it first resolves the live `BACKEND_PORT` (and cross-checks `HOME_DIR`) through `scripts/kandev-instances`

#### Scenario: authentication enabled

- **WHEN** the `features.auth` runtime flag is on for the target instance
- **THEN** REST calls include the PAT bearer header and the skill never claims local REST is unauthenticated

### Requirement: write-scenario to API mapping

The skill MUST include a mapping of common debug write scenarios to concrete REST endpoints and MCP tool names, grounded in the repo's route registrations, and MUST instruct that unmapped write needs are resolved by searching the route registrations (or MCP tool inventory) first — only falling back to the exception path when no surface exists.

#### Scenario: mapping covers the skill's data map

- **WHEN** the agent consults the mapping for workflow steps, tasks, task plans, or session operations
- **THEN** each listed scenario names its REST endpoint and/or MCP tool together with the repo source of truth

#### Scenario: unmapped write need

- **WHEN** a write need is not in the mapping
- **THEN** the agent searches the backend route registrations / MCP tool names before considering a direct DB write

