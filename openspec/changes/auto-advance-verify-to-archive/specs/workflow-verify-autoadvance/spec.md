# workflow-verify-autoadvance

## 背景

`man` workspace 的 `dev-openspec-workflow` 采用 Explore→Propose→Plan→Apply→Review→Verify→Archive→Finish-Worktreee 八步流程。Verify 步骤调用 `openspec-verify-change` 技能检测 code↔spec 漂移。期望行为：验证「无明确问题（Clean）」时自动推进到 Archive，「发现漂移（Drift Found）」时停下等待人工决策。当前 Verify 步骤已启用 signal-gating（`auto_advance_requires_signal=1` + `on_turn_complete: move_to_step→Archive`），但步骤 prompt 未显式区分 Clean/Drift 两种终态，且与技能的措辞不一致，导致 Clean 场景可能不发完成信号而卡在 Verify（已有实测案例）。

## 方案与决策

- **保留 signal-gating**：`auto_advance_requires_signal=1` 不变，`on_turn_complete` 仍为 `move_to_step→Archive`。引擎无「仅当 Clean 才推进」的条件动作，信号驱动是唯一能表达「无问题才推进」的机制。
- **重写 Verify 步骤 prompt**：显式分流「Clean → 调 `step_complete_kandev` 自动推进 Archive」与「Drift Found → 停下展示报告并提问、不发信号」，并明示技能本身只提示 archive、需 agent 主动发信号。
- **不改仓库级 `openspec-verify-change` 技能**（用户决定，out of scope）。
- **不翻转 `auto_advance_requires_signal=0`**（会连 Drift 场景也自动归档，违反 ADR 0015）。

## ADDED Requirements

### Requirement: Verify 步骤在无漂移时自动推进到 Archive
The Verify step SHALL advance the task to the Archive step automatically when drift detection reports a Clean status (no missing requirements, no out-of-scope additions, no design deviations), without requiring manual movement.

#### Scenario: Clean 结果自动推进
- **WHEN** the verify agent's drift detection reports Clean
- **THEN** the agent calls `step_complete_kandev` and the workflow transitions the task from Verify to Archive

### Requirement: Verify 步骤在发现漂移时停下等待人工决策
The Verify step SHALL halt for human decision and MUST NOT auto-advance when drift detection reports issues.

#### Scenario: Drift 结果不推进
- **WHEN** the verify agent's drift detection reports Drift Found (missing requirements, out-of-scope additions, or design deviations)
- **THEN** the agent presents the drift report and asks the user for decisions, and the task remains in Verify without advancing to Archive

### Requirement: Verify 步骤 prompt 显式编码推进规则
The Verify step's prompt SHALL explicitly encode the Clean-vs-Drift branching rule and the requirement to signal completion only on Clean.

#### Scenario: prompt 含分流指令
- **WHEN** an agent enters the Verify step
- **THEN** the step prompt instructs it to call `step_complete_kandev` when Clean and to stop and ask the user when Drift Found

## Known Failure Modes

| Symptom | Cause | Fix |
|---------|-------|-----|
| 任务卡在 Verify 不推进 | `openspec-verify-change` 技能 Clean 分支只提示「下一步 archive」、不发 signal，agent 照技能只报告不调 `step_complete_kandev` | Verify prompt 显式要求 Clean 时主动调 `step_complete_kandev` |
| 发现漂移仍被自动归档 | `auto_advance_requires_signal=0`（turn-end 无条件推进） | 保持 signal-gating=1，只有 agent 显式 signal 才推进 |
