# worktree-comparison-base Specification

## Purpose

worktree 任务工作区的比较派生面（提交列表、累计 diff、比较口径的 ahead/behind、原生 Review、任务摘要）默认以物化时记录的创建基为锚：母分支 ref（worktree 分支切出时实际解析的本地 ref）+ 分叉点 SHA。带创建基记录的任务只显示本 worktree 自己的工作；母分支改写或删除时回退记录的分叉点（fail-closed，不回退集成默认）；显式 comparison target 与手动「比较对象」选择仍优先，创建基为系统写入、非 sticky；存量任务不回填、行为不变。

## Requirements
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

## Known Failure Modes

| Symptom | Cause | Fix |
| --- | --- | --- |
| 单仓任务 launch 后创建基既不入库也不进首会话种子，Changes 面板看不到母分支口径 | 单仓 launch 走 legacy 字段路径绕过结构化 repo 列表，创建基在 `LaunchAgentResponse`/`EnvPrepareResult` 镜像链上断链 | 补齐镜像字段，`stashPreparedCreationBases` 把 preparer 的分叉点 stash 进请求 metadata，首会话在 DB 写入前拿到创建基（见 `internal/agent/runtime/lifecycle/manager_creation_base.go`） |
| agentctl 已捕获创建基但前端不显示 | status display 从 agentctl 捕获到组件约六层逐字段重映射（types→事件→WS payload→web 类型→store→hook），每层都要显式加字段，漏一层即丢 | 沿 comparison target 的既有镜像模式逐层补齐；新增状态字段时按该链路逐层核对 |

