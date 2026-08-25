## Why

`dev-openspec-workflow` 的 Verify 步骤已启用 signal-gating（`auto_advance_requires_signal=1` + `on_turn_complete: move_to_step→Archive`），理论上「无问题即自动推进到 Archive」。但仓库级 `openspec-verify-change` 技能的 Clean 分支只提示「下一步 archive」、不显式要求发 `step_complete_kandev` 信号，与工作流 prompt 措辞不一致，导致 agent 在 Clean 场景可能只报告「无漂移」就结束 turn、不发信号，任务卡在 Verify（已有实测案例）。需要把 Verify 步骤 prompt 显式化为「Clean → 发信号自动推进 / Drift Found → 停下提问」，使「无明确问题即自动推进到 Archive」可靠。

## What Changes

**dev-openspec-workflow 的 Verify 步骤 prompt（运行时 DB）**
- From: 「完成后调用 `step_complete_kandev`… 工作流才会流转到 Archive… 但若遇到真正需要我决策的问题，应停下来向我提问」——把「完成」与「无问题」混为一谈，未显式区分 Clean 与 Drift 两种终态，也未提示 agent：技能本身只提示 archive、不发信号。
- To: 显式分流两条终态，并明示「必须由 agent 主动调 `step_complete_kandev` 才会流转」。
- Reason: 消除 prompt 与技能的措辞不一致，使「无明确问题即自动推进」可靠，避免 Clean 场景卡在 Verify。
- Impact: non-breaking；仅影响 `man` workspace 下 `dev-openspec-workflow` 的 Verify 步骤行为，不改引擎、不改其它工作流、不改仓库 skill。

新的 Verify 步骤 prompt 全文（Apply 阶段经 `PUT /api/v1/workflow/steps/c33e0ba3-14bd-4fe0-9670-230d94e5709a` 写入）：

```
使用 openspec-verify-change 技能：实现完成后检测 code↔spec 漂移。派 subagent 对照 proposal.md 与 design.md 检查，报告未实现需求、范围外新增、设计偏差。

按检测结果分流：
- 若 Drift Report 为 Clean（无明确问题）：调用 `step_complete_kandev` 工具发出完成信号（必填 summary，概括本步骤产出），工作流会自动推进到 Archive。注意 openspec-verify-change 技能本身只提示「下一步 archive」而不发信号，本步骤必须由你主动调用 `step_complete_kandev` 才会流转。
- 若 Drift Found（发现漂移问题）：停下来向用户展示 Drift Report 并提问（Out of Scope 项保留或移除、Must Fix 项返回 Apply 修正），不要发出完成信号。

不要做例行的「是否继续」确认。
```

## Capabilities

### New Capabilities

- `workflow-verify-autoadvance`: 定义 `dev-openspec-workflow` 的 Verify 步骤在「验证无漂移（Clean）」时自动推进到 Archive、在「发现漂移（Drift Found）」时停下等待人工决策的行为。

### Modified Capabilities

（无：本 change 不修改既有 capability 的 spec 语义，仅新增对 Verify 步骤推进语义的规格描述。）

## Research Log

### 未知 1：Verify→Archive 的推进机制现状
- **验证方式**：读运行时 DB（`workflow_steps`）+ 读引擎模型 `apps/backend/internal/workflow/models/models.go` + 探测 REST `GET /api/v1/workflow/steps/c33e0ba3-…`。
- **结论（试通证据）**：Verify 步骤（`c33e0ba3`）已配置 `auto_advance_requires_signal=1`、`events.on_turn_complete = move_to_step→Archive`（`8c04e5b9`）、`events.on_enter = auto_start_agent + reset_agent_context`；即「agent 调 `step_complete_kandev` → 引擎执行 on_turn_complete → 推进到 Archive」。
- **证据摘要**：`GET /api/v1/workflow/steps/c33e0ba3-14bd-4fe0-9670-230d94e5709a` 返回 `{"auto_advance_requires_signal":true,"on_turn_complete":["move_to_step",step_id="8c04e5b9…"]}`；字段定义 `models.go:228`。

### 未知 2：该机制在实践中的表现
- **验证方式**：查 `tasks` / `task_step_transitions` / `session_step_history`。
- **结论（试通证据）**：近 6 个任务里 4 个经 `engine_transition`(actor=agent) 从 Verify 自动推进到 Archive；1 个任务（`Write unimatrix-doc best practice from article`）自 08-24 07:40 进入 Verify 后至今未推进，其 Verify 会话状态 `CANCELLED`、`session_step_history` 无 signal 记录。
- **证据摘要**：`task_step_transitions` 中该任务最后一条为 `Review→Verify`（`trigger=manual, actor=default-user`），其后无记录。

### 未知 3：Clean 场景卡住的根因
- **验证方式**：读 `.claude/skills/openspec-verify-change/SKILL.md`。
- **结论（试通证据）**：技能的 Clean 分支只写「提示：下一步运行 openspec-archive-change 归档变更」，未要求调 `step_complete_kandev`；Drift 分支则「询问用户」。与工作流 prompt 的「完成后调 signal」存在措辞不一致，是 Clean 场景不发信号、步骤卡住的直接根因。
- **证据摘要**：`openspec-verify-change/SKILL.md` Step 4（Clean / Drift Found 两条路径）。

### 未知 4：是否可改用「仅 Clean 才推进」的条件动作 / 翻转 signal flag
- **验证方式**：枚举引擎事件类型（`models.go` 的 `OnTurnCompleteActionType`、Phase-2 `GenericActionType`、`AutoArchiveAfterHours`）。
- **结论（拒绝理由）**：引擎无「条件推进」原语；唯一自动推进是 `on_turn_complete`（受 signal 闸门控制）与 `on_turn_start` 等。翻转 `auto_advance_requires_signal=0` 会让 Drift Found 时 agent「停下提问」的 turn-end 也触发 `move_to_step→Archive`，把有漂移的任务也自动归档，违反 ADR 0015。
- **证据摘要**：`models.go:58-88`（`OnTurnCompleteActionType` 仅有 `move_to_next/move_to_previous/move_to_step/disable_plan_mode`）。

## Impact

- **代码**：无仓库代码改动（用户选定「只改工作流 prompt」）。变更落点在运行时 SQLite DB 的 `workflow_steps.Verify.prompt`，经 REST `PUT /api/v1/workflow/steps/c33e0ba3-14bd-4fe0-9670-230d94e5709a` 应用。
- **接口（复用，不新增）**：`PUT /api/v1/workflow/steps/:id`（本机无鉴权，`127.0.0.1:38429`；路由 `internal/workflow/handlers/handlers.go:68`）。
- **依赖**：运行中后端在线可达。
- **测试**：无单元测试；验证方式为应用后 `GET` 读回步骤 prompt 断言新分流文案、并观察后续任务 Verify→Archive 的 `engine_transition` 流转（spec 场景即验收依据）。
- **文档**：无公开文档影响（工作流为 DB 内用户自建，非内置模板）。
