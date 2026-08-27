# Ledger: prefer-api-writes-in-debug-skill

> apply 阶段执行账本。执行器：`subagent-driven`（用户显式指定，非交互选择）。
> 变更类型：纯 skill 文档变更（`.agents/skills/debug-kandev/`），无生产代码。

## Per-Task Ledger

| task id | executor | 验证方法 | 结果 | 状态 | 时间戳 |
|---------|----------|----------|------|------|--------|
| 1.1+1.2+2.1+2.2（批量 dispatch，commit db38796ce） | subagent-driven（user-specified） | implementer 跑全部准出 grep + task reviewer 逐行 verbatim 比对（brief ↔ diff 字节级）；准出 1.2 的 `38429` 计数按 ruling 以意图核对（写策略章节外零出现；章节内回退行 L147 + 散文 L143） | 全部通过：章节计数各 1、位置锁定正确（68<136<172<192）、From 文本逐字替换、`REST 无鉴权` 消失、db-schema.md 仅尾部 +3/-0；review 结论 ✅ Spec compliant / Approved，零 Critical/Important | done | 2026-08-27 |
| 3.1 一致性 grep | subagent-driven（user-specified） | 按 tasks.md 3.1 命令组原样执行：REST 路由 grep×3 计数、20 个 MCP 工具名 for 循环、`38429` 位置归属 | 全部通过：路由 grep 计数各 1（≥1）；MCP 工具零 MISSING；`38429` 恰 2 行（L143 散文 + L147 回退行）均在「数据修改写策略」章节（L136-171）内、章节外零出现（符合 ruling） | done | 2026-08-27 |
| 3.2 冒烟闭环 | subagent-driven（user-specified） | 路径 1：`kandev-instances` → live 实例（PID 421415:38429）`/health`=200、`GET /api/v1/workflows` 合法 JSON（零副作用）；路径 2：`scripts/dev-isolated` 起隔离实例（:48429）→ PUT step `auto_advance_requires_signal:true`（响应 step "Backlog"）→ GET 复核 `.step.auto_advance_requires_signal`=true → teardown exit 0、`kandev-instances` 无残留、端口已关、无 pidfile | 全部通过：live 只读面可达；隔离写闭环经 API 完成并持久化；teardown 干净。备注：brief 中 GET 复核命令的 jq 路径 `jq .auto_advance_requires_signal` 因响应信封为 `{"step":{...}}` 返回 null，按 ruling 改用 `.step.` 前缀复核为 true（SKILL.md 不含该命令，非文档缺陷）；dev-isolated 首启因 mise shim 信任失败，换真实 Go 安装目录后成功（环境项） | done | 2026-08-27 |

## Exit Gate

> Step 4 准出门禁，三项逐项结论（apply 终态，2026-08-27）。

| # | 门禁项 | 结论 | 证据 |
|---|--------|------|------|
| 0 | `reviews/state.json` 无 collecting / applyBlocked | ✅ 通过 | 复查两次（apply 启动时 + exit gate 时）：`reviews/` 目录不存在，无 active review session |
| 1 | Verification Strategy 入口（3.1 + 3.2 命令组） | ✅ 通过 | 3.1：REST 路由 grep 计数各 1、20 个 MCP 工具名零 MISSING、`38429` 仅写策略章节内 2 行（L143 散文 + L147 回退行，ruling 见 Per-Task Ledger）；3.2a：live 实例（:38429）`/health`=200、`/api/v1/workflows` 返回 JSON；3.2b：dev-isolated 隔离实例（:48429）PUT step → GET 复核 `.step.auto_advance_requires_signal`=true（jq 路径 ruling 同上）→ teardown exit 0、`kandev-instances` 无残留 |
| 2 | `openspec validate prefer-api-writes-in-debug-skill --strict` | ✅ 通过 | 输出 `Change 'prefer-api-writes-in-debug-skill' is valid`，exit 0 |
| 3 | 验收方法 / 场景 / 结果 | ✅ 已提供 | 方法 = e2e-script 真实执行 skill 新章节命令路径 + 文档一致性 grep（本 ledger Per-Task Ledger 逐行记录）；场景 = E2E 图三条覆盖路径全部走通（live 只读冒烟、隔离实例写闭环、映射表事实核对）；结果 = 11/11 tasks.md 条目勾选，`git diff 4522efdee..db38796ce` 仅 `.agents/skills/debug-kandev/` 两文件 +62/-3 |

**护栏备注（`make -C apps/backend test`）**：套件在 `internal/worktree` 报 6 个 FAIL，A/B 对照证明为环境性 pre-existing——同一包在 BASE 提交（4522efdee，主 checkout）以完全相同错误签名失败（`Filename too long`、git 2.34.1 的 `isFetchRefusedCheckedOut` 措辞不匹配、prune exit 129）。本 change 前后套件结果零差异，护栏「零差异」语义成立；diff 范围证据（仅 2 个 markdown 文件）确认无后端代码被触碰。shell PATH 无 go（mise shim 不受信）属环境项，以 `~/.gvm/gos/go1.26.7/bin` 重跑取得上述结果。

三项门禁全部满足，apply 阶段退出条件达成。

## Final Whole-Branch Review（SDD 终审）

- 审查范围：`4522efdee..cfb7613a8`（本 apply session 全部 2 个 commit），最强模型独立复核
- 结论：**Ready to merge — Yes**；零 Critical / 零 Important；4 条 Minor 全部不阻塞
- 事实复核：映射表 12 个 REST 端点、20 个 MCP 工具名、`server.port` 默认值、`features.auth` 三 profile 默认关、`/health` 免鉴权、step 缓存 TTL 5s、parity 测试、`task.plan.*` WS 动作——逐项对照代码全部成立
- 4 条 Minor 交接（详情与处理建议见 `.superpowers/sdd/tasks/progress.md` 终审段）：
  1. spec delta `specs/runtime-debugging/spec.md:12`「etc 价」→「等价」（apply 禁改 spec delta，留待 archive 修）
  2. SKILL.md 速查出处注可补 `workflow/handlers/workflow_handlers.go`
  3. SKILL.md 多实例行选注释可点名 `HOME_DIR`/`REPO_PATH` 列（与 spec scenario 对齐）
  4. SKILL.md `GET /api/v1/workflows` 行可注明默认 exclude_office
- 全部 controller rulings 经终审技术复核认可

下一步：`openspec-verify-change`（漂移检查）。
