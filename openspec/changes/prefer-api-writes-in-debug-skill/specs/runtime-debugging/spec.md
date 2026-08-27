# runtime-debugging

## 背景

`/debug-kandev` skill（`.agents/skills/debug-kandev/SKILL.md`）指导 agent 排查本地 Kandev 运行时：解析数据根目录、直查 SQLite、追工作流 step 推进与 ADR 0015 信号机制。当前所有查询配方都指向 `sqlite3 $KH/data/kandev.db`，而唯一的写指引（signal-gating 修复）只在局部提到 REST。缺少全局写策略时，agent 面对新写需求会直接 `UPDATE` SQLite。

直接写 DB 并非等价捷径：后端 mutation 必须经 service 层发布 `task.*` / `workflow_step.*` 事件（backend CLAUDE.md invariant），WS 网关靠这些事件刷新 UI，orchestrator 靠它们失效编译态 step 缓存（TTL 5s 仅为兜底）并重评估队列 admission。绕过 API 的直写会制造「DB 已改、运行时未变」的次生故障——恰是本 skill 要消灭的那类疑难杂症。

## 方案与决策

- **读/写分离**：读路径保留 SQLite 直查（排障时后端可能在跑也可能没跑，只读无一致性风险）；写路径强制 API-first。
- **两个 API 面 etc 价**：REST（gin 路由）与 MCP（`internal/mcp/handlers`）共享同一 service 层与 `stepevents.Publisher`（有 parity 测试），skill 不规定偏好哪个面，由调试会话可用性决定（shell 有 curl 走 REST；会话本身挂着 kandev MCP 就走 MCP 工具）。
- **例外显式化**：无写 API 的仅 engine 追加的审计表（`task_step_transitions`、`session_step_history`）；后端未运行属操作性例外，须带警告与重启后校验指引。
- **参数发现取代硬编码**：端口经 `scripts/kandev-instances` 发现（覆盖 systemd 与 dev 实例），失败回退 `server.port` 默认 38429；auth 开启时带 PAT bearer。
- **映射表保持可验证**：写场景 → 端点/工具名的映射表附仓库内出处（路由注册文件），映射未覆盖的写需求先 grep 路由再考虑例外，防止映射表变成新的过期源。

## ADDED Requirements

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

## Known Failure Modes

| Symptom | Cause | Fix |
|---------|-------|-----|
