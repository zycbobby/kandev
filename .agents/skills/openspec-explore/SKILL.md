---
name: openspec-explore
description: >
  Use for a lightweight, low-risk probe before deciding whether a change
  is worth opening — no active change or schema required. Reports the
  conclusion using the same Research Log fields used by openspec-propose
  and openspec-plan. Not a mandatory gate before propose.
---

# OpenSpec Explore

目标：提供一个**随时可用的轻量勘探入口**，用于在决定是否值得开一个 change 之前，先用最小风险的方式看一眼。

## 定位

- **不是流水线阶段**：不要求已存在 active change，不检查 `.openspec.yaml` 的 schema
- **不是强制关卡**：不要求用户在调用 `openspec-propose` 前必须先跑本 skill；需求已经清楚或只是机械变更时，直接走 `openspec-propose` 即可
- **不替代已有调研纪律**：`openspec-propose` Step 2.3 的问题空间调研、`openspec-plan` Step 1.6 的依赖准出门禁仍按各自流程执行；本 skill 只是提供一个更轻的、可以脱离 change 生命周期单独调用的探测动作

## 安全规则（与 `openspec-propose` Step 2.3 一致，不单独发明）

- 只做低风险、可逆、范围内的验证：读现有实现、跑最小命令、调用无副作用接口、复现当前流程、查看真实输出、用现有 fixture 跑一条闭环
- 涉及生产副作用、凭据、成本或权限不明时，**拒绝执行**，把无法验证的原因作为 blocker 汇报给用户，而不是冒险调用
- 如果无法安全试通，把 blocker、拒绝理由或后续验证入口如实汇报，不把未知伪装成已知

## 执行流程

### Step 1：确认是否存在 active change（仅用于决定汇报去向，不作为前置条件）

```bash
openspec list 2>/dev/null
```

- 若存在 active change 且用户希望把这次探测结果记入该 change：进入 Step 3 的"追加模式"
- 若不存在 active change，或用户只是想先看一眼：进入 Step 3 的"仅汇报模式"

### Step 2：执行 bounded probe

按安全规则执行最小风险的验证动作：读代码、跑最小命令、复现现象、验证一个具体假设。

### Step 3：用 Research Log 固定字段汇报结论

无论哪种模式，结论都使用与 `openspec-propose` / `openspec-plan` 一致的固定字段：

- **未知描述**：这条未知是什么，为什么值得看一眼
- **验证方式**：怎么验证的（读代码 / 跑命令 / 复现现象 / 调用接口）
- **结论**：三选一——"试通证据"（给出真实 input/output）、"拒绝理由"（说明为什么不可行或不采用）、"blocker"（说明为什么无法验证）
- **证据摘要**：命令 + 退出码、真实输出片段、文件路径或行号

**仅汇报模式**：只在对话中用上述字段汇报，不创建任何文件（不写 `research.md`/`explore.md`，不写 `proposal.md`/`design.md`）。

**追加模式**：向用户确认后，把这条记录追加进该 active change 的 `proposal.md`（若该 change 还处于 propose 阶段）或 `design.md`（若已存在 `design.md`，说明已进入 plan 阶段）的 `## Research Log` 章节；若该章节不存在，先创建该章节再追加。除追加这一条记录外，不修改该 artifact 的其他章节。

### Step 4：交接

告知用户：
- 本次探测的结论（试通证据 / 拒绝理由 / blocker）
- 若已追加进某个 change 的 Research Log，说明追加到了哪个文件
- 若结论支持继续推进，建议下一步调用 `openspec-propose`；若结论是拒绝或明确的 blocker，说明不建议继续的理由

## 约束

- 不创建 `research.md`、`explore.md` 或其他独立文件
- 不勾选任何 `tasks.md` 中的任务，不创建 `ledger/`
- 不检查、不要求特定 schema
- 不做生产副作用、凭据、成本或权限不明的调用
