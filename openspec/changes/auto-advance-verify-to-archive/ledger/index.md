# Apply Ledger: auto-advance-verify-to-archive

> 由 `openspec-apply-change` 在 apply 全程维护，位置 `openspec/changes/<change-id>/ledger/index.md`。
> 与 `reviews/` ledger 同级，随 change 一并归档。markdown 形态，人类可读、跨 agent。

## Per-Task Ledger

> 每条 task 到达终态（done / rolled-back）时追加一行。
> `executor`：本次所用执行器（用户所选；无人值守自动回退时记 `auto`）。

> **执行器说明**：用户选定 `subagent-driven`，但本 change 为「仅改运行时 DB 的 Verify 步骤 prompt」、**无仓库代码改动**，无 code diff / commit / test 可让 implementer+reviewer 子代理评审。故按 1.1/1.2/1.3 三条确定性命令**由 apply 控制器直接执行**，以程序化读回断言替代子代理评审，其余纪律（per-task ledger、勾选、exit gate）保持一致。

| task id | executor | 验证方法 | 结果 | 状态 | 时间戳 |
|---------|----------|----------|------|------|--------|
| 1.1 | inline | python3 生成 `/tmp/verify-prompt.json` 后 `json.load` 校验 | JSON 合法，`prompt` 含全部分流文案 | `done` | 2026-08-25 09:28 |
| 1.2 | inline | `curl -X PUT` 返回 HTTP 200 且 body 含新 prompt | 写入成功，`updated_at` 更新为 2026-08-25T01:29:14Z | `done` | 2026-08-25 09:28 |
| 1.3 | inline | `GET` 读回 + python3 断言 8 项 | 8/8 PASS | `done` | 2026-08-25 09:29 |

## Exit Gate

> 由 Step 4 在宣告完成前填写；三项必须全部满足，否则不得宣告完成或进入 verify / archive。

- [x] **Verification Strategy 入口通过**：`curl -s "http://127.0.0.1:38429/api/v1/workflow/steps/c33e0ba3-14bd-4fe0-9670-230d94e5709a"`（读回后断言）— 结果：`OVERALL: PASS`（prompt 含「Drift Report 为 Clean」「step_complete_kandev」「Drift Found」；`auto_advance_requires_signal=true`；`on_turn_complete[0].config.step_id=8c04e5b9-…`；`on_enter` 含 `auto_start_agent`+`reset_agent_context`）
- [x] **`openspec validate auto-advance-verify-to-archive --strict` 通过** — 结果：`Change 'auto-advance-verify-to-archive' is valid`（exit 0）
- [x] **验收方法与证据**：
  - 方法：REST 读回断言（PUT 后 GET + python3 逐项断言）+ `openspec validate --strict`；改前 GET 快照留档 `/tmp/verify-step-before.json` 供回滚。
  - 场景：路径 1（Clean 自动推进）读回断言——新 prompt 显式分流 Clean→`step_complete_kandev` 自动推进 Archive；路径 2（Drift 不推进）回归保护——`auto_advance_requires_signal` 保持 `true`、`on_turn_complete` 仍指向 Archive、`on_enter` 未变。
  - 结果：读回断言 8/8 PASS；未变字段（signal-gating / on_turn_complete / on_enter）全部保持；`openspec validate --strict` 通过。
  - 未验证假设（按 plan 声明为 apply 后人工观测）：行为级「Clean→自动推进」需真实任务跑通，作为 deferred manual observation 记录在 tasks.md「验证清单」第 3 条（未勾选，非本 change 代码层面产出）。

## Rulings I made

（无。本 change 为单一步骤 prompt 写入，实现过程未遇到设计冲突、未偏离 proposal/design/tasks 文本。）
