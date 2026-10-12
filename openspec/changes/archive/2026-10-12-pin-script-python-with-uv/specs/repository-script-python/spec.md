# repository-script-python

## 背景

Kandev 仓库里的 Python 脚本是开发与校验工具，不是产品运行时。它们今天跟着 PATH 上的 `python3` 走。本机可以是系统 Python 3.9，而部分脚本需要 3.11 的 `tomllib`。开发工具链里已经有 uv，但没有用它声明解释器版本。

## 方案与决策

- **版本来源**：仓库根 `.python-version` 只包含 `3.12`。不钉补丁号，由 uv 安装该次要版本的当前补丁。
- **执行方式**：维护中的入口通过 uv 启动这些脚本。直接执行时 shebang 也走 uv。入口拿不到 uv 或拿不到 3.12 时失败，不回退到 PATH 上的其他 Python。
- **不改运行时**：产品镜像和部署脚本里的临时 `python3` 仍由镜像或宿主提供。

## ADDED Requirements

### Requirement: 声明脚本 Python 版本

The repository SHALL declare Python 3.12 as the interpreter version for its own Python scripts. The declaration MUST be the minor version only, so uv may select the current 3.12 patch.

#### Scenario: 读取版本声明

- **WHEN** a developer inspects the repository's Python version declaration
- **THEN** it names Python 3.12 and does not name a patch release

### Requirement: 维护入口使用该版本

A maintained entry point that runs a repository Python script MUST start that script with the declared 3.12 interpreter provided by uv. Maintained entry points are the root Makefile targets, the local pre-commit hooks that run repository Python files, shell wrappers that invoke those files, and the shebangs of Python files under `scripts/` and `.github/scripts/`.

#### Scenario: 从 Makefile 或 pre-commit 运行

- **WHEN** a developer runs a Makefile target or a pre-commit hook that executes a repository Python script
- **THEN** the script's `sys.version_info` is 3.12 and its interpreter path is the one uv installed for that version

#### Scenario: 直接执行脚本

- **WHEN** a developer executes a repository Python script through its shebang
- **THEN** the script runs on uv's Python 3.12 rather than the first `python3` on `PATH`

### Requirement: 无法取得声明版本时失败

If uv is missing, or uv cannot provide Python 3.12, the maintained entry point MUST exit non-zero and MUST NOT run the script with another interpreter.

#### Scenario: 机器上没有 uv

- **WHEN** a developer runs a maintained entry point on a machine where `uv` is not on `PATH`
- **THEN** the command fails before the script body runs

#### Scenario: PATH 上另有 Python

- **WHEN** `python3` on `PATH` is not Python 3.12 and a maintained entry point runs
- **THEN** the script still runs on uv's Python 3.12, or the entry point fails without using that other interpreter

### Requirement: 运行时镜像与临时 Python 保持原样

This capability MUST NOT change the Python interpreter installed in product container images, Kubernetes worker images, or one-off `python3 -c` / heredoc snippets in deployment and probe scripts.

#### Scenario: 镜像内的 python3

- **WHEN** a product or worker image runs `python3` for its own runtime or smoke checks
- **THEN** that invocation still uses the image's `python3`, not uv

## Known Failure Modes

| Symptom | Cause | Fix |
|---------|-------|-----|
| 脚本仍跑在系统 Python 3.9 上 | 入口写的是 `python3 script.py`，shebang 被忽略 | 入口改为 uv，或让 shebang 自己调用 uv |
| pre-commit 在干净环境里失败 | 机器没有 uv | 用已钉在 `mise.toml` 里的 uv，不要回退到系统 Python |
| `make -n sync-workflow` 的字符串断言失败 | 测试写死了 `python3 scripts/sync-workflow.py` | 断言改成新的 uv 入口 |
| 仓库外执行选中了 conda 或系统 Python | 入口只依赖 `.python-version`。当前目录不在仓库里、且 conda 排在 PATH 前面时，uv 会选用那个解释器 | 维护入口显式带 `--python 3.12`，不要只靠版本文件发现 |
| CI 仍跑在 runner 的 python3 上 | workflow 的 `run: python3 script.py` 忽略 shebang，而本次维护入口不含 workflow | 要锁 CI 时另改 workflow，不要指望 shebang |
