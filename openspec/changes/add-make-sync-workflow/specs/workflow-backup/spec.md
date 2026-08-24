# workflow-backup

## 背景

Kandev 的 workflow 定义只存在于运行时 DB，没有版本控制与 diff。本 capability 新增一个面向开发者/运维的 `make sync-workflow` 目标：调用运行中后端的既有 HTTP 导出接口，把全部 workspace 的全部 workflow（含 hidden/system/office）导出为「每 workflow 一文件」的 YAML 快照，落到当前工程的 `workflows/` 目录。

## 方案与决策

- **机制**：复用既有 HTTP 导出接口（`GET /api/v1/workspaces`、`GET /api/v1/workflows?workspace_id=&include_hidden=&exclude_office=`、`GET /api/v1/workflows/:id/export`），不改后端。
- **输出格式**：每个文件是一个可移植 `WorkflowExport` 信封（`version: 1` / `type: kandev_workflow`），`workflows` 列表只含单个 workflow。
- **语义（backup，非破坏性）**：只创建/覆盖当前 workflow 对应的文件，**不删除**运行时已不存在的 workflow 的历史文件。「镜像式删除陈旧文件」明确列为 out of scope，留待后续扩展。

## ADDED Requirements

### Requirement: 全量导出运行时 workflow

The `make sync-workflow` target SHALL export every workflow in every workspace, including hidden, system, and office workflows, into the current project's `workflows/` directory.

#### Scenario: 覆盖全部 workspace 与全部类型
- **WHEN** the operator runs `make sync-workflow` against a reachable backend
- **THEN** every workflow in every workspace (including hidden, system, and office workflows) is written under `workflows/`

### Requirement: 每 workflow 一个文件

Each exported file SHALL contain exactly one workflow in the portable `WorkflowExport` envelope (`version: 1`, `type: kandev_workflow`).

#### Scenario: 单文件单 workflow
- **WHEN** a workflow is exported
- **THEN** its file's top-level `workflows` list contains exactly that one workflow

### Requirement: 输出到工程 workflows 目录

The target SHALL write exported files under the current project's `workflows/` directory, creating it if absent.

#### Scenario: 目标目录
- **WHEN** `make sync-workflow` runs
- **THEN** exported files are created under `$(CURDIR)/workflows/`

### Requirement: 文件名唯一且可预测

Exported filenames SHALL be derived from a filesystem-safe slug of the workflow name, and MUST be unique across the entire export; when two workflows in different workspaces yield the same slug, the filename MUST be disambiguated using workspace identity.

#### Scenario: 同名 workflow 跨 workspace 不覆盖
- **WHEN** two workspaces each contain a workflow with the same name
- **THEN** both workflows are exported to distinct filenames and neither overwrites the other

### Requirement: 重复运行覆盖

Re-running the target SHALL overwrite existing files for the workflows still present, so the directory reflects the latest runtime state.

#### Scenario: 幂等重跑
- **WHEN** `make sync-workflow` runs twice without workflow changes in between
- **THEN** the second run overwrites the same files and leaves the directory content unchanged apart from timestamps

### Requirement: 后端不可达时明确失败

The target SHALL fail with a non-zero exit code and a clear error naming the backend URL when the backend is unreachable.

#### Scenario: 后端未运行
- **WHEN** the backend is not reachable at the configured URL
- **THEN** the target exits non-zero and reports the backend URL in the error message

## Known Failure Modes

| Symptom | Cause | Fix |
|---------|-------|-----|
| 导出缺 workflow | 未传 `include_hidden=true` 或 `exclude_office=false` | 列表调用必须同时带这两个查询参数 |
| 同名 workflow 互相覆盖 | 文件名只用 workflow 名 slug | 跨 workspace 同名时以 workspace 身份消歧 |
| `jq` 不可用导致解析失败 | 脚本依赖 `jq` 但环境未安装 | 用 `python3` 或内建 JSON 解析兜底，或在 doctor 中检查 |
