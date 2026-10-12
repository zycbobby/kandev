# Apply Ledger: pin-script-python-with-uv

## Per-Task Ledger

| task id | executor | 验证方法 | 结果 | 状态 | 时间戳 |
|---------|----------|----------|------|------|--------|
| 1.1 | inline | `tr -d '[:space:]' < .python-version` | 输出 `3.12` | `done` | `2026-10-12 17:52` |
| 1.2 | inline | `make -n sync-workflow` | dry-run 含 `uv run --no-project --python 3.12 python scripts/sync-workflow.py` | `done` | `2026-10-12 17:52` |
| 1.3 | inline | 读取 `.pre-commit-config.yaml` 四个 hook entry | 均含 `--python 3.12`，不再以 `python3` 开头 | `done` | `2026-10-12 17:52` |
| 1.4 | inline | `bash scripts/sync-workflow.test.sh` | `All sync-workflow checks passed.`；`python3 -c` heredoc 仍保留 | `done` | `2026-10-12 17:52` |
| 1.5 | inline | 两个目录搜索旧 shebang；`git ls-files -s scripts/list-docs.py` | 47 个 shebang 已替换，索引模式 `100755`，旧 `#!/usr/bin/env python3` 无剩余 | `done` | `2026-10-12 17:52` |
| 1.6 | inline | 读取 `AGENTS.md` Tooling 与 Knowledge 三处命令 | 三处均含 `uv run --no-project --python 3.12 python` | `done` | `2026-10-12 17:52` |

执行器是 inline：用户要求在当前会话直接推进，没有授权另开实现子任务。

## Exit Gate

- [x] **Verification Strategy 入口通过**：仓库根用 bash 执行入口脚本（`list-docs.py` 额外带 `--root`，因为该命令默认用 cwd）。结果：pass。`make -n` 打出 uv 命令；在 `/` 执行 shebang 后证据文件为 `3.12.13` 与 `.../cpython-3.12-macos-aarch64-none/bin/python3.12`；`list-docs.py --root … validate` 报告 `Validated 377 decisions and 1543 specifications.`；`PATH=/usr/bin:/bin` 时 stderr 为 `env: uv: No such file or directory`，退出非 0。
- [x] **`openspec validate pin-script-python-with-uv --strict` 通过**
- [x] **验收方法与证据**：
  - 方法：入口脚本、`bash scripts/sync-workflow.test.sh`、`uv run --no-project --python 3.12 python scripts/lint-spec-files.py --all`
  - 场景：版本声明、Make dry-run、仓库外 shebang、uv 缺失失败、sync-workflow 桩服务器、规格 lint
  - 结果：入口脚本打印 `pin verification passed`；sync-workflow 全部 ok；规格 lint 打印 `All specification files passed.`
