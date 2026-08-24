# Apply Ledger: add-make-sync-workflow

> 由 `openspec-apply-change` 在 apply 全程维护，位置 `openspec/changes/add-make-sync-workflow/ledger/index.md`。
> Executor: `superpowers:subagent-driven-development`（用户指定，无人值守不交互）。

## Preflight Scan（dispatch 前冲突扫描）

| Task A | Task B | 共享接口 / 文件 | 结论 |
|--------|--------|----------------|------|
| 1.1/1.2 `scripts/sync-workflow.py` | 2.2 Makefile target | CLI `python3 scripts/sync-workflow.py <url> <out_dir>` | 一致：2.2 以 `"$(URL)" "$(CURDIR)/workflows"` 调用，匹配 1.1 契约 |
| 1.1 脚本 | 3.1 测试 | 文件名消歧算法 + 退出码 + stderr 含 URL | 一致：3.1 断言 `kanban.yml`/`pr-review.yml`/`kanban--beta.yml` 与 slug+消歧算法吻合（ws-1 Alpha: Kanban→kanban, PR Review→pr-review；ws-2 Beta: Kanban→kanban--beta） |
| 2.2/2.3 Makefile | 3.1 测试 (a) | `make help` 列 sync-workflow、`make -n sync-workflow` 打印 python 调用 | 一致：3.1(a) 断言这两点 |
| 3.2 Makefile test-scripts | 2.x Makefile | 不同区域（test-scripts vs variables/deploy/help） | 无冲突：3.2 与 `make-deploy.test.sh` 并列，不触碰 2.x 区域 |

任务自洽性：Task 1 伪码契约精确（slug/get/main 错误串逐字给出）；Task 2 引用 `phase`/`success` 宏（Makefile:73-80 已存在）；Task 3 桩路由与断言与 slug 算法吻合。扫描 clean。

**Ruling 1**：proposal.md 曾提 `scripts/sync-workflow.sh`（shell）与 `scripts/make-sync-workflow.test.sh`；design.md D1 与 tasks.md 均定为 `scripts/sync-workflow.py`（Python3 stdlib）与 `scripts/sync-workflow.test.sh`。design/tasks 为绑定权威，proposal 中为早期占位名。→ 按 Python3 + `sync-workflow.test.sh` 实现。成本若误：无（D1 已明确拒绝 bash/curl/jq）。

## Per-Task Ledger

> 每条 task 到达终态（done / rolled-back）时追加一行。

| task id | executor | 验证方法 | 结果 | 状态 | 时间戳 |
|---------|----------|----------|------|------|--------|
| 1.1/1.2 | subagent-driven | py_compile + live smoke(8 yml) + unreachable(exit 1) + review | 通过 | done | 2026-08-24 20:05 |
| 2.1/2.2/2.3 | subagent-driven | make help + make -n (default & URL override) + review | 通过 | done | 2026-08-24 20:15 |
| 3.1/3.2 | subagent-driven | `bash scripts/sync-workflow.test.sh` + review | 通过 | done | 2026-08-24 20:35 |

## Task Completion Log

- `Task 1: complete (commits 3cd88733a..ffaa4d074, review clean)` — reviewer Approved, 0 findings; ⚠️ test-evidence resolved by controller smoke (failure exit 1 + URL in stderr; live backend synced 8 yml).
- `Task 2: complete (commits ffaa4d074..f57071c04, review clean)` — reviewer Approved, 0 findings; controller verified `make help` lists both entries and `make -n` prints the python call with default & overridden URL.
- `Task 3: fix round 1/5 (1 addressed, 0 open — stub startup could vacuous-pass; commits 7d89b1d77..89a24f5be)`.
- `Task 3: complete (commits f57071c04..89a24f5be, review clean)` — scoped re-review ADDRESSED, no new breakage; controller ran `bash scripts/sync-workflow.test.sh` exit 0 (13 checks). Note: full `make test-scripts` is blocked by a PRE-EXISTING env failure in `scripts/dev-prod-db-path.test.sh` ("Make variable home" expects `D:\kandev-probe` Windows-probe path but this env resolves to `/media/zuo/AigoData/kandev-home`), unrelated to this change and preceding our test line in the target order.

## Final Whole-Branch Review

- Verdict `Needs fixes`（0 Critical，3 Important + 6 Minor，产品代码正确）→ 单次 fix wave 修复 3 Important + 1 廉价 Minor（commits 89a24f5be..3d3cdfe06）→ scoped re-review 全部 ADDRESSED，无新 breakage。最终干净。

**Parked (deferred, non-blocking) findings：**
- `Final review: minor (deferred):` sync-workflow.py `json.loads(...)["workspaces"]/["workflows"]` 在非 JSON 200 响应时抛裸 traceback（无 `sync-workflow:` 前缀/URL）。可后续包裹 try/except。
- `Final review: minor (deferred):` `URL ?=` 会被已导出的 `URL` 环境变量静默覆盖（D2 已选 `URL`，仅记录，不违反）。
- `Final review: minor (deferred):` 非 ASCII workflow 名全部坍缩为 `workflow` fallback slug（spec 要求 unique+deterministic，满足；"predictable in spirit" 是软建议）。
- `Final review: minor (deferred):` 删除占用裸名槽位的 workflow 后，下次运行会提升另一 workspace 的同名 workflow 到裸名（D3 固有，backup 语义可接受）。
- `Final review: minor (deferred):` help 条目列对齐/归类为 "Service Commands" 的观感（cosmetic）。
- `Final review: minor (deferred):` `task-1-report.md` 被提交而后续 task report 未提交（惰性进程元数据，不阻塞）。

## Exit Gate

> 由 apply 完成前填写；三项必须全部满足。

- [x] **Verification Strategy 入口通过**：`bash scripts/sync-workflow.test.sh` — 结果：`exit 0，All sync-workflow checks passed（16 checks 全绿）`
- [x] **`openspec validate add-make-sync-workflow --strict --no-interactive` 通过**：`Change 'add-make-sync-workflow' is valid`
- [x] **验收方法与证据**：
  - 方法：e2e-script —— 桩 `http.server` 跑通「列 workspace → 列 workflow → 导出 → 落盘」全路径，断言文件名、去重、幂等、陈旧文件保留、不可达失败
  - 场景：路径 1（成功，3 文件 + stale 保留）；路径 2（跨 workspace 同名 → `kanban.yml`/`kanban--beta.yml` 互不覆盖）；路径 3（关闭端口 → 退出非 0 且 stderr 含 URL）；make 级 `make help` / `make -n sync-workflow`
  - 结果：全部通过；`make help` 列出两行 sync-workflow，`make -n` 打印 `python3 scripts/sync-workflow.py "http://localhost:38429" "…/workflows"`
