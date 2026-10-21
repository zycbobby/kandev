# Design: anchor-worktree-comparison-base

## Context

worktree 任务工作区的比较派生面（commits、cumulative diff、比较 ahead/behind、原生 Review、task summaries）目前默认用 merge-base(HEAD, origin/&lt;base_branch&gt;)（origin-first 在案设计意图）。母分支领先远端时面板混入母分支自己的提交（实测「推送 39」）。本 change 新增「创建基」：物化时记录母分支 ref + 分叉点 SHA，作为比较默认值的权威锚点。行为契约见 `specs/worktree-comparison-base/spec.md`；六项产品裁决见 `proposal.md`。

本 design 的全部结论来自本 worktree 的代码级勘探（见 Research Log）；无外部依赖（无第三方 API / 外部 CLI / agent runtime 行为参与本变更的验收）。

## Goals / Non-Goals

**Goals:**

- 物化时按 repo 捕获创建基（母分支 ref 原样 + 分叉点 SHA），持久化于 `task_repositories.metadata`。
- agentctl 全部比较面在一次取基中换默认：显式 comparison target > 创建基 > 现行 origin-first。
- 历史改写 fail-closed：母分支被 rebase/删除 → 回退记录分叉点；分叉点也不可解析 → 显式 unavailable；绝不静默扩展回集成默认。
- 存量任务零行为变化；手动 picker 语义（sticky、origin-first）不变。

**Non-Goals（继承 proposal）：**

- 不改 origin / checkout 分支 / upstream tracking / push 路由 / worktree 分支生成 / 推送 badge 的 push 语义。
- 不改手动 picker 的 origin-first 解析（另立变更）。
- 主工作区 / 非 worktree 场景不纳入。
- 不破坏 `docs/specs/platform/requirements/workspace-git-status.md` AC 001.16-001.34 的 fail-closed 规则。

## Decisions

### D1. 载体：agentctl tracker 侧 per-repo 创建基 map（否决 session 字段）

创建基作为一等默认值进入 tracker，镜像 base-branch / comparison-target 两条既有链路。**否决 session 字段盖章**：tracker 的 status / ahead-behind / `ResolveBaseAnchor`（multi-repo commits/diff）路径完全不读 session 字段——只有 request 驱动的 commits/cumulative-diff 会动，面板内各面会漂移。tracker map 一次覆盖所有面。

### D2. 持久化：`task_repositories.metadata["creation_base"]`

`models.CreationBase{Version: 1, Ref, ForkSHA}` + `Put/Load/Remove`，逐行镜像 `models/comparison_target.go`（版本化、校验、保留无关 metadata）。无需 SQL migration。与 `comparison_target`、`manual_base_branch_override` 同居一个 metadata map，durable 优先级一次读取即可裁决。`Ref` 记录物化时实际解析的 ref **原样**（本地 `main` 就是 `main`；remote-only base ref 场景就是 `origin/develop`——origin-first 禁改写指「不得把本地名改写成 origin/ 名」，记录原样即正确）。

### D3. 优先级在 backend 投影时裁决（agentctl 保持简单）

`manual_base_branch_override` marker **已存在**（`models/comparison_target.go:16,29-32`：手动选择 task repo 比较分支后留下的 sticky 标记，`UpdateRepositoryBaseBranch(manualSelection=true)` 写入、`FromSystem` 不写）。CreationBaseProvider 收集时跳过带 `comparison_target` 或 `manual_base_branch_override` 的行 → 推给 agentctl 的 map 天然只含「实际生效的默认」。agentctl 侧优先级简化为：显式 comparison target > 创建基 > baseBranches origin-first。sticky / 非 sticky 语义零新增代码。

### D4. 捕获点：`createNewBranchRef`（worktree manager）

`manager_lifecycle.go:1776-1807` 的 `createNewBranchRef` **已经**执行 `rev-parse --verify <baseRef>^{commit}` 拿 OID（正是分叉点）。扩展 snapshot 携带母 ref + OID → `Worktree` → `env_preparer_worktree.go:514` 的 `RepoWorktreeResult` 增两字段 → `executor_execute.go`（`buildTaskEnvironmentRepos:3942` 同一持久化点旁）经新的 repo 层 merge-preserving 写入方法落到 `task_repositories.metadata`。仅**新建分支**时捕获（`tryReuseExisting` 复用不重记录——原始分叉点仍是正确锚；重物化重新走 `createNewBranchRef` → 覆盖更新 = spec 的「重锚不丢弃」）。

### D5. agentctl 解析：`ResolveCreationBaseAnchor`（本地、无远端物化）

tracker 增加 `SetCreationBase(*models.CreationBase)` + 使用时解析：

```text
anchor = merge-base(HEAD, Ref)            # Ref 原样，不做 origin-first
若 Ref 不可解析           → anchor = ForkSHA
若 merge-base 严格落后于 ForkSHA（IsAncestor(mb, Fork) 且 mb ≠ Fork，母分支被改写）→ anchor = ForkSHA
若 anchor 不可解析        → 有界错误码 creation_base_ref_unavailable（比较派生面显式 unavailable；文件状态面不受影响）
不调用 CorrectStaleComparisonBase（不重锚到集成默认）
```

（`IsAncestor` 检测母分支改写与既有 `CorrectStaleComparisonBase` 的 `correctStaleComparisonBase` 同一手法，`comparison_base.go:44`。）

三个插入点，全部在「显式 comparison target 之后、branch-only 解析之前」：
1. tracker status / ahead-behind：`workspace_git_status.go` 的 base 解析（comparison target 优先检查在 :468/:703，创建基层插在其下）；
2. `git.go:875-950` `runGitLogForRepo`（commits）；
3. `git.go` `runGitCumulativeDiffForRepoResolved`（~:1151，注释「matches the COMMITS panel」）。

无 pending 状态（comparison target 需要 pending 是因为远端 fetch 异步；创建基是本地 ref，同步解析即可）。

### D6. agentctl 管道：镜像 comparison-target 链（但无远端物化）

| 层 | comparison-target 先例 | 创建基镜像 |
|---|---|---|
| 实例配置 | `config.go:372-375` `ComparisonTargets` map | `CreationBases map[string]models.CreationBase` |
| 创建请求 | `instance/instance.go:229` | `creation_bases` 字段 |
| 进程管理 | `instance/manager.go:305,384` → `PrepareComparisonTargets` | `UpdateCreationBases`（stamp 全部 tracker + 后台 refresh，模式照 `manager.go:1272 UpdateBaseBranches`；新 tracker spawn 时 `lookupCreationBase`，照 `manager_submodules.go:88`） |
| 控制路由 | `api/server.go:171` POST `/workspace/comparison-targets` | POST `/workspace/creation-bases` |
| 远端物化 | compare-* remote fetch | **无**（本地 ref） |

`models.CreationBase` 放 `internal/task/models`（agentctl 已 import 该包：`config.go:375` 用 `models.ComparisonTarget`，先例成立）。

### D7. lifecycle 管道：镜像 `manager_comparison_targets.go`

新文件 `manager_creation_base.go`：`CreationBaseProvider`（task service 注入）、launch 前 seed（`manager_launch.go:1390-1396` 旁加 `MetadataKeyCreationBases = "creation_bases"`）、readiness 推送、`PushCreationBasesForTask`（live 更新，照 `manager_base_branches.go:95`）。backendapp 装配 provider（照 comparisonTargetPusher 接线）。

### D8. live 更新：`applyBaseBranchSideEffects` 追加创建基推送

`service_branch_update.go:153-179` 在既有 base-branch / comparison-target 推送后追加创建基推送：手动选择后该 repo 行带 marker → provider 收集时跳过 → agentctl map 替换后该 tracker 丢创建基 → 回落到（新选的）baseBranch 的现行 origin-first 解析。即「手动选择覆盖且不被系统写回」零专门代码。

### D9. 前端：状态载荷携带创建基显示字段

镜像 comparison target 的 DTO/web-type 路径：`GitStatusUpdate` 增创建基 display + error code 字段 → web 类型 → Changes 头部（`changes-panel-header-actions.tsx:220-287`，`baseBranchDisplay` / `ComparisonTargetDisplay` 同一渲染区）显示生效比较基（母 ref 名）。i18n 走 7 语言目录（`pnpm run i18n:check` / `i18n:zh-hant`）。

### D10. ADR + durable spec

反转 worktree 场景的 origin-first 默认是在案设计决策的反转 → 记 ADR（`docs/decisions/`，英文）。durable spec：`docs/specs/platform/requirements/workspace-git-status.md` 增 ACs + `docs/specs/platform/system-design/workspace-git-status.md` 增设计段（requirements/system-design 分立的仓库规范）。

## Research Log

### 未知 1：创建基进 agentctl 的载体（session 字段 vs tracker map）
- **验证方式**：读 agentctl 比较面消费代码，确认各面数据来源。
- **结论（试通证据）**：status/ahead-behind/multi-repo commits/diff 走 tracker（`ResolveBaseAnchor`、`resolveBaseBranch`），commits/cumulative-diff 走 HTTP request（`runGitLogForRepo` 的 `req.TargetBranch` 来自 session）——session 字段只动后者，面板漂移；tracker map 全覆盖。
- **证据摘要**：`workspace_git_status.go:676-699`（`ResolveBaseCommit`/`ResolveBaseAnchor`）；`git.go:900-949`（req.Since/TargetBranch）；`manager.go:1263-1306`（UpdateBaseBranches stamp+refresh 模式）。

### 未知 2：手动选择的 sticky 机制是否已存在
- **验证方式**：读 `models/comparison_target.go` 与 base 更新服务。
- **结论（试通证据）**：已存在。`ManualBaseBranchOverrideMetadataKey`（:16）+ `HasManualBaseBranchOverride`（:29-32，注释明说 marker 在清除 provider target 后仍保留、launch 时 PR 刷新不能覆盖）；`UpdateRepositoryBaseBranchFromSystem`（`service_branch_update.go:53-58`）系统侧不写 marker。优先级机制零新增。
- **证据摘要**：如上行号；`UpdateTaskRepositoryBaseBranchAndClearComparisonTarget(ctx, id, branch, manualSelection)`（:79-84）。

### 未知 3：分叉点捕获是否有现成解析点
- **验证方式**：读 worktree 分支创建代码。
- **结论（试通证据）**：`createNewBranchRef` 已 `rev-parse --verify <baseRef>^{commit}` 取 OID（:1784-1792），即分叉点；母 ref 即传入的 baseRef。
- **证据摘要**：`manager_lifecycle.go:1776-1807`；`RepoWorktreeResult` 构造 `env_preparer_worktree.go:514`；orchestrator 持久化点 `executor_execute.go:3942`。

### 未知 4：agentctl 侧优先级插入点是否都先查显式 target
- **验证方式**：读三处比较入口。
- **结论（试通证据）**：`runGitLogForRepo` 先 `comparison.Explicit`（`git.go:889-898`）；status 捕获先 `ComparisonResolution`（`workspace_git_status.go:468,703-704`）——创建基层插入其下即得优先级。
- **证据摘要**：如上行号。

### 未知 5：前端生效比较基显示位
- **验证方式**：读 Changes 头部组件。
- **结论（试通证据）**：头部已有 `baseBranchDisplay` 与 `ComparisonTargetDisplay`（fork-PR 加的）两个渲染位，创建基 display 插同一区。
- **证据摘要**：`changes-panel-header-actions.tsx:220,287`。

### 未知 6（依赖准出门禁）：是否存在仓库外依赖
- **验证方式**：枚举本变更触达面（git 子进程、SQLite、WS/HTTP、Playwright）。
- **结论（拒绝理由 / 无外部依赖）**：无第三方 API、无新外部 CLI（git 是核心依赖且已有分类命令通道 `subproc`）、无 agent/runtime 行为参与验收。git merge-base 语义（改写历史后 merge-base 落后于旧 tip）为标准语义，且仓库内 `CorrectStaleComparisonBase` 已用同一 `IsAncestor` 手法处理同类场景。
- **证据摘要**：`comparison_base.go:29-49`。

## 测试与验证策略概览

- **核心准入**：Go integration 矩阵（agentctl `process` 解析 + `api` 两入口 + task service 投影/优先级 + lifecycle 接线），覆盖 spec 全部 Scenario。
- **UI 准出**：Playwright e2e（desktop + mobile parity）复现「本地 main 领先 origin/main」症状并断言换基后只剩本 worktree 工作。
- 详见 `tasks.md` 的 Verification Strategy 与 E2E 测试图。
