## Why

Kandev 的 workflow 定义目前只存于运行时 SQLite DB，没有版本控制、没有 diff、没有回滚；现有的 `internal/workflowsync` 只做「repo → 运行时」的单向同步，运行时在 UI 里手工调好的 workflow 无法落盘到工程目录。需要新增一个 `make sync-workflow` 目标，把运行时全部 workflow 反向导出为可提交、可 diff 的 YAML 文件，落到工程 `workflows/` 目录，作为版本化备份，并为后续「repo → 运行时」sync 提供可回滚的种子。

## What Changes

- 新增根 Makefile 目标 `sync-workflow`：调用**运行中后端**的 HTTP 导出接口，枚举全部 workspace 与全部 workflow（含 hidden/system/office），逐 workflow 导出并写入 `$(CURDIR)/workflows/`。
- 新增辅助脚本（如 `scripts/sync-workflow.sh`）承载 HTTP 调用与落盘逻辑；**不改后端**，复用既有导出端点。
- 输出布局：`workflows/` 目录下**每 workflow 一个 YAML 文件**，每个文件是一个可移植 `WorkflowExport` 信封（`version: 1` / `type: kandev_workflow`），`workflows` 列表只含单个 workflow。

**workflow 定义落盘**
- From: workflow 定义只存在于运行时 DB（或由 workflowsync 从 repo 单向读入）。
- To: `make sync-workflow` 可随时把运行时全部 workflow 导出为 `workflows/*.yml`。
- Reason: 让运行时 workflow 获得版本控制、可 diff、可回滚。
- Impact: non-breaking，纯新增能力；不改变任何既有后端行为。

## Capabilities

### New Capabilities

- `workflow-backup`: 把运行中后端的全部 workflow（跨全部 workspace，含 hidden/system/office）导出为「每 workflow 一文件」的 YAML 快照，落到当前工程的 `workflows/` 目录。

### Modified Capabilities

（无：本 change 不修改既有 capability 的 spec 语义，仅新增一个面向开发者/运维的导出能力。）

## Research Log

### 未知 1：目标产物格式是否已有可复用实现
- **验证方式**：读代码（`internal/workflow/models/export.go`、`docs/workflow-import-export.md`、`internal/workflowsync`）。
- **结论（试通证据）**：可移植格式 `WorkflowExport`（`version: 1` / `type: kandev_workflow` / `workflows[]`）已存在，`BuildWorkflowExport` 已把领域模型 + agent profile 解析成 portable 结构，且 `internal/workflowsync` 正是读取同格式 `.yml/.yaml/.json` 文件。
- **证据摘要**：`apps/backend/internal/workflow/models/export.go:8-14`；`docs/workflow-import-export.md`；`apps/backend/internal/workflowsync/models.go:23`（`DefaultPath = ".kandev/workflows"`，本 change 输出到 `workflows/`，与 sync 目录不同）。

### 未知 2：HTTP 机制能否覆盖「全部 workspace + 全部类型」
- **验证方式**：读路由与 service 代码，确认端点组合。
- **结论（试通证据）**：`GET /api/v1/workspaces` 枚举 workspace；`GET /api/v1/workflows?workspace_id=<id>&include_hidden=true&exclude_office=false` 列出含 hidden 与 office 的 workflow；`GET /api/v1/workflows/<id>/export` 按 ID 导出单个 workflow（`ExportWorkflow` 走 `GetWorkflow`，不按 hidden 过滤）。
- **证据摘要**：`apps/backend/internal/task/handlers/workspace_handlers.go:37`；`apps/backend/internal/task/handlers/workflow_handlers.go:63,84-120`（`parseIncludeHidden`、`exclude_office != "false"`）；`apps/backend/internal/workflow/service/service.go:604`。

### 未知 3：离线读 DB vs 在线调 HTTP
- **验证方式**：向用户澄清机制选型。
- **结论（试通证据）**：用户拍板走**在线 HTTP 导出接口**（需后端在线），而非直读 SQLite DB。
- **证据摘要**：本会话澄清问答 `mechanism = q1_opt2`；`output_dir = $(CURDIR)/workflows/`；`scope = 全部 workspace + 全部类型`；`layout = 每 workflow 一文件`。

## Impact

- **代码**：根 `Makefile`（新增 `sync-workflow` target + `help` 条目）；新增 `scripts/sync-workflow.sh`（或等价脚本）。**无后端改动**。
- **接口（复用，不新增）**：`GET /api/v1/workspaces`、`GET /api/v1/workflows?workspace_id=&include_hidden=&exclude_office=`、`GET /api/v1/workflows/:id/export`。
- **依赖**：运行中后端在线可达；脚本侧使用 `curl` + `jq`（或 `python3`）解析 JSON/写文件。
- **测试**：新增 `scripts/make-sync-workflow.test.sh`（dry-run + `make help` 断言，仿 `scripts/make-deploy.test.sh`），接入 `Makefile` 的 `test-scripts`；导出文件的文件名唯一性与覆盖行为用脚本测试覆盖。
- **文档**：`Makefile` `help` 文案；若涉及公开用户文档，走 `/docs-maintainer` 评估。
