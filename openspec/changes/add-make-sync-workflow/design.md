# Design: add-make-sync-workflow

## Context

Kandev 的 workflow 定义存于运行时 DB，无版本控制。本 change 新增根 Makefile 目标 `sync-workflow`：调用**运行中后端**的既有只读 HTTP 导出接口，把全部 workspace 的全部 workflow（含 hidden/system/office）导出为「每 workflow 一文件」的 YAML 快照，落到 `$(CURDIR)/workflows/`。

- 默认后端 `http://localhost:38429`（`apps/backend/internal/launcher/constants.go:10` `defaultBackendPort=38429`；`scripts/dev-isolated` 亦用 38429）。
- 探针已确认本地有运行中后端（`kandev __backend` 监听 38429，`/health` 返回 `version v0.91.0-87-g3cd88733a`）。
- 本 change **不改后端**，纯新增 make 目标 + 脚本。

## Goals / Non-Goals

**Goals:**
- 提供 `make sync-workflow`，把运行时全部 workflow 导出为可提交、可 diff 的 YAML，落到 `workflows/`（每 workflow 一文件，可移植 `WorkflowExport` 信封）。
- 覆盖全部 workspace、全部类型（hidden/system/office 一并导出）。
- 重复运行幂等（覆盖旧文件）；后端不可达时明确失败。

**Non-Goals:**
- 不实现「镜像式删除陈旧文件」（backup 语义，非 mirror）。
- 不实现反向 apply（repo → 运行时由既有 `internal/workflowsync` 负责）。
- 不支持 auth 开启时的 token 传递（默认 auth-off；401 视为失败并报错）。
- 不改后端、不加 endpoint、不加 DB 迁移。

## Research Log

### 未知 1：三条导出端点的真实响应形状与默认端口
- **验证方式**：curl 运行中后端 `http://localhost:38429`（只读 GET，无副作用）。
- **结论（试通证据）**：链路可用，响应形状如下。
- **证据摘要**：
  - `GET /health` → 200 `{"mode":"websocket+http","service":"kandev","status":"ok","version":"v0.91.0-87-g3cd88733a"}`
  - `GET /api/v1/workspaces` → `{"workspaces":[{"id","name","owner_id","task_prefix","created_at","updated_at"}],"total":3}`
  - `GET /api/v1/workflows?workspace_id=<id>&include_hidden=true&exclude_office=false` → `{"workflows":[{"id","workspace_id","name","sort_order","style","source","created_at","updated_at"}],"total":N}`
  - `GET /api/v1/workflows/<id>/export` → `Content-Type: application/x-yaml`，body 形如 `version: 1\ntype: kandev_workflow\nworkflows:\n    - name: Kanban\n      steps: ...`（yaml.v3 默认 4 空格缩进，`workflows` 列表含单个 workflow）。

### 未知 2：脚本工具链（curl/jq 是否可用、是否引入依赖）
- **验证方式**：读 repo 既有脚本与 `test-scripts` 依赖面。
- **结论（拒绝理由）**：不依赖 `curl`/`jq`，改用 Python3 标准库（`urllib.request` + `json`）。
- **证据摘要**：`test-scripts` 已大量调用 `python3`（`Makefile:575-596`）；导出体是 YAML 直接落盘、无需解析；JSON 解析仅需 workspaces 列表与 workflows 列表两个形状。

## Decisions

### D1：脚本用 Python3 标准库实现
- **选择**：`scripts/sync-workflow.py`，仅用 stdlib（`urllib.request`、`json`、`re`、`os`、`sys`）。
- **理由**：HTTP+JSON+写文件用 Python 比 bash+curl+jq 更稳健；Python3 已是 repo 测试依赖，无新增依赖。
- **已考虑 alternative**：bash + curl + jq（被拒：jq 非保证依赖，JSON 解析脆弱）；Go 命令（被拒：用户已选 HTTP 机制、无后端改动，脚本即可，避免新增二进制构建链路）。

### D2：base URL 默认 `http://localhost:38429`，可被 `URL=` 覆盖
- **选择**：Makefile 定义 `URL ?= http://localhost:38429`，`sync-workflow` 目标以 `python3 scripts/sync-workflow.py "$(URL)" "$(CURDIR)/workflows"` 调用。
- **理由**：与 `defaultBackendPort=38429`、`dev-backend`、`dev-isolated` 一致；可被命令行 `make sync-workflow URL=http://localhost:4000` 覆盖。
- **已考虑 alternative**：`PORT=`（被拒：`URL` 更通用，覆盖 host+scheme）。

### D3：文件名 = slug(workflow.name)，跨 workspace 同名时以 workspace 身份消歧
- **选择**：确定性算法——workspaces 按 `id` 升序、每个 workspace 内 workflows 按 `id` 升序遍历；`base=slug(name)`；首次出现用 `base`，同名冲突时用 `base--slug(workspace.name)`，再冲突追加 `--2/--3/...`。`slug` 规则：小写、非 `[a-z0-9]` 折叠为 `-`、去首尾 `-`、空则回退 `workflow`。
- **理由**：满足 spec delta「文件名唯一且可预测、跨 workspace 以 workspace 身份消歧」，且确定可测。
- **已考虑 alternative**：纯数字后缀（被拒：spec 要求用 workspace 身份消歧）。

### D4：非破坏性 backup（只写/覆盖，不删）
- **选择**：只 `open(..., "w")` 写入/覆盖当前 workflow 文件，不删除 `workflows/` 下其余文件。
- **理由**：proposal 已定「backup 语义」；删除陈旧文件属镜像行为，列为 non-goal，避免误删风险。

### D5：失败即停，错误信息含后端 URL
- **选择**：任一请求非 2xx 或连接失败 → 打印 `sync-workflow: backend unreachable at <url>: <status/error>` 到 stderr 并 `exit 1`。
- **理由**：spec delta「后端不可达时明确失败」；错误含 URL 便于定位端口/主机问题。

## Risks / Trade-offs

- [Risk] 后端端口非默认（如 `make dev` 自动端口）→ Mitigation：`URL=` 可覆盖；失败信息含 URL。
- [Risk] 跨 workspace 同名 workflow 覆盖 → Mitigation：D3 确定性去重；测试覆盖同名场景。
- [Risk] auth 开启后 401 → Mitigation：列为 non-goal；401 走 D5 明确报错。
- [Trade-off] 只读不删（backup 非 mirror）→ 接受理由：避免误删，mirror 语义已列 out of scope，可后续单独 change 扩展。

## Migration Plan

N/A — 本 change 不涉及部署、endpoint 或 DB 变更，纯新增 make 目标与脚本。

## Open Questions

N/A — 依赖门禁已通过真实探针闭环；无遗留未知或 blocker。
