## Why

仓库 Python 脚本直接调用 PATH 上的 `python3`，没有版本声明。关掉 conda base 后，本机 `python3` 是系统 3.9.6，而 `.github/scripts/notify-release-contributors.py` 无条件 `import tomllib`，需要 3.11 或更高。`mise.toml` 已把 uv 0.11.21 定为开发工具，但没有用它锁定解释器。现在用 uv 把仓库脚本固定到 3.12，让 Makefile、pre-commit 和直接执行走同一个解释器。

## What Changes

**仓库脚本的 Python 解释器**
- From: `python3` 或 `#!/usr/bin/env python3` 使用 PATH 上的第一个解释器。
- To: 仓库根 `.python-version` 只写 `3.12`。维护中的入口通过 uv 执行这些脚本，解释器必须是 uv 提供的 3.12。
- Reason: 脚本的最低要求已经是 3.11（`tomllib`），本机默认解释器却可以是 3.9。uv 已在工具链里，适合作为唯一的版本来源。
- Impact: 跑这些入口的环境必须有 uv。拿不到 3.12 时命令失败，不回退到别的 Python。产品镜像、容器内 `python3 -m venv`，以及部署脚本里的 `python3 -c` 不改。

假设（用户说「常用版本」，未指定小版本）：钉 **3.12**，不钉补丁号。它高于脚本的 3.11 下限，又不是当前最新的 3.14。uv 会安装该次要版本的最新补丁。

## Capabilities

### New Capabilities

- `repository-script-python`: 用 uv 声明并强制仓库自有 Python 脚本的解释器版本。

### Modified Capabilities

（无。现有 OpenSpec capability 不定义脚本解释器。）

## Research Log

### 未知 1：仓库现在有没有 Python 版本声明，脚本实际要求哪一版

- **未知描述**：选错版本会让现有脚本无法启动，或没有必要地抬到过新的解释器。
- **验证方式**：找版本文件；读脚本的标准库用法。
- **结论（试通证据）**：没有 `.python-version`、`pyproject.toml`、`uv.lock`，CI workflow 也没有 `setup-python`。`.github/scripts/notify-release-contributors.py` 第 14 行无条件 `import tomllib`（3.11+）。`.github/scripts/lint-harness-files.py` 在没有 `tomllib` 时尝试 `tomli`，再没有则失败，错误文案写明 Python 3.11+。
- **证据摘要**：`mise.toml` 只钉了 `uv = "0.11.21"`，没有钉 Python。`notify-release-contributors.py:14`；`lint-harness-files.py:19-24`。

### 未知 2：只有 `.python-version` 时，uv 会不会在没有 pyproject 的目录里锁定解释器

- **未知描述**：若 `uv run` 必须先有项目文件，这个方案就要额外引入 `pyproject.toml`，范围会变大。
- **验证方式**：在临时目录只放 `.python-version`（内容 `3.12`），分别跑 `uv run python`、`uv run hello.py`，以及 shebang `#!/usr/bin/env -S uv run --script`。
- **结论（试通证据）**：三条路径都打印 `3.12.13`，解释器在 `~/.local/share/uv/python/cpython-3.12-macos-aarch64-none/`。同一台机器上直接 `python3` 仍是 `/usr/bin/python3` 3.9.6。因此钉文件本身不改变 `python3`，入口必须改成 uv。
- **证据摘要**：uv 0.11.3 在 `/tmp` 探针中退出码 0；本机 uv 与 `mise.toml` 的 0.11.21 同属 0.11，行为以探针为准。

### 未知 3：哪些 `python3` 调用属于这次要锁的「scripts」

- **未知描述**：把产品镜像和一次性 shell 片段也改成 uv，会把开发工具链塞进运行时镜像。
- **验证方式**：枚举 `python3` 调用点，按「执行仓库里的 .py 文件」和「宿主或镜像里的临时 Python」分开。
- **结论（拒绝理由）**：只锁仓库自有脚本的维护入口。不纳入 Dockerfile / worker 镜像里的 `python3`，不纳入 `python3 -c` 和 heredoc（部署、探针、测试夹具），不改已归档的 OpenSpec ledger 与 CHANGELOG。
- **证据摘要**：维护入口包括根 `Makefile`、`.pre-commit-config.yaml` 的四个 `python3` hook、`scripts/sync-workflow.test.sh`、`scripts/demo-team-access.sh`，以及 `scripts/` 与 `.github/scripts/` 下脚本的 shebang。`scripts/sync-workflow.test.sh` 还断言 `make -n` 输出含 `python3 scripts/sync-workflow.py`，实现时这条断言要跟着入口文案改。

## Impact

- 新增仓库根 `.python-version`，内容为 `3.12`。
- 改维护入口：`Makefile`、`.pre-commit-config.yaml`、调用仓库 `.py` 的 shell 测试与包装脚本、相关 shebang。
- 更新仍在指导读者怎么跑这些脚本的现用文档（例如根 `AGENTS.md`）。历史记录、归档 change、CHANGELOG 保持原句。
- 依赖：贡献者环境里的 uv。`mise.toml` 已钉 `uv = "0.11.21"`，并让 `pipx:pre-commit` 依赖 uv。本 change 不把 Python 再交给 mise 装一份。
- 非目标：产品容器镜像、Kubernetes worker 里的 `python3 -m venv`、部署脚本的 `python3 -c`、给脚本增加第三方依赖或 `pyproject.toml`。
