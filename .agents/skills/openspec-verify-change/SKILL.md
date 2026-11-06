---
name: openspec-verify-change
description: >
  Use after implementation is complete to run a convergence check instead of
  a drift report: machine checks first (openspec validate + project tests),
  a coverage matrix over the change's spec deltas, gaps classified as
  missing/partial/contradicts/unrequested, every finding requiring both a
  source ID and an observable consequence, output as Phase: Convergence
  tasks appended to tasks.md (unrequested items become adjudication
  requests). Does not block archive. Triggers: "检查收敛", "verify",
  "检查漂移", "check drift", "convergence", after openspec-apply-change
  completes.
---

# OpenSpec Verify（收敛检查）

本 skill 在实现完成后对照**封闭清单**核对覆盖：以 change 的 spec deltas 与 tasks.md 为准逐条找实现证据，缺口转化为任务，而不是对 proposal/design 散文做开放比对。`Converged` 是健康常态。**verify 不阻塞 archive**——门禁职责归 tasks 勾选与 `openspec validate`。

## 执行流程

### Step 0：机器先行

先跑机器检查，把结果压成两行纳入检测材料（这些活不派给 LLM）：

```bash
openspec validate <change-id> --strict --no-interactive
# 项目测试：按仓库约定执行（如 npm test / pytest；CLAUDE.md 有约定用约定）
```

### Step 1：检查 review session 是否已收束

如果 `reviews/state.json` 存在，必须先判断：

- `status` 是否仍为 `collecting`
- `applyBlocked` 是否仍为 `true`

**若任一条件成立**：停止收敛检查，告知用户当前 review round 尚未 finalize / cancel，要求先完成 `openspec-review-change`。review round 只能在用户显式确认后 finalize，Agent 不能自行结束当前 review。

### Step 2：组装检测材料（scoped work packet）

只传**路径与范围**，不内联全文：

- deltas：`openspec/changes/<change-id>/specs/*/spec.md` ← **唯一比对基准**
- tasks：`openspec/changes/<change-id>/tasks.md`（勾选状态）
- rulings：`openspec/changes/<change-id>/reviews/`（**rejected / deferred 的 feedback 不计缺口**）
- 实现范围：`git diff main...HEAD`（备选 `HEAD~1...HEAD`）限定为本 change 触及文件；不携带全仓库 diff，不重新探索 codebase；git 不可用则读 proposal Impact 列出的文件
- intent 边界：`proposal.md` 的 Why / Impact 段（仅用于 unrequested 的能力级判定）

### Step 3：派发收敛检查 subagent

使用 Agent tool 派发 subagent（「规格收敛检查员」），packet 如下：

---

你是规格收敛检查员。对照封闭清单核对实现，产出缺口任务；不做泛泛 review。

**材料路径**：（同 Step 2 所列，按路径自读）

**步骤（按序）**：

1. **覆盖矩阵（主体任务）**：对 deltas 中每条 Requirement / Scenario，给出实现证据——`文件:行` 或测试名。给不出证据 → 缺口。注意力放在「逐条找证据」，不是「扫 diff 找问题」。
2. **越界审计**：只查 diff 中**新增的对外可观察行为**——新命令、端点、flag、持久化状态、错误契约变化、用户可见行为。找不到 Requirement 支撑 → unrequested。
   明确**不算** unrequested：内部 helper、重命名、注释、日志文案、目录组织、防御分支、重构。
3. **design 偏离**：仅当偏离改变上述对外可观察行为面才报；内部实现路径选择不算。

**缺口四分类**：`missing`（无实现证据）/ `partial`（有证据但场景覆盖不全）/ `contradicts`（实现与 Requirement 行为不符）/ `unrequested`（新增可观察行为无 Requirement 支撑）。

**降噪硬规则**：

- 每条缺口必须**同时**给出：出处（Requirement/Scenario ID 或 diff 位置）+ 可观察后果（哪个用户/测试/模块会看到什么不同）。给不出后果 → 删除该条，不输出。
- 不确定 → 降为 note，宁漏报不误报。
- 永不报告：风格、命名、注释、文档措辞、目录组织、内部实现路径。那是 review 的事。
- 不爬本 change 触及文件之外的代码；每条发现至多一次定点检查。

**输出**：

- 无缺口 → `Converged`
- 有缺口 → `## Phase: Convergence` 任务块，每条含：做什么 + 验收（哪个 Scenario 通过）+ 改码或改规格
- unrequested 项**不自动追加**，单独列为裁决请求（保留并补 delta 合法化 / 移除）

---

### Step 4：处置产出

- **Converged**：告知用户实现与规格收敛，提示下一步运行 `openspec-archive-change`。
- **有缺口**：展示产出，经用户确认后把 `Phase: Convergence` 任务追加进 `tasks.md`，返回 `openspec-apply-change` 执行，完成后再跑本 skill，直至 Converged。
- **unrequested**：逐项请用户裁决——保留则补 delta 合法化（语义增补走对应流程）；移除则回 `openspec-apply-change` 删除。

## 输出

无独立报告文件。唯一写入动作：经用户确认后的收敛任务追加进 `tasks.md`。
