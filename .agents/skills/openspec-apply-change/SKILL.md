---
name: openspec-apply-change
description: >
  Use when tasks.md exists under openspec/changes/<change-id>/ and you are ready
  to implement. Lets the user interactively choose an executor (agent-agnostic,
  with an always-available inline baseline), drives implementation task by task,
  records a per-task ledger, checks each task off, pauses on design conflicts,
  enforces an explicit exit gate before completion, and refuses to code while an
  active review reconciliation session is still blocking apply. Triggers: "开始实现",
  "执行任务", "apply the change", after openspec-plan completes.
---

# OpenSpec Apply

本 skill 在 apply 启动时由用户**交互式选择执行器**（agent-agnostic，始终提供零依赖的内联基线），按 tasks.md 逐步驱动实现，全程维护可追溯的 per-task 账本，并在宣告完成前强制通过显式准出门禁。

## 执行流程

### Step 1：读取 change 上下文

```bash
# 必读：了解要做什么
cat openspec/changes/<change-id>/proposal.md
cat openspec/changes/<change-id>/tasks.md

# 如有 design，也读取
cat openspec/changes/<change-id>/design.md 2>/dev/null

# 读取相关 spec deltas
cat openspec/changes/<change-id>/specs/*/spec.md 2>/dev/null

# 如有 review ledger，也读取
cat openspec/changes/<change-id>/reviews/state.json 2>/dev/null
cat openspec/changes/<change-id>/reviews/index.md 2>/dev/null
```

### Step 1a：检查 review session 是否阻塞 apply

如果存在 `openspec/changes/<change-id>/reviews/state.json`，必须先判断：

- `status` 是否为 `collecting`
- `applyBlocked` 是否为 `true`

**若任一条件成立：**
- 停止直接编码
- 告知用户当前 change 仍有 active review session / unresolved review round
- 明确提示先运行 `openspec-review-change` 完成 reconcile / finalize / cancel

**禁止**：在 collecting review session 存在时直接根据 review comment 改产品代码。

### Step 2：选择执行器并逐任务实现

#### Step 2-0：交互式执行器选择（agent-agnostic）

> **本步骤是必填项，不是可选步骤。** 即使可用执行器只有两项，也必须向用户展示菜单并等待回答后再继续。

在开始实现前，**让用户选择执行器**，而不是替用户自动决定。

1. **探测可用执行器**：以 agent 自身可见的 skills / commands 列表为唯一判定依据（**不需要**任何文件系统探测或 shell 命令）。候选及其特征：

   | 执行器 | 可用条件 | 账本机制 | 漂移风险 | 备注 |
   |--------|---------|---------|---------|------|
   | `subagent-driven` | `superpowers:subagent-driven-development` 可见 | ✅ TodoWrite + 双重审查 | 低 | **多任务首选**；每个 task 独立 context + spec/quality 双审 |
   | `ultragoal` | `/ultragoal` 命令可见（OMC/OMX 体系） | ✅ OMC/OMX state 完整账本 | 低 | 启动较重；仅当本机有 OMC/OMX 时才列入 |
   | `executing-plans` | `superpowers:executing-plans` 可见 | ⚠️ 依赖执行器内部机制 | 中 | 跨 session 推进，适合长时任务 |
   | `/goal` | Claude Code 原生，仅当可见 | ⚠️ 独立 Evaluator 每轮对账（防目标遗忘），但仅读 transcript，无法验证真实文件系统 | 中 | 轻量；task 少（≤5）时够用；**condition 必须要求 Claude 把验证结果显式打印出来**，否则 Evaluator 可能误判完成 |
   | **内联基线** | 永远在列 | ❌ 仅靠 skill 自带 ledger/index.md | 高 | 零依赖兜底；task 多时慎用 |

2. **询问用户**：把上面探测到的可用项组成菜单向用户提问，请其选择，并附上每项的账本/漂移特征说明。提问用**平台中立**的方式描述——**不要把指令绑定到某个具体提问工具名**。
3. **用户选定** → 按执行器分流：
   - **`subagent-driven` / `ultragoal` / `executing-plans`**：把控制权交给该编排器；skill 不再自己逐条实现，只在编排器完成后执行 Step 1a 检查和 Step 4 exit gate。
   - **`/goal`**：构造一个"可评估"的 condition 再启动。`/goal` 有独立 Evaluator 每轮对账，但 Evaluator **只读 transcript**，看不到真实文件系统——Claude 不打印结果，Evaluator 就无从判断。因此 condition 必须满足：
     - **要求显式打印验证结果**：如 `run npm test and print exit code`、`print git status output`
     - **描述可测量的终态**：如 `all tasks in tasks.md checked [x]`、`build exits 0`
     - **加过程约束（可选）**：如 `do not modify proposal.md or design.md`
     - **加轮次上限（可选）**：如 `or stop after 20 turns` 防止无限循环

     示例 condition：
     ```
     All tasks in tasks.md are checked [x], npm test exits 0 (print the output),
     and git status is clean. Do not modify proposal.md or design.md.
     Stop after 30 turns if not complete.
     ```
   - **内联基线**：由 skill 自身按 Step 2-1 逐条执行。
4. **无人值守回退**（仅限以下可证明条件之一，否则一律提问）：
   - CI / headless 管道中运行（无终端、无 MCP 对话通道）
   - 用户调用时已显式传入 executor 参数

   满足条件时按优先级自动选择：`subagent-driven`（可见时首选）→ `executing-plans` → `/goal` → 内联基线；并在账本（见 Step 2b）记录 `executor: auto`。**`ultragoal` 不参与自动回退**（启动重，不适合无感知启动）。回退不报错、不阻塞、不要求用户安装任何东西。

**无论选哪个执行器，以下纪律始终保留**：设计冲突暂停（Step 3）、scope 约束、review-session 阻塞判断（Step 1a / Step 4）、以及所有 task 完成后向 `ledger/index.md` 汇总结果（Step 2b）。

#### Step 2-1：逐任务执行循环（仅内联基线走此路径）

> 若选用专有编排器，跳过此节，由编排器驱动循环；完成后直接进入 Step 4。

按照 `tasks.md` 中的顺序逐任务执行：

1. 读取当前未完成的第一条 task
2. 实现该 task
3. **实现完成后，先思考如何验证这个 task 的交付物**（见 Step 2a）
4. 执行验证；验证通过后，在 `tasks.md` 中将对应条目改为 `- [x]`
5. **写账本**：向 `ledger/index.md` 追加该 task 的一行记录（见 Step 2b）
6. 提交（如果是有意义的完整单元）
7. 继续下一条 task

### Step 2a：验证策略（每个 task 实现后必做）

**在标记 `[x]` 之前**，针对该 task 的交付物选择最合适的验证方式：

| 交付物类型 | 优先验证方式 |
|------------|-------------|
| 函数 / 模块 | 运行现有相关测试；若无测试，手动构造最小调用验证输出 |
| CLI 命令或子命令 | 实际执行命令，检查 stdout/stderr/exit code 是否符合预期 |
| 文件生成 / 修改 | 检查文件存在、内容结构正确（`cat` / `grep` 关键字段）|
| 配置 / 模板变更 | lint 或 dry-run；若有 schema，做格式校验 |
| 构建产物 | 运行构建命令，确认 artifact 存在且可执行 |

**验证失败时**：修复问题后再次验证，不能跳过直接标 `[x]`。

### Step 2b：per-task 账本（可追溯执行记录）

apply 全程维护一份账本，位置 `openspec/changes/<change-id>/ledger/index.md`（与 `reviews/` ledger 同级，随 change 一并归档）。首个 task 开始前若该文件不存在则创建，结构见 `schema-assets/superpowers-bridge/templates/ledger.md`，两段：

- **`## Per-Task Ledger`**：每条 task 到达终态（完成 / 回滚）时追加一行，字段至少包含：
  | task id | executor | 验证方法 | 结果 | 状态 | 时间戳 |
  - `executor`：本次所用执行器（用户所选；无人值守回退时记 `auto`）
  - `验证方法` / `结果`：该 task 在 Step 2a 用的验证方式与结论
  - `状态`：`done` 或 `rolled-back`
- **`## Exit Gate`**：由 Step 4 在宣告完成前填写（见 Step 4）。

账本用 **markdown**（人类可读、跨 agent，无需解析器）。它是 apply 允许写入的产物之一（见「约束」）。

### Step 3：遇到设计冲突时暂停

**必须暂停的情况**：
- 实现过程中发现 `design.md` 中的技术决策有问题
- 需要的接口在 proposal 中没有描述
- 发现与其他模块的冲突

**暂停方式**：向用户说明：
1. 当前在做什么
2. 遇到了什么冲突
3. 提出 2-3 个解决选项，等待用户决策

**严禁**：自行做出超出 proposal/design 范围的架构决策（不要 vibe coding）。

### Step 4：全流程验证循环（退出条件）

**所有实现 task 勾选完毕后，不能直接宣告完成。必须逐项通过显式准出门禁（exit gate），并把结果写入账本。**

流程：

1. 再次检查 `reviews/state.json`；如果仍有 collecting review 或 `applyBlocked=true`，停止并要求先完成 review reconciliation
2. **逐项检查准出门禁三项**：
   1. **Verification Strategy 入口通过**：读取 `tasks.md` 顶部 `## Verification Strategy` 的验证入口并执行；若发现问题，修复后重跑，直到通过（`manual-oneshot` 则执行指定手动验证并确认输出符合预期；入口不存在或无法执行则暂停并告知用户）
   2. **`openspec validate <change-id> --strict` 通过**
   3. **提供验收方法与证据**：明确用了什么验证方法、跑了什么场景、得到什么结果
3. **把门禁结果写入账本** `ledger/index.md` 的 `## Exit Gate` 段：三项各自的通过状态 + 第 3 项的验收方法 / 场景 / 结果
4. 三项全部满足后，才能进入 Step 5

**关键规则**：
- 这是 apply 阶段的**退出条件**，不是可选步骤；**任一门禁项不满足，不得宣告完成，也不得进入 verify / archive**
- 即使 per-task 验证（Step 2a）都通过了，仍需跑这个全流程门禁
- **TDD / 单测仅为实现过程中的可选手段，不是最终验收门槛**：单测全绿**不构成**完成；最终验收以 `## Verification Strategy` 选定的验证入口通过为准（按本仓库取向，该入口应优先是 e2e / 集成级验证）

### Step 5：完成后提示

全流程验证通过后，告知用户实现完成。

提示：下一步运行 `openspec-verify-change` 检查是否有漂移。

## 约束

- **只修改代码、tasks.md 与 change `ledger/`**：不修改 proposal.md、design.md、spec deltas
- **遇到设计冲突必须暂停**：不自行决策，等待用户指引
- **保持 tasks.md 勾选状态最新**：每完成一个 task 立即更新
- **collecting review session 阻塞 apply**：review 未 reconcile 前不得继续编码
- **全流程验证循环是退出条件**：所有 task 完成后必须至少跑一轮 verify → bugfix → verify，不通过不能宣告完成

## 输出

```text
openspec/changes/<change-id>/
  tasks.md         # 更新：已完成的 task 改为 [x]
  ledger/index.md  # per-task 执行记录 + 准出门禁结果
代码文件           # 按 tasks.md 实现的代码变更
```
