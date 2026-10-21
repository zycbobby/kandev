# worktree-comparison-base

## 背景

任务工作区以 git worktree 形式物化。现行比较基默认是 merge-base(HEAD, origin/&lt;base_branch&gt;)（origin-first 在案设计意图），导致母分支（worktree 分支切出时依据的本地分支）领先其远端对应分支时，提交列表、累计 diff、比较 ahead/behind、原生 Review、任务摘要混入母分支自己的提交（实测本地 main 领先 origin/main 39 个提交，「推送 39」、popo/openspec 提交混入）。用户预期：worktree 的比较派生面只显示本 worktree 自己的工作。

本 capability 新增「创建基」概念：物化时记录母分支名与分叉点 SHA，作为 worktree 任务工作区比较默认值的权威锚点，并定义其解析、回退、优先级与兼容规则。

## 方案与决策

- **创建基定义**：母分支 = worktree 分支切出时实际解析的本地 ref（子任务场景即继承 `base_branch` 的本地解析结果；本地 main 场景即 `main`）；分叉点 = 切出时该 ref 的 tip SHA。
- **持久化**：`task_repositories.metadata` 新记录，不改 `base_branch`（ADR 0013 语义重载：worktree 行分支锚点 + 唯一键成分，且驱动物化/清理与文档化 push-rebase-merge 语义）；无需 SQL migration（`comparison_target` 先例）。
- **比较默认值**：merge-base(HEAD, 母分支)，按本地 ref 解析、不做 origin-first 改写。母分支推进时 merge-base 稳定在分叉点；rebase 到新母分支 tip 后范围收缩到 rebase 后的工作。
- **回退规则（fail-closed）**：母分支不可解析或活动 merge-base 退回到记录分叉点之前（历史改写）时，用记录的分叉点 SHA；记录 SHA 也不可解析时比较派生数据显式 unavailable，绝不静默扩展回集成默认（`origin/main`、`origin/master`、本地 `main`、本地 `master`）。不重锚到集成基（区别于现行 `CorrectStaleComparisonBase` 的通用策略）。
- **优先级**：显式仓库限定 comparison target（fork PR 流）> 手动「比较对象」picker 选择（sticky，用户权威，选择即清除系统默认口径）> 创建基默认（系统写入、非 sticky，复用 `UpdateRepositoryBaseBranchFromSystem` 语义）> 现行 origin-first 默认（无创建基记录的存量任务与非 worktree 场景）。
- **比较-only**：创建基不改变 `origin`、checkout 分支、upstream tracking、push 路由、worktree 分支生成、终端清理语义。git 约束：两 worktree 不能 checkout 同一分支，比较-only 引用合法。
- **存量兼容**：不回填、不迁移；存量任务保持现行行为，手动 picker 仍是逃生口。
- **fail-closed 家族保留**：`docs/specs/platform/requirements/workspace-git-status.md` AC 001.16-001.34 的不可用显式化、非交互凭证、命令时限等规则全部适用。

## ADDED Requirements

### Requirement: 记录 worktree 创建基

When Kandev materializes a task workspace worktree for a repository, it SHALL record the creation base: the mother branch name (the resolved local ref the worktree branch was cut from) and the fork-point commit SHA.

#### Scenario: 物化时记录母分支与分叉点
- **WHEN** a worktree branch is cut from a local `main` whose tip is ahead of `origin/main`
- **THEN** the recorded creation base names `main` as the mother branch and records the local tip SHA as the fork point

#### Scenario: 恢复与重建后记录仍在
- **WHEN** the worktree is recovered or re-materialized (branch cleanup or recovery head)
- **THEN** the creation base record survives, or is re-anchored to the same mother branch, and is not silently dropped

### Requirement: 创建基作为比较默认值

For a task workspace materialized as a worktree with a recorded creation base, comparison-derived surfaces — commit list, cumulative diff, comparison-derived ahead/behind totals, native Review, and task summaries — SHALL default to merge-base(HEAD, mother branch), resolved against the local ref without origin-first rewriting.

#### Scenario: 母分支领先远端时只显示本 worktree 的工作
- **WHEN** the mother branch is a local branch ahead of its remote counterpart (e.g. local `main` ahead of `origin/main` by 39 commits)
- **THEN** the commit list shows only commits produced in this worktree, not the mother branch's own commits

#### Scenario: 母分支推进时默认值稳定
- **WHEN** the mother branch gains new commits after this worktree was cut
- **THEN** the default comparison base remains the fork point (the merge-base does not move)

#### Scenario: rebase 到新母分支 tip 后范围收缩
- **WHEN** the worktree branch is rebased onto a newer mother-branch tip
- **THEN** the comparison range shrinks to the worktree's post-rebase work instead of showing the whole rebase

### Requirement: 历史改写时的回退规则

When the mother branch ref no longer resolves, or the live merge-base regresses behind the recorded fork point (history rewrite or rebase of the mother branch), the default comparison SHALL use the recorded fork-point SHA. It SHALL NOT silently expand to an integration default (`origin/main`, `origin/master`, local `main`, local `master`).

#### Scenario: 母分支被 rebase 或删除
- **WHEN** the mother branch has been rebased so the recorded fork point is no longer on its history, or the ref is deleted
- **THEN** the comparison uses the recorded fork-point SHA

#### Scenario: 记录不可解析时显式不可用
- **WHEN** the recorded fork-point SHA can no longer be resolved in the repository
- **THEN** comparison-derived data reports an explicit unavailable state instead of falling back to an integration default

### Requirement: 与显式比较目标及手动选择的优先级

An explicit repository-qualified comparison target (fork PR flow) SHALL remain authoritative over the creation-base default. A manual **Compare against** selection SHALL override the creation-base default and mark the row as user-selected. The creation-base default is system-authored and non-sticky: a later system-side update SHALL NOT replace a user's manual selection.

#### Scenario: fork PR 比较目标优先
- **WHEN** a repository-qualified comparison target is attached to the task repository
- **THEN** all comparison-derived surfaces use it, ignoring the creation-base default

#### Scenario: 手动选择覆盖且不被系统写回
- **WHEN** the user picks a branch in **Compare against**, and the system later refreshes the creation-base record
- **THEN** the user's selection stays authoritative

### Requirement: 创建基仅用于比较

The creation base SHALL NOT change `origin`, the checkout branch, upstream tracking, push routing, worktree branch generation, or terminal cleanup semantics. Two worktrees MAY reference the same mother branch as a comparison-only ref.

#### Scenario: 换默认基不影响推送与检出
- **WHEN** the default comparison base changes to the mother branch
- **THEN** push destinations, upstream tracking, and the worktree's own branch remain unchanged

### Requirement: 存量任务保持现行行为

Tasks and workspaces without a recorded creation base SHALL keep the existing comparison behavior. No migration or backfill SHALL be performed.

#### Scenario: 存量任务不回填
- **WHEN** an existing task workspace has no recorded creation base
- **THEN** its comparison base resolution is unchanged, and the manual picker remains the escape hatch
