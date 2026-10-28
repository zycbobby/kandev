# show-background-subagent-running — Verify

**Date:** 2026-10-28
**Result:** Drift Report: **Clean**（无未实现需求、无范围外新增、无设计偏差）

## 检测方式

- 派发只读漂移检测 subagent，对照 `proposal.md`、`design.md`、`tasks.md`、`specs/subagent-card-status/spec.md` 与 `git diff main...HEAD` 的 apps/web 完整 diff（1151 行），并按需读仓库当前源文件复核。
- 校准：只报真实漂移；风格/命名/locale 脚本生成形态差异不算；逐条 Scenario 找到 diff 对应行号才可判一致。
- 无未收束 review round（本 change 无 `reviews/` 目录）。

## 六项关键核对（全部一致）

1. **范围**：`apps/backend` 零文件；`apps/web` 仅 6 个代码文件 + 8 个 locale 文件，与 tasks.md Files 列表一致。
2. **设计决策 D1–D4**：`isSubagentBackgroundRunning` 谓词形状、`resolveSubagentDisplayStatus` 的 background → working → terminal 顺序（background 不受 turn 门控）、三分支 `SubagentDisplayStatus` 类型逐条吻合；Task 1 helper 搬移字节等价（仅新增 export）。
3. **布局零改动**：`autoExpanded = isActive`、meta 行 `!isActive` 显隐维持 turn 域语义（D2 解耦成立）。
4. **turn-group**：改动即 design 指定的同一谓词（`isSubagentEffectivelyActive || isSubagentBackgroundRunning`），仅封装为具名 helper `isSubagentMessageRunning`，语义等价（实现细节，非漂移）。
5. **i18n**：8 个 locale 全有 `statusBackgroundRunning`、值与 design 表一致、无 em dash、置于 `statusWorking` 邻近；新文案仅经 `t()`。
6. **禁止项**：无 `active_subagent_count` 归零翻转、无伪造 usage/outputFile、前端 terminality 解析未改。

## 测试覆盖

design.md「E2E 测试图」6 条用户路径 + tasks.md「Review Focus」5 条输入类均有真实对应用例（含「无翻转路径」纯谓词测试替代「永久停留」的 design 指定方案）；聚焦 vitest 3 文件 78/78 PASS（Apply 阶段 exit gate）。
