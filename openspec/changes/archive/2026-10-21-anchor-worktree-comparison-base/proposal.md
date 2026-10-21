## Why

任务工作区以 git worktree 形式物化时，「更改」面板及其全部比较派生面（提交列表、累计 diff、比较口径的 ahead/behind、原生 Review、任务摘要）用 merge-base(HEAD, origin/&lt;base_branch&gt;) 作为比较基：origin-first 取基是 agentctl 的在案设计意图（注释明说 origin 是 upstream source of truth、远端 ref 锚定 live divergence）。但当 worktree 的母分支（切出时所依据的本地分支）领先其远端对应分支时——实测本地 main 领先 origin/main 39 个提交（「推送 39」截图），提交列表混入母分支自己的 popo/openspec 提交——用户看到的不是「本 worktree 自己的工作」。本 change 把 worktree 任务工作区的比较默认基从「origin merge-base」改为「物化时记录的创建基（母分支 + 分叉点）」，属于产品级默认值变更，同时保留显式比较目标与手动选择的既有优先级和逃生口。

## What Changes

**创建基（creation base）记录**

- From: worktree 物化时不记录切出点；子任务继承父的 `base_branch`、有意丢 `CheckoutBranch`（两 worktree 不能共享工作分支），母分支对子任务不落库；最近的 `worktree_integration_ref` 只服务终端清理，均非创建基记录。
- To: 物化时按 repo 记录创建基：母分支名（worktree 分支切出时解析的本地 ref，本地 main 场景即 `main`）+ 分叉点 SHA（切出时该 ref 的 tip）；记录随 worktree 恢复/重建保留或重锚。
- Reason: 分叉点是「只看本 worktree 工作」的唯一权威锚点，事后不可追溯；不记录则任何默认值都只能退回 origin-first。
- Impact: 新增持久化记录，非 breaking；无需 SQL migration（走 `task_repositories.metadata`，有 `comparison_target` 先例）。

**比较默认值**

- From: agentctl 取基优先级为 显式 comparison target → `target_branch` merge-base（origin-first：`origin/<branch>` 优先、失败回退本地）→ 存储 `since`；`CorrectStaleComparisonBase` 把陈旧 base 重锚到 `origin/main|master` 集成基；最终 `--first-parent <base>..HEAD`。
- To: 带创建基记录的 worktree 任务工作区，比较默认取 merge-base(HEAD, 母分支)，按本地 ref 解析、**不做 origin-first 改写**；母分支不可解析或活动 merge-base 退回到记录分叉点之前（母分支被 rebase/改写）时，回退记录的分叉点 SHA；绝不静默扩展回集成默认（`origin/main` 等）。
- Reason: 本地 main 领先远端正是用户实测症状的根因；母分支推进时 merge-base 天然稳定在分叉点，rebase 到新母分支 tip 后范围正确收缩到 rebase 后的工作。
- Impact: 提交列表、累计 diff、比较 ahead/behind、原生 Review、任务摘要一次性换默认基；记录必须作为一等「本地 ref 默认值」进入 agentctl 比较解析（与 fork-PR comparison target 同一权威面），具体载体（session 字段 vs tracker map）由 design 决定。

**优先级与共存**

- 最终优先级：显式 comparison target（fork PR 流）> 手动「比较对象」picker 选择（sticky，用户权威）> 创建基默认（系统写入、非 sticky）> 现行 origin-first 默认（无创建基记录的存量任务与非 worktree 场景）。
- 创建基复用 `UpdateRepositoryBaseBranchFromSystem` 的非 sticky 语义：系统侧刷新不得覆盖用户手动选择。

**关键假设（inline-direct，未逐条访谈）**

- 「只看本 worktree 自己的工作」的口径 = 自分叉点起的工作（merge-base 语义，与系统内所有其它比较基一致），而非 session 起 SHA（`base_commit_sha`）：后者在 rebase 后会把整个 rebase 显示为变更。分叉点 SHA 仅作历史改写时的回退锚，不作为日常默认。
- 母分支定义统一为「worktree 分支切出时实际解析的本地 ref」：子任务场景即继承 base_branch 的本地解析结果（今日子任务本就从与父相同的 base 切出）；不引入「父任务工作分支」作为子任务默认母分支——那会改变子任务面板相对父工作的口径，需要时可手动选择。

### 不做什么（Non-goals）

- 不改 `origin`、checkout 分支、upstream tracking、push 路由（fork-pr-comparison-targets plan 的 invariants 全部保留）。
- 不改 worktree 分支生成；创建基仅是比较 ref（git 约束：两 worktree 不能 checkout 同一分支，比较-only 引用合法）。
- 不改手动 picker 选择的 origin-first 解析语义：「picker 无法表达本地 main」的缺口由新默认值消除实际需求；改手动选择的解析是独立行为问题，影响全场景（含非 worktree），另立变更。
- 不改推送 badge 的 push 语义（分支 vs 自身上游的 ahead/behind）。
- 主工作区/非 worktree 任务场景不在范围。
- 不破坏 `docs/specs/platform/requirements/workspace-git-status.md` AC 001.16-001.34 的 fail-closed 规则；比较派生数据不可用时显式 unavailable，不伪装可用。

## Capabilities

### New Capabilities

- `worktree-comparison-base`：worktree 任务工作区的比较基默认取物化时记录的创建基（母分支 + 分叉点），使提交列表、累计 diff、比较 ahead/behind、原生 Review、任务摘要只显示本 worktree 自己的工作；含历史改写回退（fail-closed，不回退集成默认）、与显式比较目标/手动选择的优先级、系统写入非 sticky、存量任务不回填。

### Modified Capabilities

（无：`openspec/specs/` 既有 capability 均与本题无关，本 change 新建 capability。）

## Research Log

以下结论来自上一工作流步（Explore，已在本 worktree @2c1663810 逐点复核；完整原始调查见 `/tmp/handoff-worktree-comparison-base-2026-10-21.md`）。

### 未知 1：现状行为是否 bug、设计意图是否在案
- **验证方式**：读代码与注释。
- **结论（试通证据）**：origin-first 是在案设计意图，非实现缺陷。`computeMergeBase` 注释明说 origin 是 upstream source of truth、local ref 可任意滞后；取基优先级为显式 target → `target_branch` merge-base（无共同历史回退分支 tip）→ 存储 `since`；`CorrectStaleComparisonBase` 重锚陈旧 base 到集成基。
- **证据摘要**：`apps/backend/internal/agentctl/server/api/git.go:831-845, 875-950`；`apps/backend/internal/agentctl/server/process/comparison_base.go:22-49`；`apps/backend/internal/agentctl/server/process/git_log.go:129`。

### 未知 2：一个比较基驱动哪些面（影响面）
- **验证方式**：读消费方代码 + fork-pr plan 消费方清单。
- **结论（试通证据）**：commits、cumulative diff、ahead/behind、workspace status、原生 Review（`git.go:1153` 注释「matches the COMMITS panel」）、task summaries 共用同一基线，一次换基全部联动。
- **证据摘要**：`apps/backend/internal/agent/handlers/review_change_source.go:57-89`；`docs/plans/fork-pr-comparison-targets/plan.md`。

### 未知 3：母分支是否已有持久化、创建时丢什么
- **验证方式**：读子任务继承与 worktree 物化代码。
- **结论（试通证据）**：子任务继承 `BaseBranch`、有意丢 `CheckoutBranch`（UI 与 MCP 两路径同规则，sibling off same base 利 stacked PR）；母分支不落库；`worktree_integration_ref` 是物化时捕获的清理权威，非创建基记录。
- **证据摘要**：`apps/backend/internal/task/service/service_tasks.go:791-832`；`apps/backend/internal/mcp/handlers/handlers.go`（inherited repo inputs，~1441-1488）；`apps/backend/internal/worktree/worktree.go:101-103`。

### 未知 4：手动逃生口现状与缺口
- **验证方式**：读 picker → service → agentctl fan-out 链路。
- **结论（试通证据）**：「比较对象」picker 已联动一切（清 comparison-target override、重置全部 session base、发 `task.updated`、推 live agentctl，免重启）；但 origin-first 使选 `main` 仍解析 `origin/main`，本地 main 领先场景症状仍在；选无远端对应的分支（典型父 worktree 分支）今天可用。
- **证据摘要**：`apps/web/components/task/base-branch-picker.tsx`；`apps/backend/internal/task/service/service_branch_update.go:49-179`；`UpdateRepositoryBaseBranchFromSystem`（:53-58）区分系统侧与用户手动选择。

### 未知 5：相关在案边界与约束
- **验证方式**：读 plans / specs / ADR。
- **结论（试通证据）**：`subtask-new-workspace-branch-label/plan.md` 把「Changing the base branch sent during subtask creation」列为 out-of-scope——本 change 重开该边界，理由：不改「发送的 base_branch」（物化语义不动），只新增比较口径的创建基记录；ADR 0013 显示 `base_branch` 已语义重载（worktree 行分支锚点 + 唯一键成分），故不动它；fork PR 的 `comparison_target` metadata 机制与 AC 001.16-001.34 fail-closed 规则必须保留；`openspec/specs/` 无相关 capability，需新建。
- **证据摘要**：`docs/plans/subtask-new-workspace-branch-label/plan.md`（Out of scope）；`docs/decisions/0013-multi-branch-tasks.md`；`docs/plans/fork-pr-comparison-targets/plan.md`；`docs/specs/platform/requirements/workspace-git-status.md` AC 001.16-001.34。

## Impact

- **代码**：
  - agentctl 比较解析：`internal/agentctl/server/api/git.go`（取基优先级插入「本地 ref 创建基默认」层）、`internal/agentctl/server/process/comparison_base.go`（创建基场景的重锚/回退规则，不重锚到集成基）。
  - 创建基记录：`internal/worktree/manager_lifecycle.go`（物化时捕获母分支 + 分叉点）、`internal/worktree/worktree.go` / `store.go`（持久化与恢复）、`internal/task/service/`（metadata 读写、非 sticky 系统写入、picker 共存）。
  - session 基线与 fan-out：`internal/orchestrator/executor/executor_execute.go`（session 盖章取创建基）、`service_branch_update.go` 一族（优先级与清除语义）。
  - 前端：Changes 面板比较基展示（`apps/web/components/task/base-branch-picker.tsx` 及 changes 面板）显示母分支口径。
- **durable spec**：`docs/specs/platform/requirements/workspace-git-status.md`（比较口径行为，AC 001.16-001.34 fail-closed 保持）——按仓库规范在 plan/design 阶段更新 requirements/system-design；建议评估 ADR（反转 origin-first 在案决策，可用 `/record`）。
- **文档**：`docs/git-operations.md`、`docs/public/git-operations.md`（比较基语义）、`docs/public/coordination.md`（子任务基线叙述）——按 `/docs-maintainer` 评估。
- **测试**：后端单测 + 焦点回归（本地 main 领先 origin/main 复现「推送 39」混入 → 换基后只剩本 worktree 提交）；母分支 rebase/删除的回退与 fail-closed；fork-PR target 与手动选择优先级；创建基非 sticky；e2e 视 design 决定。
- **兼容**：存量任务不回填、行为不变；新任务自动获得新默认。
