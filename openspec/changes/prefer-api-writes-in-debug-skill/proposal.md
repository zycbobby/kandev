# Proposal: prefer-api-writes-in-debug-skill

## Why

`/debug-kandev` skill 教 agent 用 `sqlite3` 直查运行时数据，但只在 signal-gating 修复一处提示走 REST，没有全局写策略；端口硬编码 `127.0.0.1:38429`、「REST 无鉴权（本机）」在 auth 开启时不成立、也没有「写需求 → API」映射。agent 遇到新的修改需求时会退回直接 `UPDATE` SQLite，绕过事件发布（WS UI 不刷新）、orchestrator 编译态 step 缓存失效与队列 admission 重评估，制造「改了 DB 但运行时没变」的新故障。本次把 skill 的数据修改策略升级为 API-first，并把「确实没有 API」的边界显式化。

## What Changes

**写策略全局化**
- From: `SKILL.md` 仅在 signal-gating 修复一处写「改法用 REST API，不要直接写 SQLite」，其余章节无写指引；新写需求无规则可循。
- To: 新增全局「数据修改写策略」章节：读 DB 自由；一切修改优先走 API（REST 或 MCP）；直接写 DB 仅限两种显式例外（无写 API 的审计表、后端未运行时的修复）。
- Reason: 直接写 DB 绕过 `workflow_step.*`/`task.*` 事件 fanout、orchestrator step 缓存失效（仅 5s TTL 兜底）与队列 admission 重评估。
- Impact: non-breaking；仅改 skill 文档，无生产代码变更。

**端口与实例发现**
- From: REST 示例硬编码 `http://127.0.0.1:38429`。
- To: 优先用 `scripts/kandev-instances` 取真实 `BACKEND_PORT`（同时覆盖 systemd 与 dev 实例；顺带校验 `HOME_DIR`），失败再回退 `server.port` 默认值 38429。
- Reason: `server.port` 可被 `KANDEV_SERVER_PORT` 等覆盖（`internal/common/config/catalog.go:46`）；dev-isolated 实例用随机端口。
- Impact: non-breaking。

**鉴权描述修正**
- From: 「REST 无鉴权（本机）」。
- To: 注明 auth 为 opt-in runtime flag，默认关；开启后 curl 需带 `Authorization: Bearer $KANDEV_API_TOKEN`（PAT）。
- Reason: 现描述在 auth 开启的安装上误导 agent 直接裸调导致 401。
- Impact: non-breaking。

**常见写需求 → API 映射表（新增章节）**
- 列出 skill 数据地图内实体的写场景与对应面：workflow step 修改/创建/删除/重排（REST `PUT/POST/DELETE /api/v1/workflow/steps`、`PUT .../reorder`，或 MCP `update_workflow_step_kandev` 等）、task move/state/archive/delete（REST `POST /tasks/:id/move` 等，或 MCP `move_task_kandev` / `update_task_state_kandev` / `archive_task_kandev`）、task plan CRUD（MCP `*_task_plan_kandev`）、session stop/message/spawn（MCP）。
- 注明 REST 与 MCP 共享同一 service 层与事件 publisher（有 parity 测试），任选其一等价安全。
- 注明映射表未覆盖的写需求应先 grep 仓库路由注册（`internal/*/handlers`）或 MCP 工具名，找不到再考虑例外路径。

**与 debug skill 交叉引用**
- 引用 `debug` skill 的 `references/instance.md` 纪律：live 实例「Never mutate」、有副作用的验证用 `scripts/dev-isolated` 起隔离实例、证据收集走 diagnostic bundle。
- Reason: 两个 skill 已有互补分工，避免 debug-kandev 重复发明或与之矛盾。
- Impact: non-breaking；仅添加引用，不改 `debug` skill。

## Capabilities

### New Capabilities

- `runtime-debugging`: 本地 Kandev 运行时排查能力——数据根目录定位、SQLite 只读查询、工作流推进追踪，以及本次新增的数据修改写策略（API-first + 显式例外 + API 映射）。

### Modified Capabilities

（无：`openspec/specs/` 目前不存在，本 change 是首个 capability spec。）

## Research Log

### 未知 1：直接写 DB 实际绕过什么
- **验证方式**：读代码追事件订阅链（orchestrator / gateway / backend CLAUDE.md invariant）。
- **结论（试通证据）**：绕过三层下游——WS 事件 fanout（`internal/gateway/websocket/task_notifications.go:38`，UI reload 前不刷新）、orchestrator 编译态 step 缓存失效（`internal/orchestrator/event_handlers_workflow_step_cache.go:12-17`，仅 5s TTL 兜底，`workflow_step_cache.go:21`）、队列 admission 重评估（`internal/orchestrator/event_handlers_workflow_queue.go:19`）。
- **证据摘要**：上述文件行号；backend CLAUDE.md「Task lifecycle events」与「Workflow steps follow the same rule」invariant：mutation 必须经 service 层发布事件。

### 未知 2：写 API 面是否完整覆盖 skill 的写需求
- **验证方式**：grep 路由注册 + 枚举 MCP 工具名。
- **结论（试通证据）**：覆盖完整。workflow steps REST CRUD/reorder/import-export（`internal/workflow/handlers/handlers.go:63-79`；`UpdateStepRequest` 含 `prompt`/`events`/`auto_advance_requires_signal` 等，`controller.go:171-188`）；tasks REST `POST /tasks`、`PATCH /tasks/:id`、`POST /tasks/:id/move`、`DELETE`、`archive/unarchive`、`bulk-move`（`internal/task/handlers/task_handlers.go:157-166,180`）；MCP 71 个工具含 `update_workflow_step_kandev`、`move_task_kandev`、`update_task_state_kandev`、`archive_task_kandev`、task plan CRUD、`stop/message/spawn_session_kandev`。REST 与 MCP 共享 `stepevents.Publisher`，有 parity 测试（`internal/mcp/handlers/workflow_step_parity_test.go`）。
- **证据摘要**：grep 退出码 0 及上述行号；MCP 工具名枚举自 `internal/mcp/`。

### 未知 3：「确实没有 API」的边界
- **验证方式**：对审计表 grep HTTP handlers 与 MCP handlers 的写面。
- **结论（试通证据）**：边界极小——`task_step_transitions` / `session_step_history` 为 engine 追加的审计表，无 HTTP 写面、无 MCP 写面（两次 grep 均空），且本就不应编辑；另「后端未运行」时 API 不存在，属操作性例外。
- **证据摘要**：`grep -rn "task_step_transitions|session_step_history" internal/{task,workflow}/handlers/` 与 `internal/mcp/handlers/` 写面过滤均为空。

### 未知 4：端口与鉴权的事实基准
- **验证方式**：读配置目录、auth 中间件与既有脚本范式。
- **结论（试通证据）**：端口 = `server.port`（默认 38429，env `KANDEV_SERVER_PORT` 等，`internal/common/config/catalog.go:46`）；实例发现用 `scripts/kandev-instances`（输出 `PID BACKEND_PORT WEB_PORT AGENTCTL_PORT HOME_DIR REPO_PATH`，`debug` skill `references/instance.md:9-13`）。auth opt-in 默认关；开启后 PAT bearer（`internal/auth/httpmw/middleware.go:280-281`；`scripts/kandev-logs:63-64` 的 `Authorization: Bearer ${KANDEV_API_TOKEN}` 是现成范式）。
- **证据摘要**：上述文件行号。

## Impact

- **代码（skill 文档，非生产代码）**：`.agents/skills/debug-kandev/SKILL.md`（主要改动：新增写策略章节、映射表、端口/鉴权修正、交叉引用）；可选在 `references/db-schema.md` 尾部加一行「写操作走 API」交叉引用。`.claude/skills` 是指向 `.agents/skills` 的 symlink（已验证），单处修改双端生效。
- **接口**：仅引用既有 REST/MCP 面，不新增、不修改任何后端接口。
- **依赖**：`scripts/kandev-instances`（仓库既有）；`curl`；auth 开启时需 `KANDEV_API_TOKEN`。
- **测试**：skill 为 markdown 文档，无单测；验收以 `openspec validate --strict` + 人工核对映射表与代码事实一致为准。
- **无 DB 变更、无 i18n 影响**（skill 内容为 agent-facing 文档，非 web UI copy）。
