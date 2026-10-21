# Ledger: anchor-worktree-comparison-base

## Per-Task Ledger

| task id | executor | 验证方法 | 结果 | 状态 | 时间戳 |
|---------|----------|---------|------|------|--------|
| 1.1 | inline (user-directed) | `go test ./internal/task/models/ -run CreationBase -v`：往返/JSON 再水化/未知 Version/危险 ref（空、`..`、`.lock`、HEAD）/坏 SHA/origin/ 前缀合法/SHA-256 合法/Remove 幂等 | 11 例全绿 | done | 2026-10-21T15:00+08:00 |
| 1.2 | inline (user-directed) | `go test ./internal/task/repository/sqlite/ -run CreationBase -v`：写读往返+无关 key 与 manual marker 保留+分支列不动、重物化覆盖更新、缺行报 not found、nil 拒绝；`go build ./internal/task/...` + gofmt 通过 | 3 例全绿 | done | 2026-10-21T15:00+08:00 |

