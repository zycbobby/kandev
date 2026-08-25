## Context

`man` workspace 的 `dev-openspec-workflow`（DB 内 `source='manual'`，非内置模板）采用 Explore→Propose→Plan→Apply→Review→Verify→Archive→Finish-Worktreee 八步流程。Verify 步骤（step id `c33e0ba3-14bd-4fe0-9670-230d94e5709a`，position 5）当前配置：

- `auto_advance_requires_signal = 1`（ADR 0015 signal-gating）
- `events.on_turn_complete = move_to_step → Archive`（step id `8c04e5b9-8b9c-458c-a7b7-2f8f4c68c5a3`）
- `events.on_enter = auto_start_agent + reset_agent_context`
- `prompt`：使用 `openspec-verify-change` 技能，但把「完成」与「无问题」混为一谈，未显式区分 Clean / Drift 两种终态。

实测：近 6 个任务里 4 个经 `engine_transition`(actor=agent) 从 Verify 自动推进到 Archive；1 个任务因 Verify 会话 `CANCELLED` 且未发 `step_complete_kandev` 而卡在 Verify。根因是仓库级 `openspec-verify-change` 技能的 Clean 分支只提示「下一步 archive」、不显式要求发 signal，与工作流 prompt 措辞不一致。

本 change 目标是：让 Verify 步骤「无明确问题（Clean）即自动推进到 Archive、发现漂移（Drift Found）即停下等待人工决策」可靠。

## Goals / Non-Goals

**Goals:**
- 重写 Verify 步骤 prompt，显式分流 Clean（→ 调 `step_complete_kandev` 自动推进 Archive）与 Drift Found（→ 停下提问、不发 signal）。
- 保留 `auto_advance_requires_signal=1` 与 `on_turn_complete → Archive` 不变。
- 应用后经 REST 读回校验新 prompt 与未变字段。

**Non-Goals:**
- 不改仓库级 `openspec-verify-change` 技能（用户已决定，out of scope）。
- 不翻转 `auto_advance_requires_signal=0`（会连 Drift 场景也自动归档，违反 ADR 0015）。
- 不改引擎、不改其它工作流（`issue-driven-openspec-workflow` 不受影响）。
- 不为「Clean 才推进」新增引擎级条件动作（引擎无此原语，且非必需）。

## Research Log

### 未知 1：能否用 REST PUT 只更新 Verify 步骤的 prompt（部分更新）
- **验证方式**：读 `apps/backend/internal/workflow/controller/controller.go` 的 `UpdateStepRequest` 与 `handlers.go` 的 `httpUpdateStep`；并已用 `GET /api/v1/workflow/steps/c33e0ba3-…` 实测读回当前步骤。
- **结论（试通证据）**：`UpdateStepRequest` 的 `Prompt` 字段为 `*string`（`json:"prompt,omitempty"`），`AutoAdvanceRequiresSignal` 为 `*bool`（omitempty），故 `{"prompt": "<新文本>"}` 可做部分更新、只改 prompt，不动其它字段；handler 绑定 JSON 后调 `controller.UpdateStep`，成功后回 200 并 publish `workflow_step.updated`。
- **证据摘要**：`controller.go:171-186`（`UpdateStepRequest` 字段含 `Prompt *string`）；`handlers.go:68`（`api.PUT("/workflow/steps/:id", h.httpUpdateStep)`）、`handlers.go:189-206`；`GET` 返回当前步骤完整 JSON（含 `auto_advance_requires_signal:true`、`on_turn_complete` 指向 `8c04e5b9`）。

### 未知 2：变更通道应走 REST 还是直写 SQLite
- **验证方式**：读 debug-kandev skill 与 workflow service 说明。
- **结论（试通证据）**：走 REST `PUT`，不直写 SQLite——后端会原子更新 + 发 `workflow_step.updated` 事件，且无缓存不一致；直写 DB 会绕过事件发布、破坏 WS 驱动的 UI 与队列重评估。
- **证据摘要**：`.claude/skills/debug-kandev/SKILL.md`（「改法用 REST API，不要直接写 SQLite」）；`apps/backend/CLAUDE.md` 的 workflow step 事件发布约定（`internal/workflow/stepevents.Publisher`）。

## Decisions

### D1：变更面只落在 Verify 步骤 prompt（DB）
- **选择**：仅经 `PUT /api/v1/workflow/steps/c33e0ba3-…` 更新 `prompt`，无仓库代码改动。
- **理由**：用户在本 change 澄清阶段选定「只改工作流 prompt」；prompt 是 agent 进入步骤时收到的首要指令，显式化后足以消除与技能的措辞冲突。
- **已考虑 alternative**：同时改仓库级 `openspec-verify-change` 技能 Clean 分支 → 被拒（用户选定，且该 skill 被 `man` 下两个工作流共用、范围外）。

### D2：保留 signal-gating，不翻转 flag
- **选择**：`auto_advance_requires_signal` 保持 `1`。
- **理由**：引擎无「仅当 Clean 才推进」的条件动作；翻转 `=0` 会让 Drift Found 时 agent「停下提问」的 turn-end 也触发 `move_to_step→Archive`，把有漂移的任务误归档。
- **已考虑 alternative**：`auto_advance_requires_signal=0`（legacy turn-end 推进）→ 被拒，违反 ADR 0015。

### D3：应用方式走 REST PUT，且用 python3 构造 JSON
- **选择**：用 `python3` 把多行 prompt 序列化为 JSON 写临时文件，`curl -X PUT` 发送；不用手工转义。
- **理由**：prompt 含换行与反引号，手工转义易错；python3 本机已装（`sqlite3` 未装但 `python3` 在），可确定性生成 body。
- **已考虑 alternative**：`curl -d '{"prompt":"..."}'` 手工内联 → 易因转义出错，被拒。

## Risks / Trade-offs

- [Risk] prompt-only 修复仍有残余风险：`openspec-verify-change` 技能 Clean 分支仍只提示「下一步 archive」，若 agent 更信技能措辞可能仍不发 signal → Mitigation: 新 prompt 明确「技能本身只提示 archive、必须由你主动调 `step_complete_kandev`」，以显式指令覆盖技能的弱提示。
- [Risk] 该工作流其余步骤（Explore/Propose/Plan/Apply）prompt 也依赖同类 signal 约定，但本次只改 Verify → Mitigation: 范围保持 Verify 单步，其它步骤不变（用户未要求）。
- [Trade-off] 不做行为级自动 E2E（真实任务跑通 Clean 场景成本高、需人工启动）→ 接受理由：以 REST 读回断言 + 后续任务 Verify→Archive 的 `engine_transition` 观测作为验收，够用且零副作用。

## Migration Plan

N/A — 本 change 不涉及部署变更。应用为单条 REST `PUT`（更新一个 `workflow_steps` 行的 `prompt`），无 DB schema 变更、无后端重启。回滚：重新 `PUT` 回旧 prompt 文本即可。

## Open Questions

（无。变更边界与机制均已收敛；行为级 E2E 观测作为 apply 后的人工验收项，非阻塞。）
