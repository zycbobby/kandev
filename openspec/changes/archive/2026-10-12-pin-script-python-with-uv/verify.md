# Verification Report

> 此文件在 apply 完成 + Verify 步骤漂移检测通过后产生，确认实现与 proposal / design / specs / tasks 的一致性，作为 archive 前置。

**Change**: `pin-script-python-with-uv`
**Verified at**: `2026-10-12 18:10`
**Verifier**: openspec-verify-change 漂移检测 subagent，主会话复核 workflow 排除项

---

## 1. Structural Validation (`openspec validate --strict --no-interactive`)

- [x] `Change 'pin-script-python-with-uv' is valid`（exit 0）

## 2. Task Completion (`tasks.md`)

- [x] 实现清单 1.1–1.6 全部勾选；验证清单 3 条全部勾选
- [x] ledger Exit Gate 三项全部满足：入口脚本、`openspec validate --strict`、验收证据

## 3. Delta Spec Sync State

| Capability | Sync 状态 | 备注 |
|---|---|---|
| repository-script-python | 待 sync | 新增 capability。`openspec/specs/` 现有 `file-preview`、`runtime-debugging`、`workflow-selector` 都不拥有仓库脚本解释器。archive 时只 materialize 这一份 delta。 |

## 4. Requirements ↔ Implementation 逐条对照

| Requirement | 实现位置 | 差距 |
|---|---|---|
| 声明脚本 Python 版本 | `.python-version` 内容为 `3.12` | 无 |
| 维护入口使用该版本（Makefile / pre-commit） | `Makefile` 的 `REPO_PYTHON`，`.pre-commit-config.yaml` 四个 hook | 无 |
| 直接执行脚本 | `scripts/` 与 `.github/scripts/` 共 47 个 shebang：`#!/usr/bin/env -S uv run --no-project --python 3.12 --script`，并设为可执行 | 无 |
| 无法取得声明版本时失败 | `PATH=/usr/bin:/bin` 执行 shebang，stderr `env: uv: No such file or directory`，退出非 0 | 无 |
| PATH 上另有 Python | 仓库外执行仍是 uv 的 3.12.13（`cpython-3.12-macos-aarch64-none`） | 无 |
| 运行时镜像与临时 Python 保持原样 | Dockerfile、worker 镜像无 diff；`python3 -c` heredoc 未改 | 无 |

**Out-of-scope 检查**：`.github/workflows` 里仍有 `python3 .github/scripts/...`（例如 `notify-release-contributors.yml`、`lint-harness-files.yml`、`architecture-lint.yml`）。规格的维护入口不含 workflow，design 明确不改它们。这是排除项，不是缺口。

## 5. Design Decisions 核对（D1–D5）

| 决策 | 实现 | 差距 |
|---|---|---|
| D1 `.python-version` 与 `--python 3.12` 分开写 | 声明文件与 Makefile、pre-commit、shell、shebang 都带 `--python 3.12` | 无 |
| D2 Makefile 用 `REPO_PYTHON` | 13 处配方改为 `$(REPO_PYTHON)` | 无 |
| D3 只改两个目录里已有的 shebang | 47 个文件只改第一行；`docs/plans/` 三个脚本未改 | 无 |
| D4 只改执行仓库 `.py` 的 shell 行 | `demo-team-access.sh` 与 `sync-workflow.test.sh`；`python3 -c` 保留 | 无 |
| D5 文档只改根 `AGENTS.md` | Tooling 与 Knowledge 三处命令已改 | 无 |

## 6. Front-Door Routing Leak Detector

- [x] 无 `docs/superpowers/specs/` 与 `docs/superpowers/plans/` 的新文件

## Overall Decision

- [x] ✅ PASS — 可进入 archive
- [ ] ⚠️ PASS WITH WARNINGS
- [ ] ❌ FAIL
