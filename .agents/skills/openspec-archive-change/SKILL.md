---
name: openspec-archive-change
description: >
  Use when a change has been implemented, verified, and its tasks are all
  checked, ready to be archived. Runs as a convergence ceremony: warn-level
  preconditions, mechanical merge via `openspec archive --yes`, then
  bidirectional active-layer convergence (add-pass by four admission
  criteria, prune-pass sweep, budget from config), then capability routing
  with specs.domains upkeep and supersede markers on superseded changes.
---

# OpenSpec Archive（收敛仪式）

把归档执行为收敛仪式：spec 快照由 CLI 机械合并，重头是**活跃层双向收敛**与**路由归位**。克制：可推导即不写，决策不进 spec（决策写入项目 adr 目录（默认 `openspec/adr/`，解析规则见 adr capability））。

## Step 1：前置（warn 级）

```bash
openspec validate <change-id> --strict --no-interactive
# 并确认 tasks.md 已全部勾选
```

- 任一未满足 → **警告并请求用户确认**，确认后仍可继续（软门槛，不硬阻塞）。
- 不做任何 verify 产物检查（verify 无文件输出；门禁归 tasks 勾选与 validate）。

## Step 2：机械合并

```bash
openspec archive <change-id> --yes
```

CLI 自动把 delta 合并进 `openspec/specs/<capability>/spec.md` 并移入 `changes/archive/`。仅当 CLI 合并失败才手工兜底，规则一行：优先合并到已有 capability，不轻易新建顶层目录。不把项目 adr 索引或 adr 目录移入 archive 目录。

## Step 2.5：扫描持久产品文件中的 change 路径指针

对本 change 触及的持久产品文件扫描 `openspec/changes/` 路径指针。范围取相对默认分支的 `git diff --name-only`，或 proposal Impact 列出的持久文件；排除 skill、command、`workflows/`、`schema-assets/`、以及当前 change 工作集。命中则改成项目 adr 索引或正文引用（措辞可操作：写明何时读、读来做什么），或改成自包含 how-to 并删除该指针。不做全仓库文档巡检。

## Step 3：活跃层双向收敛（自评）

逐项自评：**命中才询问用户，未命中一行带过**（如「KFM 无新增、活跃层无准入、预算 87/200」）。预算读 `openspec/config.yaml` 的 `active_layer.budget`，无该键时缺省 200。

**add-pass**：本次变更的候选全局约束逐条过准入判据——

- a. **四判据**：① 同一类问题第二次出现；② review/归档时发现 agent 本应从代码库或既有规范知道的约束；③ 新协作者需要同样上下文才能正确工作；④ 约束全局、跨会话、可验证。叠加「可推导即不写」：能从代码库推导的内容不进活跃层。
- b. **内容清单**：候选须属四类常驻判断之一——全局不变量 / 非默认约定 / 已验证的坑 / 路由地图；不在清单内（可推导的真相、场景与多步流程、背景与理由、细节堆叠、局部规则）不写入。
- c. **分层路由**：多步流程 → 对应 skill；局部规则 → 对应 capability spec；背景与理由 → 项目 adr 目录；全过 a、b 的全局约束 → 写入 `AGENTS.md` 和/或 `CLAUDE.md` 对应章节（并入既有章节，不建平行章节）。
- d. **supersede**：写入时并入已有对应章节；spec 中被取代的条目在原位做行内 supersede 标记并指向替代者，不删除历史。
- f. **被否增补记 KFM**：用户在本项否决的增补，追加到对应 capability spec 的 `## Known Failure Modes` 表（`| Symptom | Cause | Fix |`）防重复提出。

**写入前 no-op 检查（适用本仪式全部产出：活跃层增补、KFM 行、指针改写、报告）**：逐句检查——删掉该句 agent 行为不变 → 整句删（不删词）；同一事实与既有表述重复 → 合并，不写平行句；只删句、并表述，不改语义与准入/路由结论。优先使用专用写作优化 skill（如 aihero `/writing-for-agents`，宿主可见时调用）；不可见时按本判据内联执行，不阻塞。

**prune-pass**：主动扫三类，命中即删或降层（降层去向 = 项目 adr 目录，不新建 spec 章节）——

1. 被本次变更推翻的约束
2. 代码已演进到「可推导」的约束
3. 重复或互相平行的章节

**预算**：活跃层任一文件接近/超过 `budget`（软预算 200 行）→ 触发裁剪，把不再「必须始终活跃」的约束降层。

**KFM**：本次有踩坑（What Didn't Work）→ 追加对应 capability spec 的 `## Known Failure Modes` 表行（`| Symptom | Cause | Fix |`）。该表是坑台账（检索辅助），不是真相层。

## Step 4：路由归位与 supersede

- 读 `config.yaml` 的 `specs.domains` 定位本变更 capability 的域；**新建 capability 时必须同步在 `domains` 增行**（新建门槛：无法装入任何既有 capability 才允许）。
- 域漂移（capability 归属变化）→ 在 `domains` 数组间挪名。
- 本次归档**推翻了某已归档 change 的决策** → 在该 change 的 `proposal.md` 头部追加 `> Superseded by: <slug>`（不改写原文）。
- 不创建 ADR 目录；不读取仓库级 ADR 目录偏好。

## Step 5：报告

- archive 路径
- 合并到了哪些 capability
- 活跃层净变化（如 +2/−1，净 −N 行）
- supersede 动作（如有）
- 若本 change 有人读决策：确认 apply 已写对应正文并更新项目 adr 索引；未写则记录必须经 apply 补写，不创建 ADR 目录
