---
name: openspec-review-change
description: >
  Use when human review feedback must be reconciled into OpenSpec artifacts before
  more coding. Opens or resumes a review session, groups feedback into rounds
  bound to checkpoints, updates proposal/spec/design/tasks when needed, and never
  edits product code directly. Triggers: "/review", "review this change",
  "address review comments", after or during openspec-apply-change.
---

# OpenSpec Review Change

本 skill 将人类 review 从“直接驱动代码修改的 prompt”转换为“先对齐 OpenSpec artifact 的显式生命周期阶段”。

核心规则：**Do not modify product code until review feedback has been reconciled into OpenSpec artifacts.**

Superpowers 集成策略：
- 默认使用 `superpowers:receiving-code-review` 做 review 输入归一化
- 仅在意见含糊、冲突、或难以分类时，才可选使用 `superpowers:brainstorming` 做澄清
- round 收口前使用 `superpowers:verification-before-completion` 检查 artifact 是否齐全
- 若后续需要修实现，则回到 `openspec-apply-change`，并在 apply 中条件性使用 `systematic-debugging` / `test-driven-development`，而不是在本 skill 内直接改代码

## 执行流程

### Step 1：读取 change 上下文与 review ledger

```bash
cat openspec/changes/<change-id>/proposal.md
cat openspec/changes/<change-id>/tasks.md
cat openspec/changes/<change-id>/design.md 2>/dev/null
cat openspec/changes/<change-id>/specs/*/spec.md 2>/dev/null
cat openspec/changes/<change-id>/reviews/state.json 2>/dev/null
cat openspec/changes/<change-id>/reviews/index.md 2>/dev/null
```

如果 `reviews/` 目录不存在，则初始化：

```text
openspec/changes/<change-id>/reviews/
  state.json
  index.md
  checkpoints/
```

### Step 2：把 `/review` 解释为开启或恢复 review session

- `/review` **不是**单条消息命令，而是开启或恢复当前 change 的 active review session
- 若 `reviews/state.json` 记录 `status=collecting`，则继续当前 round
- 若上一轮已 `finalized` / `cancelled`，或实现基线已变化，则创建新 round + 新 checkpoint

当前 round 的最小状态记录在 `reviews/state.json`：

```json
{
  "activeRound": 1,
  "activeCheckpoint": "cp-001",
  "status": "collecting",
  "mode": "in-flight",
  "applyBlocked": true
}
```

### Step 3：创建或恢复 checkpoint

每轮 review 都必须绑定一个 checkpoint，描述“这轮 review 审的是哪一版实现”。

checkpoint 最小字段：
- `id`
- `changeId`
- `trigger`（`in-flight` | `post-apply` | `post-fix`）
- `createdAt`
- `completedTasks`
- `pendingTasks`
- `modifiedFiles`
- `gitCommit`（可选）
- `allowApply`
- `requiresReconcile`

checkpoint 文件路径：

```text
openspec/changes/<change-id>/reviews/checkpoints/<checkpoint-id>.json
```

**触发规则**：
- 实现中途被打断 → `trigger=in-flight`
- tasks 完成后进入 review → `trigger=post-apply`
- 上一轮修复后再次 review → `trigger=post-fix`

### Step 4：收集 review 输入

在进入正式分类前，优先用 `superpowers:receiving-code-review` 对 review comment 做归一化：拆分多条意见、识别可能误读、分离事实 / 建议 / 疑问。


active review session 存在且 `status=collecting` 时：

- 后续**未带 `/review` 前缀**的用户消息，默认视为当前 round 的补充输入
- 不把这些 followup 解释成直接 coding instruction
- 只有显式操作才改变当前 round 生命周期：
  - `/review finalize`
  - `/review cancel`
  - `/review new`

把本轮输入持续追加到：

```text
openspec/changes/<change-id>/reviews/<round>.md
```

```md
## Review Inputs
- item 1
- item 2
```

### Step 5：必要时先做受限澄清

如果 review 意见满足以下任一条件：
- 表述含糊
- 多条意见互相冲突
- 无法判断是 code-only / requirement / design / scope

则**仅可选地**使用 `superpowers:brainstorming` 做澄清。

约束：
- brainstorming 只用于澄清，不替代 classification / checkpoint / artifact reconciliation
- 澄清结束后仍必须回到本 skill 的分类步骤

### Step 6：分类每条 review 意见

每条 review comment 必须落入以下五类之一：

```text
A. Code-only fix
   仅修实现缺陷，不改需求/设计/范围
   -> 更新 tasks.md

B. Requirement change
   改用户可见行为、API、错误语义、权限、数据契约
   -> 先更新 spec delta

C. Design change
   改架构、模块边界、依赖、数据流、迁移策略
   -> 先更新 design.md

D. Scope change
   增减 feature 或改变 proposal intent
   -> 更新 proposal.md；必要时开新 change

E. Invalid / rejected feedback
   错误、越界、已满足或明确拒绝
   -> 记录原因，不改代码
```

### Step 7：更新 artifact，但不改产品代码

分类完成后，只允许做这些改动：

- `proposal.md`
- `specs/*/spec.md`
- `design.md`
- `tasks.md`
- `reviews/` ledger

**禁止**：
- 在本 skill 内直接改产品代码
- 未完成分类就先修代码
- 把“顺便加个功能”静默并入当前实现

### Step 8：完成当前 round

当当前 review 已经收敛时，Agent **只能提出建议 finalize**，不能自行结束 round。

只有在用户显式确认（例如 `/review finalize`）后，才能：

1. 在 `reviews/<round>.md` 写入：
   - `## Status`
   - `## Trigger`
   - `## Checkpoint`
   - `## Review Inputs`
   - `## Classification`
   - `## Artifact Updates Required`
   - `## Implementation Tasks Added`
   - `## Rejected or Deferred Feedback`
   - `## Verification Plan`
   - `## Resume Decision`
2. 更新 `reviews/index.md` 摘要
3. 在 finalize 前，使用 `superpowers:verification-before-completion` 检查本轮 artifact 更新、追加任务、拒绝项记录与 resume decision 是否齐全
4. 把 `reviews/state.json.status` 设为 `finalized` 或按需切换到下一轮 `collecting`
5. 只有当 `applyBlocked=false` 且所需 artifact 已更新后，才允许回到 apply

### Step 9：提示下一步

- 若当前 round 产生了新的实现任务：提示返回 `openspec-apply-change`
- 若 review 仅补 artifact 且无需再实现：提示继续 `openspec-verify-change`
- 若 feedback 改变 scope：提示更新 proposal 或新开 change

## 输出

```text
openspec/changes/<change-id>/
  proposal.md                # 必要时更新
  design.md                  # 必要时更新
  tasks.md                   # 必要时更新
  specs/*/spec.md            # 必要时更新
  reviews/
    state.json
    index.md
    checkpoints/*.json
    <round>.md
```

**不输出产品代码修改。**
