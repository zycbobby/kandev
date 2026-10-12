## Context

仓库脚本目前由 PATH 上的 `python3` 启动。开发工具链已经在 `mise.toml` 钉了 `uv = "0.11.21"`，但没有 Python 版本声明。部分脚本需要 3.11 的 `tomllib`。产品镜像和部署脚本里的临时 `python3 -c` 不在这次范围内。

## Goals / Non-Goals

**Goals:**

- 用仓库根 `.python-version` 声明 Python 3.12（只写次要版本）。
- Makefile、pre-commit、调用仓库 `.py` 的 shell 包装，以及 `scripts/` 与 `.github/scripts/` 的 shebang，都通过 uv 启动 3.12。
- uv 缺失，或 cwd 不在仓库里、PATH 上另有 conda/系统 Python 时，这些入口仍然用 uv 的 3.12，否则失败，不回退。

**Non-Goals:**

- 不改 Dockerfile、worker 镜像、`python3 -c` / heredoc。
- 不把 Python 交给 mise 再装一份，不新增 `pyproject.toml` 或第三方依赖。
- 不改 `docs/plans/` 下一次性脚本的 shebang。
- 不批量改写历史 plan、归档 OpenSpec 和 CHANGELOG 里的旧命令。只更新根 `AGENTS.md` 里仍在指导人怎么跑这些脚本的句子。

## Research Log

### 未知 1：只靠 `.python-version`，uv 会不会在仓库外、且 conda 位于 PATH 前面时仍锁定 3.12

- **未知描述**：若不会，shebang 和「从别的目录调用脚本」就会悄悄跑到 conda，规格里的直接执行场景失败。
- **验证方式**：临时目录只放 `.python-version`（`3.12`）。当前 shell 的 PATH 上 conda base 在前。分别从子目录、仓库外目录执行 `uv run` 和 shebang。
- **结论（试通证据）**：cwd 在声明文件之下时，`uv run --no-project python` 和 `#!/usr/bin/env -S uv run --script` 都是 3.12.13。cwd 在仓库外时，同样的 shebang 和 `uv run --no-project python <script>` 选中了 `/Users/game-netease/miniconda3/bin/python3`（3.14.7）。加上 `--python 3.12` 后，仓库外 shebang 与 `uv run --no-project --python 3.12 python` 都回到 3.12.13，参数也原样进了 `sys.argv`。
- **证据摘要**：探针目录 `/tmp/uv-discover.*` 与 `/tmp/uv-pin2.*`，退出码 0。因此每个维护入口都要显式带 `--python 3.12`，不能只依赖 cwd 发现 `.python-version`。

### 未知 2：没有 uv 时会不会落到别的解释器

- **未知描述**：失败必须发生在脚本主体之前。
- **验证方式**：`PATH=/usr/bin:/bin uv run --no-project python -c 'print("ran")'`。
- **结论（试通证据）**：shell 报 `command not found: uv`，退出码 127，没有打印 `ran`。
- **证据摘要**：同上探针，退出码 127。

### 未知 3：CI workflow 是否另有一条 `python3 scripts/...` 入口

- **未知描述**：若 workflow 直接调用这些脚本，只改 Makefile 和 pre-commit 锁不住 CI。
- **验证方式**：在 `.github/workflows` 搜索 `python` 加 `scripts/` 或 `.github/scripts/`。
- **结论（拒绝理由）**：没有匹配。这次不改 workflow，也不为 CI 单独装一套 Python。
- **证据摘要**：`rg` 对 `.github/workflows/*.{yml,yaml}` 无匹配。

## Decisions

### D1：版本声明与启动参数分开写

- **选择**：`.python-version` 的唯一内容是 `3.12`。每个维护入口使用 `uv run --no-project --python 3.12`。shebang 为 `#!/usr/bin/env -S uv run --no-project --python 3.12 --script`。
- **理由**：声明文件满足「打开仓库就能看到版本」。`--python 3.12` 在 cwd 不在仓库内、conda 排在 PATH 前面时仍然生效，探针已证明。
- **已考虑 alternative**：只写 `.python-version`、入口用 `uv run python`。仓库外会选中 conda，拒绝。把 Python 加进 `mise.toml` 会变成第二个解释器管理者，拒绝。

### D2：Makefile 用一个变量展开

- **选择**：根 Makefile 增加 `REPO_PYTHON := uv run --no-project --python 3.12 python`，现有 13 处 `python3 <仓库脚本>` 改为 `$(REPO_PYTHON)`。
- **理由**：`make -n` 会展开变量，测试可以断言完整命令。一处修改版本，所有目标跟着走。
- **已考虑 alternative**：每个配方手写长命令。容易漏改，拒绝。

### D3：shebang 只改两类目录里已经存在的 `#!/usr/bin/env python3`

- **选择**：`scripts/**/*.py` 与 `.github/scripts/**/*.py` 的这一行 shebang 换成 D1 的 uv shebang。没有 shebang 的文件不补。`docs/plans/` 不动。
- **理由**：规格把 shebang 范围限定在这两个目录。直接执行时内核读 shebang，`python3 file.py` 会忽略它，所以 Makefile、pre-commit 和 shell 包装仍要改。
- **已考虑 alternative**：只改调用方、不改 shebang。直接执行 `./scripts/list-docs.py` 仍走 PATH，拒绝。

### D4：shell 里只有「执行仓库 .py 文件」的那几行要改

- **选择**：改 `scripts/demo-team-access.sh` 的 seed 调用，以及 `scripts/sync-workflow.test.sh` 里三次执行 `sync-workflow.py` 的命令和它对 `make -n` 的字符串断言。`python3 -c`、heredoc、镜像内 `python3` 保持原样。
- **理由**：这些才是规格里的维护入口。临时 Python 属于非目标。
- **已考虑 alternative**：把所有 `python3` 都换成 uv。会把部署脚本和镜像探针卷进来，拒绝。

### D5：文档只改根 AGENTS.md 的现行指令

- **选择**：在 Tooling 里写明脚本用 uv 的 3.12，并把 Knowledge 小节里三处 `python3 scripts/...` 改成同一条 uv 命令。
- **理由**：这份文件要求约定变化时同步更新。其余文档里的 `python3 scripts/...` 是历史记录或大范围说明，不是执行入口。
- **已考虑 alternative**：改完所有 markdown。范围过大，而且会改写已完成 plan 的验收记录，拒绝。

## Risks / Trade-offs

- [Risk] 贡献者机器没有 uv → Mitigation: `mise.toml` 已钉 uv；入口在 uv 缺失时退出 127，不回退。
- [Risk] `.python-version` 与 `--python 3.12` 以后改了一处忘了另一处 → Mitigation: 验收命令同时检查文件内容和 `make -n` 展开结果。
- [Trade-off] 命令变长 → 接受。短写 `uv run python` 在仓库外不可靠。
- [Trade-off] `env -S` 需要支持该参数的 `env` → 接受。这些脚本本来就是 Unix shebang；探针在本机 macOS 上已通过。

## Migration Plan

N/A — 本 change 不涉及部署。拉下代码后，跑仓库脚本需要 PATH 上有 uv。已有 mise 工具链的环境已经包含 uv。

## Open Questions

无。版本 3.12 与入口形式已由探针定下来。
