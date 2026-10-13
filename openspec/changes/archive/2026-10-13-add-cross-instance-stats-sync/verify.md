# Verification Report

> 此文件由 verify 轮（`openspec-verify-change`）产生，记录漂移检测结果与处置。

**Change**: `add-cross-instance-stats-sync`
**Verified at**: `2026-10-13`
**Verifier**: openspec-verify-change（drift-detection subagent，main session 复核）

---

## 1. Structural Validation

- [x] `openspec validate add-cross-instance-stats-sync --strict --no-interactive` → valid（verify 改写 spec 场景后复跑仍 valid）

## 2. Task Completion

- [x] tasks.md 实现清单 T1–T9 全部勾选；ledger/index.md 九条 task 均记录验证方法与结果，Exit Gate 已填写。

## 3. Drift Detection（subagent 对照 proposal.md / design.md / tasks.md / spec delta）

**Status: Drift Found（1 处，已收口）→ 处置后 Clean**

- 8 条 Requirement、全部 Scenario、9 条 task、design D1–D10 逐条核实均有实现证据；ledger 三条实现备注（`v_stats_repositories` 第 8 视图、驼峰 YAML 键名、`EnsureViews` boot 末时机）独立核实论证成立；无 Out of Scope 项（无额外端点、无新依赖、无 UI/i18n/tombstone/反向同步/多远端 fan-in）。
- **唯一漂移**：「未映射 source」行为在变更文档内自相矛盾——spec 场景与 design D6 写「未映射即 403 拒绝」，proposal.md「workspace 映射」一节写「默认映射到用户主 workspace」；实现遵循 proposal（`service.go` resolveWorkspace 回退 `defaultWorkspace`，`backendapp/statsync.go:32-41` 恒定注入主 workspace 回退）。
- **用户裁决（2026-10-13）**：以实现/proposal 为准（方案 A）。specs/stats-sync/spec.md 场景改写为「未映射的 source 默认归入主 workspace」+「映射无法解析被拒绝」两条，design.md D6 与失败路径同步改写，ledger 增补 verify 裁决备注。**代码、测试、公开文档零改动。**

## 4. 低危观察（不构成漂移，未入档，供部署/后续参考）

- `handlers.go:51-54`：超 32MiB 请求体返回 413，是 `docs/public/stats-sync.md` 状态码表之外的防御性第五态，契约内发送方不可触发。
- `v_stats_git_snapshots` 已建但无查询消费（/stats git 区块只读 commits；峰值 LOC 走 ListSessionCodeStats 插件 API 且有意保持核心表）；design「区块→集成方式」表对 git 区块的描述与区块事实不完全相符，实现无害。
- `push.py` tasks 的 ephemeral/automation 过滤以 `"origin" in columns` 为门：REMOTE 老版本若缺 `origin` 列，过滤被整体跳过，此类实例的 ephemeral/automation 任务会被镜像计入。REMOTE 实际 schema 属开放事实，部署前建议确认。

## Overall Decision

- [x] ✅ PASS — 可进入 archive（validate strict valid；9/9 task 完成；唯一 drift 经用户裁决按方案 A 收口于文档侧）
