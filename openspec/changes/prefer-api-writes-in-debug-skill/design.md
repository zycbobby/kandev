# Design: prefer-api-writes-in-debug-skill

## Context

`/debug-kandev` skill（`.agents/skills/debug-kandev/SKILL.md`，`.claude/skills` 为其 symlink）是 agent 排查本地 Kandev 运行时的入口文档：解析数据根目录、SQLite 只读查询、工作流推进追踪。proposal 已确认：skill 唯一的写指引（signal-gating 修复）局部化了 API 用法，端口硬编码、鉴权描述过时、无全局写策略与写需求映射。本 change 是**纯 skill 文档变更**：不改后端代码、不改 DB、不涉及 i18n（agent-facing 文档，非 web UI copy）。

实现物只有两类文件：

| 文件 | 职责 | 变更 |
|---|---|---|
| `.agents/skills/debug-kandev/SKILL.md` | skill 主体：新增「数据修改写策略」与「写需求 → API 速查」两个章节；修正 signal-gating 段的硬编码端口与鉴权表述；参考节补交叉引用 | Modify |
| `.agents/skills/debug-kandev/references/db-schema.md` | 表结构速查 | 尾部加一行只读警示交叉引用 |

章节插入位置（锁定）：「数据修改写策略（API 优先）」插在 `## 排查配方（先跑这些）` 之后、`## 工作流 step 推进机制（排障核心）` 之前；「写需求 → API 速查」紧随其后。理由：先读配方后写策略，读写分离的叙事顺序；signal-gating 段落（在推进机制章节内）回引写策略章节，消除重复。

## Goals / Non-Goals

**Goals:**

- skill 读者（agent）面对任何数据修改需求时，第一反应是走 API（REST 或 MCP），并能当场解析真实端口与鉴权要求
- 「确实没有 API」的边界（审计表、后端停机）显式化并带警告
- 常见写场景有具体端点/工具名速查表，且每项标注仓库内出处，未覆盖需求有兜底检索路径
- 与 `debug` skill 的实例纪律（live 不 mutate、dev-isolated 隔离验证）交叉引用而非重复

**Non-Goals:**

- 不改任何后端代码、不新增端点或 MCP 工具
- 不为 skill 文档新增仓库级测试脚本（markdown 无逻辑；一致性用验证期 grep 命令保证，见 Verification Strategy）
- 不重写 skill 的读配方部分（SQL 查询照旧）
- 不把 `debug` skill 的 `references/instance.md` 内容搬过来（只引用）
- 不覆盖 office/integrations 等集成表的写路径（超出本 skill 数据地图）

## Research Log

### 未知 1：`scripts/kandev-instances` 的真实输出形状（skill 将指导解析 `BACKEND_PORT`）
- **验证方式**：在本机运行该脚本（只读，列进程）。
- **结论（试通证据）**：输出两行——表头 `PID BACKEND_PORT WEB_PORT AGENTCTL_PORT HOME_DIR REPO_PATH` + 实例行；本机 live 实例 `38429`、`HOME_DIR=/media/zuo/AigoData/kandev-home`。`awk 'NR==2{print $2}'` 可稳定取端口。
- **证据摘要**：`./scripts/kandev-instances` → exit 0，输出如上。

### 未知 2：live 后端 REST 面真实可达性与响应形状（验证策略要引用真实命令）
- **验证方式**：对 live 实例发只读 GET。
- **结论（试通证据）**：`GET /health` → 200；`GET /api/v1/workflows` → 200 JSON（`{"workflows":[...]}`，无需 workspace_id 参数）；`GET /api/v1/workspaces` → 200 JSON；`GET /api/v1/workflows/<id>/workflow/steps` → 200 JSON（含 `events`、`auto_advance_requires_signal` 等字段）。auth 关闭时无需凭据。
- **证据摘要**：`curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:38429/health` → `200`；上述 GET 的 JSON 体片段已捕获。

### 未知 3：写路径 e2e 的沙箱（隔离实例）是否可用
- **验证方式**：读 `scripts/dev-isolated` 头部注释。
- **结论（试通证据）**：脚本自动挑非冲突端口、mktemp 全新 HOME + SQLite、mock providers、等健康检查、打印 URL/日志/pidfile/teardown 命令；warm build 约 3s。写路径闭环（PUT → GET 复核）可安全在隔离实例执行。
- **证据摘要**：`head -30 scripts/dev-isolated`；`debug` skill `references/instance.md:15-29` 的用法记录。

### 未知 4：task plan 是否有 REST 端点（决定映射表 REST 列怎么写）
- **验证方式**：grep task handlers 与 backendapp 的 plan 路由；grep WS action 常量。
- **结论（试通证据）**：无 REST plan 路由（grep 空）；plan 经 WS 网关动作 `task.plan.create/get/update/delete/revisions.list`（`pkg/websocket/actions.go:76-80`）与 MCP `*_task_plan_kandev` 工具暴露。映射表 REST 列记 `—`，shell 场景实用面是 MCP。
- **证据摘要**：`grep -E 'api\.(GET|POST|PUT|PATCH|DELETE)\("[^"]*plan' internal/task/handlers/` → 空；`grep '"task\.plan' pkg/websocket/actions.go` → 5 行命中。

依赖准出门禁结论：计划全部依赖（kandev-instances、REST 面、dev-isolated、路由/工具名事实）均落到「试通证据」终态，无未验证假设，无 blocker。

## Decisions

### D1：写策略以「读/写分离」表述，而非全面禁写
- **选择**：读路径保留 SQLite 直查；写路径强制 API-first，例外两类（审计表、后端停机修复）。
- **理由**：排障场景后端可能未运行，只读无一致性风险；全面禁读会毁掉 skill 现有核心价值。
- **已考虑 alternative**：全面禁止触碰 DB（拒绝：读查询是 skill 主用途）；教 agent 用 API 读（拒绝：排障常在后端故障态，且 SQL 对 ad-hoc 排查更强）。

### D2：REST 与 MCP 并列呈现，不指定偏好
- **选择**：映射表双列（REST / MCP），按会话可用性自选。
- **理由**：两面共享 service 层与 `stepevents.Publisher`，有 parity 测试（`internal/mcp/handlers/workflow_step_parity_test.go`），语义等价；shell 场景 curl 顺手，挂着 kandev MCP 的会话直接调工具更顺。
- **已考虑 alternative**：只写 REST（拒绝：MCP 面覆盖 task plan 等 REST 缺失的场景，且本 skill 的典型读者常带 MCP）。

### D3：端口经 `scripts/kandev-instances` 解析，38429 仅作回退默认
- **选择**：`PORT=$(./scripts/kandev-instances | awk 'NR==2{print $2}')`，空则回退 38429，再 `/health` 自检。
- **理由**：dev-isolated 实例用随机端口、`server.port` 可被 env 覆盖（`internal/common/config/catalog.go:46`）；该脚本同时覆盖 systemd 与 dev 实例并顺带校验 `HOME_DIR`。
- **已考虑 alternative**：读 config.yaml（拒绝：配置发现顺序复杂，且运行中实例的实际端口才是要打的 target）。

### D4：例外边界收敛为两条，审计表声明「只读」而非「可直写」
- **选择**：`task_step_transitions` / `session_step_history` 标注为 engine 追加审计、无写 API 且不应改；后端停机修复要求确认进程已停 + 重启后复查。
- **理由**：审计完整性是排障证据链本身；「无 API 所以可以直写」的暗示会污染证据。停机直写绕过校验与事件，须带警告。
- **已考虑 alternative**：把 settings / runtime_flag_overrides 也列为例外（拒绝：feature toggles 有 UI/API 面，不属「没有 API」）。

### D5：映射表逐项标注仓库内出处，未覆盖需求给兜底检索法
- **选择**：表后注明路由注册文件与 MCP 工具目录；未覆盖写需求先 grep `api.(GET|POST|PUT|PATCH|DELETE)` 路由注册或 MCP 工具名，确实没有再走例外。
- **理由**：防止映射表变成新的过期源——skill 读者能自行升级映射。
- **已考虑 alternative**：映射表不查出处的纯清单（拒绝：无出处的表无法验证，漂移不可发现）。

### D6：不提交测试脚本，验证用固化命令序列
- **选择**：一致性检查与冒烟闭环作为 tasks.md 固化命令（grep 校验 + live 只读冒烟 + dev-isolated 写闭环），不新增 `scripts/*.test.sh`。
- **理由**：变更对象是 markdown，无逻辑可测；grep prose 的脚本比命令本身更脆。repo 测试规范的例外条款（config/文档类）适用。
- **已考虑 alternative**：新增 `scripts/debug-skill-api-map.test.sh` 进 `test-scripts`（拒绝：维护成本 > 收益，且断言 prose 内容的测试极脆）。

## Risks / Trade-offs

- [Risk] 映射表随代码漂移（端点改名/新增工具后 skill 过期） → Mitigation: D5 的出处标注 + 兜底 grep 法；verify 阶段的一致性检查命令可随时重跑。
- [Risk] `kandev-instances` 多实例时 `NR==2` 取错行 → Mitigation: 命令注明多实例时按 `REPO_PATH`/`HOME_DIR` 列人工选行；脚本输出本身就是表格，可读。
- [Risk] agent 在 live 实例上试写造成真实数据污染 → Mitigation: 写策略章节显式「live 实例不要做有副作用的实验，用 dev-isolated」，与 `debug` skill 的 Never-mutate 纪律对齐。
- [Trade-off] skill 变长（新增约 60 行） → 接受理由：写策略是排障安全的核心边界，长度换正确性；速查表是表格化扫读，不增加推理负担。

## Migration Plan

N/A — 本 change 不涉及部署变更（纯 skill 文档，无 endpoint / DB / 依赖变更）。回滚 = revert 两个 markdown 文件的提交。

## Open Questions

无（proposal 的 4 条未知与 plan 的 4 条 probe 均已闭环）。
