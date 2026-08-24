---
name: openspec-verify-change
description: >
  Use after implementation is complete to detect drift between code and spec.
  Dispatches a subagent to compare implementation against proposal.md and
  design.md, reports unimplemented requirements, out-of-scope additions, and
  design deviations, and refuses to verify while a review reconciliation round is
  still collecting or blocking apply. Triggers: "检查漂移", "verify",
  "check drift", after openspec-apply-change completes.
---

# OpenSpec Verify

本 skill 在实现完成后，通过派发 subagent 对比代码与 proposal/design，检测三类漂移问题：未实现、超出范围、设计偏差。

## 执行流程

### Step 1：收集检测材料

读取规格文档：

```bash
cat openspec/changes/<change-id>/proposal.md
cat openspec/changes/<change-id>/tasks.md
cat openspec/changes/<change-id>/design.md 2>/dev/null
cat openspec/changes/<change-id>/specs/*/spec.md 2>/dev/null
cat openspec/changes/<change-id>/reviews/state.json 2>/dev/null
cat openspec/changes/<change-id>/reviews/index.md 2>/dev/null
```

### Step 1a：先检查 review session 是否已收束

如果 `reviews/state.json` 存在，则必须先判断：

- `status` 是否仍为 `collecting`
- `applyBlocked` 是否仍为 `true`

**若任一条件成立：**
- 停止 drift detection
- 告知用户当前 review round 尚未 finalize / cancel
- 明确要求先完成 `openspec-review-change`
- 注意：review round 只能在用户显式确认后 finalize，Agent 不能自行结束当前 review

### Step 2：收集实现内容

获取实现内容（优先 git diff，无 git 则读取相关文件）：

```bash
# 优先：与主分支对比
git diff main...HEAD 2>/dev/null

# 备选：最近一次提交
git diff HEAD~1...HEAD 2>/dev/null
```

如果 git 不可用，读取 proposal 中 Impact 列出的相关代码文件。

### Step 3：派发 drift-detection subagent

使用 Agent tool 派发 subagent，将以下 prompt 中的占位符替换为实际内容后发送：

---

你是一个漂移检测专家。请对比规格文档与实现，检测三类问题。

**规格文档（proposal.md）**：
[此处插入 proposal.md 完整内容]

**design.md**（如有）：
[此处插入 design.md 内容，无则写"无"]

**tasks.md**：
[此处插入 tasks.md 内容]

**spec deltas**：
[此处插入 spec deltas 内容]

**review ledger**（如有）：
[此处插入 reviews/index.md 与当前 relevant round 摘要；若某些 feedback 已明确 rejected / deferred，也要包含]

**实现内容（git diff 或代码文件）**：
[此处插入实现内容]

检测三类问题：

1. **未实现（Missing）**：proposal 中的 Requirement 或 Scenario 在代码中没有对应实现
2. **超出范围（Out of Scope）**：代码实现了 proposal 中未提到的功能
3. **设计偏差（Design Deviation）**：实现与 design.md 的技术决策不符

输出格式：

## Drift Report

**Status:** Clean | Drift Found

**Issues（如有）：**
- [Missing] `Requirement: <名称>` - Scenario "<场景名>" 未找到对应实现
- [Out of Scope] `<文件>:<行号>` - 实现了 proposal 未描述的 <功能>
- [Design Deviation] `<文件>:<行号>` - 使用了 <实际方案>，design.md 要求 <设计方案>

校准：只报告真实的漂移问题。代码风格差异、命名不同、小的实现细节不是漂移。若 review ledger 中已明确拒绝某条 feedback，不要把它当成 drift。

---

### Step 4：处理检测结果

**如果 Status: Clean**：
- 告知用户实现与规格一致，无漂移
- 提示：下一步运行 `openspec-archive-change` 归档变更

**如果 Status: Drift Found**：
- 展示完整的 Drift Report 给用户
- 分析每个问题的优先级：
  - **Must Fix**：Missing 和 Design Deviation（影响规格正确性）
  - **Discuss**：Out of Scope（可能是合理的实现细节，也可能是 scope creep）
- 询问用户：
  - 每个 Out of Scope 项是保留还是移除
  - Must Fix 项返回 `openspec-apply-change` 修正

## 输出

**无文件输出**，只产生报告和行动建议。
