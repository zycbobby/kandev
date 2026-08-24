# Apply Ledger: <change-name>

> 由 `openspec-apply-change` 在 apply 全程维护，位置 `openspec/changes/<change-id>/ledger/index.md`。
> 与 `reviews/` ledger 同级，随 change 一并归档。markdown 形态，人类可读、跨 agent。

## Per-Task Ledger

> 每条 task 到达终态（done / rolled-back）时追加一行。
> `executor`：本次所用执行器（用户所选；无人值守自动回退时记 `auto`）。

| task id | executor | 验证方法 | 结果 | 状态 | 时间戳 |
|---------|----------|----------|------|------|--------|
| <如 1.1> | <如 /goal / subagent-driven / executing-plans / inline / auto> | <Step 2a 用的验证方式> | <结论> | `done` / `rolled-back` | `YYYY-MM-DD HH:mm` |

## Exit Gate

> 由 Step 4 在宣告完成前填写；三项必须全部满足，否则不得宣告完成或进入 verify / archive。

- [ ] **Verification Strategy 入口通过**：`<tasks.md 中选定的验证入口命令>` — 结果：`<pass / 输出摘要>`
- [ ] **`openspec validate <change-id> --strict` 通过**
- [ ] **验收方法与证据**：
  - 方法：<用了什么验证方法>
  - 场景：<跑了什么场景 / 覆盖路径>
  - 结果：<得到什么结果>
