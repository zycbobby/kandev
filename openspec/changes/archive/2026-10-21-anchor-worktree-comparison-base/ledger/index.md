# Ledger: anchor-worktree-comparison-base

## Per-Task Ledger

| task id | executor | 验证方法 | 结果 | 状态 | 时间戳 |
|---------|----------|---------|------|------|--------|
| 1.1 | inline (user-directed) | `go test ./internal/task/models/ -run CreationBase -v`：往返/JSON 再水化/未知 Version/危险 ref（空、`..`、`.lock`、HEAD）/坏 SHA/origin/ 前缀合法/SHA-256 合法/Remove 幂等 | 11 例全绿 | done | 2026-10-21T15:00+08:00 |
| 1.2 | inline (user-directed) | `go test ./internal/task/repository/sqlite/ -run CreationBase -v`：写读往返+无关 key 与 manual marker 保留+分支列不动、重物化覆盖更新、缺行报 not found、nil 拒绝；`go build ./internal/task/...` + gofmt 通过 | 3 例全绿 | done | 2026-10-21T15:00+08:00 |
| 2.1 | inline (user-directed) | `go test ./internal/worktree/ -run CreationBase -v`：新建分支 Worktree 带母 ref `main` + main tip SHA、复用路径在母分支推进后不重记录（锚不变或空）；签名传播触及 8 个 gitAddWorktree 调用点 + 3 个测试调用点，`go build ./internal/...` 通过 | 2 例全绿 | done | 2026-10-21T15:20+08:00 |
| 2.2 | inline (user-directed) | `go test ./internal/orchestrator/executor/ -run CreationBase -v`：recorded 载荷 = Version/Ref 原样/ForkSHA、复用行跳过、recorder 失败不阻断后续行、nil 依赖 no-op；executor 窄接口 `TaskRepositoryCreationBaseRecorder` + backendapp `taskCreationBaseRecorder` 装配；回归对比（executor 34 个既有失败 / lifecycle 23 个既有失败，全部为本机环境问题——`/var` symlink 与 checkout proof——与 2c1663810 基线失败集完全一致，零新增零掩盖） | 3 例全绿，零回归 | done | 2026-10-21T15:25+08:00 |
| 3.1 | inline (user-directed) | `go test ./internal/task/service/ -run CreationBase -v`：优先级过滤（普通行进入 map（根别名+tracker 键）、comparison_target 行跳过、manual marker 行跳过）、无记录投影空 map 空任务 ID 不报错 | 3 例全绿 | done | 2026-10-21T15:40+08:00 |
| 3.2 | inline (user-directed) | `TestManualBaseBranchSelectionPushesCreationBaseWithoutTheRow`：手动选择后第二次推送为空 map（tracker 丢创建基回落）；补 `applyBaseBranchSideEffects` 尾部 `pushTaskCreationBases`；handlers 测试 mock 补新接口方法 stub；service 包回归与基线失败集（41 个环境失败）零差异 | 1 例全绿，零回归 | done | 2026-10-21T15:40+08:00 |
| 4.1 | inline (user-directed) | `go build ./internal/agentctl/... ./internal/agent/runtime/...`；config/instance/manager 三层字段+clone+传递（lint dupl/goconst 修复：client 侧提取 `postWorkspaceControlJSON` 共享助手、api 侧 `errInvalidJSONBody` 常量） | 编译通过 | done | 2026-10-21T16:10+08:00 |
| 4.2 | inline (user-directed) | `go test ./internal/agentctl/server/process/ -run CreationBase -v`：root stamp+清空、cfg 持久化+subpath 键+空键回退、map 整体替换 | 3 例全绿 | done | 2026-10-21T16:10+08:00 |
| 4.3 | inline (user-directed) | `go test ./internal/agentctl/server/api/ -run CreationBase -v`：非法 JSON/危险 key/坏 base 400、合法 map 200+tracker 收到、空 body 清空；agentctl 三包全量回归与基线失败集零差异（api 3 个 + process 1 个既有失败均为环境性） | 2 例全绿，零回归 | done | 2026-10-21T16:10+08:00 |
| 5.1 | inline (user-directed) | `creation_base_resolution_test.go`（process 包）D5 全矩阵：母分支领先锚=分叉点、母分支推进锚稳定、rebase 后范围收缩、母分支删除→ForkSHA、改写 merge-base 落后→ForkSHA、ForkSHA 丢失→`creation_base_ref_unavailable`、`origin/` 前缀原样解析、无记录不解析 | 8 子例全绿 | done | 2026-10-21T16:30+08:00 |
| 5.2 | inline (user-directed) | 三插入点：`ResolveBaseCommit/ResolveBaseAnchor/resolveAheadBehindCompareRef` 创建基层（显式 target 之后）、`runGitLogForRepo`/`runGitCumulativeDiffForRepoResolved` 同构插入（软假设确认：两函数 `comparison.Explicit` 块后同构）、multi-repo `resolvePerRepoBaseAndScope` 跳过 stale 重锚；`TestResolveBaseCommitFlipsFromOriginFirstToCreationBase` 复现并反转「推送 39」、`git_creation_base_test.go`（api 包）commits=1/diff 仅本 worktree 文件/unavailable fail-closed；streams DTO 增 `creation_base`/`creation_base_error_code`；submodule 守卫 | 矩阵+反转+fail-closed 全绿，既有 BaseBranch/ComparisonEvidence 回归全绿 | done | 2026-10-21T16:30+08:00 |
| 6.1 | inline (user-directed) | lifecycle：`stashPreparedCreationBases`（preparer 分叉点 stash 到请求 metadata，首会话在 DB 写入前拿到创建基）+ `collectLaunchCreationBases`（durable 投影 merge 其下，新观察胜出）+ `creationBasesFromMetadata`（resume 解码）+ 5 个后端拷贝点 + container/agentctl CreateInstanceRequest/ContainerConfig 字段 + 3 个 readiness 推送点；测试：readiness 推送 6 子例（含空 map 不推送照 `pushTaskBaseBranches` 空保护）、live 推送仅目标任务+空 taskID+best-effort、stash 键约定（根键/跳过复用）、merge 语义（durable/新观察胜出/JSON 再水化/坏值 fail-closed/空 nil） | 全绿，lint 0 issues | done | 2026-10-21T16:50+08:00 |
| 6.2 | inline (user-directed) | backendapp：`provideLifecycleManager` 增 `services.Task.TaskCreationBases` provider 参数 → `SetCreationBaseProvider`（Start 前装配）；`main.go` 增 `services.Task.SetAgentCreationBasePusher(lifecycleMgr)`；`go build ./...` 全量通过 | 编译通过 | done | 2026-10-21T16:50+08:00 |
| 7.1 | inline (user-directed) | Changes 面板分支详情卡（桌面 hover + 移动 drawer 同路径）显示生效母分支；display 字段经 agentctl types→事件→WS payload→web 类型→store→derive hook→组件约六层补齐（c4794afa4 续修单仓 legacy 路径与逐层重映射缺口）；`pnpm --filter @kandev/web typecheck && lint` 绿 | 展示链路绿 | done | 2026-10-21T17:11+08:00 |
| 7.2 | inline (user-directed) | i18n 新增 copy 进 7 语言目录 + pseudo（繁体走 `pnpm run i18n:zh-hant`）；`pnpm run i18n:check` + `i18n:ratchet` 绿 | 全绿 | done | 2026-10-21T17:11+08:00 |
| 8.1 | inline (user-directed) | e2e 双 spec（`worktree-creation-base-comparison.spec.ts` + `mobile-worktree-creation-base-comparison.spec.ts` + 共享 helpers）复现「推送 39」症状并证明反转：本地 main 领先 origin/main 3 提交时本 worktree 提交=1、存量对照混入母分支提交、picker 覆盖后回落；chromium 3 例 + mobile-chrome 1 例 | 4 例全绿 | done | 2026-10-21T19:18+08:00 |
| 9.1 | inline (user-directed) | ADR `docs/decisions/2026-10-21-worktree-creation-base-comparison.md`（accepted）：反转 worktree 场景的 origin-first 默认，引用被反转的在案决策，fork-pr invariants 保留 | 落盘 | done | 2026-10-21T19:22+08:00 |
| 9.2 | inline (user-directed) | durable spec 落为独立 `worktree-comparison-base` requirements/system-design 垂直对（6 ACs 对齐 spec delta；`workspace-git-status.md` 仅加交叉引用与 updated 日期）；`list-docs.py validate` + `lint-spec-files.py --all` 通过 | 校验绿 | done | 2026-10-21T19:22+08:00 |
| 9.3 | inline (user-directed) | 公开文档三处：`docs/git-operations.md`、`docs/public/git-operations.md`、`docs/public/coordination.md`；`node scripts/validate-public-docs.mjs` 通过 | 校验绿 | done | 2026-10-21T19:22+08:00 |

> 7–9 行由 archive 会话按 apply 期证据（tasks.md 勾选、提交 af820dc16 / c4794afa4 / 29f34da4e / b47b7314e 与 step-complete 摘要）补录。

## Exit Gate

- 验证入口通过：Go 聚焦套件 + 双 e2e spec + web typecheck/lint/i18n；本机环境性失败（executor 34 / lifecycle 23 / service 41 / agentctl 8）与基线提交 2c1663810 逐名对照为零新增零掩盖。
- `openspec validate anchor-worktree-comparison-base --strict --no-interactive` 通过。
- ADR `2026-10-21-worktree-creation-base-comparison.md` + durable spec 新对（6 ACs）+ 公开文档三处已落盘并过校验。
- verify 轮（`openspec-verify-change`）漂移检测 Clean，详见 `verify.md`。

