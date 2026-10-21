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

