# Proposal: settle-abandoned-delivery-and-safe-deploy

## Why

2026-11-04 的一次 service restart 在 HTTP shutdown 超时后强制终止 agent 进程组，使 9 个会话、15 个 durable submission 进入永久死锁。已发布的 pre-intake settlement 只覆盖「journal 根本没有该 submission」的形态；本次事故暴露了另外两条没有恢复出口的路径：journal 已接收但 terminal evidence 丢失，以及 recovery 记录与 resume 创建的 successor execution 绑定不一致。现有 retry 与 native resume 对二者都 fail-closed，且没有运行时结算入口，只能离线修改 SQLite 与 journal。与此同时，部署脚本在重启前不检查在途工作，systemd 的 30s 停止预算又短于后端 35s 的优雅关闭预算，会重复制造同类事故。

同一时间窗口还暴露了第三个独立问题：backend↔agentctl 的 agent-stream WebSocket 在负载下两次断线重连（12:15:18 / 12:17:30），`step_complete_kandev` 信号在 5s 发送窗口内没有进入 stream，backend 从未收到；workflow 停在 completion-pending，直到用户新开一轮才恢复，同窗口并行任务也丢了 3 次 MCP 工具调用。现场同时出现 NTFS SQLite 写争用（启动时 `writer_wait_count=2281`、`writer_ping deadline_exceeded`）。这是控制面信号在传输层的丢失与不可见，和 Form A/B 的 durable submission 死锁不同。本 change 同时加入 Form C：诊断并加固 agent-stream WS，让 step_complete 信号在瞬断/背压下存活或可重试，并让 completion-pending 可见、可恢复。本 change 为这三条路径提供保守、可审计的恢复与传输规则，并让部署在重启前拒绝或显式覆盖进行中的工作，避免再次批量污染运行中的会话。

## What Changes

**Form A：journal-present 未知结果的保守结算**
- From: journal 中的 submission 为 `interrupted_unknown` 且没有 `terminal_event_retained` / `terminal_sequence` 时，retry 固定返回 `terminal_outcome_missing`；native resume 对 journal-only unknown peer 要求不存在的 terminal evidence，对 SQL 已终态的 peer 又拒绝 retire；journal 的 `Unresolved` 同时阻塞新 prompt。
- To: 认证 journal 证明 exact submission、历史 owner process 已终止、且没有其它 live/ambiguous 工作时，retry 先 retire 该 journal 记录（保留 payload、state 和未知标签），再把 SQL submission 结算为 `failed`；绑定 recovery block 在同一事务解析；native resume 忽略已 retired peer。
- Reason: 「没有 terminal evidence」不等于工作仍活着；owner 已证明死亡后必须有一个保守、可审计、可收敛的出口，而不是要求离线改库。
- Impact: 非破坏性；只影响当前 blocked 的恢复路径；不重发历史 prompt，不伪造 terminal event/sequence，不把 `fully_settled_at` 伪装成完整 terminal 结算。

**Form B：retry 接受 recovery successor execution**
- From: `recoverySessionMatches` 要求 `executors_running.agent_execution_id` 与 recovery 记录完全一致；resume 创建新 execution 后，retry 在毫秒级以 `recovery_identity_mismatch` 失败。
- To: retry 继续校验 session / incarnation / generation / submission / stream / promptGeneration，但 execution ID 的归属交给运行时现有的 successor 证据（`loadedRecoverySuccessorMatches` + `proveHistoricalDeliveryOwnerDead`）裁决。
- Reason: 数据库层 guard 不应否决运行时已经能证明的 successor；记录中的旧 execution 已死并不代表当前绑定不合法。
- Impact: 非破坏性；live 且非 successor 的 execution 仍 blocked；原 execution 路径行为不变。

**部署 idle 门禁与停止预算**
- From: `scripts/deploy-user-service.sh` 在 resolve home/port 后直接 publish 并执行 `service restart`，不检查在途工作；生成的 systemd unit 固定 `TimeoutStopSec=30s`。
- To: publish 之前只读检查 SQLite 中的 `STARTING`/`RUNNING` session、`prepared`/`accepted`/`dispatching`/`interrupted_unknown` submission 与 open recovery block；busy 时列出活跃 task 并拒绝，`--force` 显式覆盖；服务已运行但数据库不可定位或不可读时按 busy 处理；systemd unit 的停止预算提升到 60s。
- Reason: 重启会截断 agent drain；必须先证明没有在途工作，或由操作者显式承担风险。
- Impact: operator-facing；从运行中的 session 内部发起 `make deploy` 会默认被拒绝，需要 `--force`（make 目标建议透传 `DEPLOY_FORCE=1`）。

**Form C：agent-stream 信号投递韧性**
- From: `step_complete_kandev` 与 MCP 工具调用复用同一条 agent-stream WS；relay 使用无缓冲 request channel + 5s 发送超时，没有 ping/pong 与写超时；客户端 event queue 满时会关闭 stream。瞬断或背压下信号丢失，backend 从未收到，workflow 停在 completion-pending，直到用户新开一轮；同窗口其它 MCP 工具调用也失败。
- To: step_complete 信号在发送前持久化到 agentctl 本地 journal（或等价的有界持久队列），断线/背压时在重连后按 request ID 幂等重放；MCP 控制请求不因 agent-event backlog 而失败；agent-stream 增加 ping/pong、读写 deadline 与 typed disconnect reason；未在窗口内送达的信号向 backend 发出 typed delivery-failure notice，workflow 以既有 recovery surface 显示 completion-pending，agent 的聊天保底/重发仍可用且去重。
- Reason: 一次瞬态传输故障不应要求用户新开一轮才能推进 workflow；信号「已尝试但未送达」必须可见，而不能无声等待。
- Impact: non-breaking；有界重试/持久捕获，不改变 step_complete 的 accepted/stale 语义；不把 durable replay 扩展到所有非幂等 MCP 工具。

## Capabilities

### New Capabilities
- `durable-delivery-abandoned-settlement`: journal-present、无 terminal evidence、owner 已证明死亡时的证据规则、结算事务、journal retirement 与幂等边界。
- `durable-delivery-retry-succession`: retry 对 recovery successor execution 的接受规则，以及必须保持的 fail-closed 身份校验。
- `service-deploy-idle-gate`: 部署前的 idle 检查、活跃工作清单、`--force` 覆盖，以及生成 systemd 单元的停止预算。
- `agent-stream-signal-resilience`: agent-stream 瞬断/背压下的 MCP 控制请求与 step_complete 信号投递、持久捕获/重放、completion-pending 可见性与传输可观测性。

### Modified Capabilities
（无。现有的 `durable-delivery-pre-intake-settlement` 与 `durable-delivery-admission-health` 语义不变；本 change 只新增能力。）

## Research Log

### 未知 1：Form A 是否只是 native resume 的单点校验？
- **验证方式**：读 lifecycle / orchestrator / journal 代码，结合备份 SQLite 与保留 journal 状态，并检查状态矩阵测试。
- **结论（试通证据）**：否。retry retained 路径固定返回 `terminal_outcome_missing`（`lifecycle/retained_delivery_recovery.go:54-57`）；native resume 对 journal-only unknown peer 要求 terminal evidence（`lifecycle/native_resume_terminal.go:31-33,47-53`），对 SQL 已终态 peer 又拒绝 retire（`lifecycle/native_resume_delivery.go:110-120`）；状态矩阵把「SQL terminal + journal interrupted_unknown」固定为 blocked（`lifecycle/native_resume_state_matrix_test.go:18-26`）；journal `Unresolved` 还会经 `DeliveryCapability` → `unresolved_durable_work` 阻塞新 prompt（`agentctl/server/process/manager.go:553-566`；`lifecycle/durable_delivery_activation.go:48-49`）。
- **证据摘要**：备份库 session `03b6ea40-4fb8-48c0-8b2f-e22c0d851f82` 的 `prompt:e9d56d2b` 为 `interrupted_unknown` / `terminal_sequence=0`，block `409d4882` 绑定非空 stream `476f...:g1`；recovery metadata 绑定 `e9c352eb`，`executors_running` 已是 `11faeca6`。

### 未知 2：能否复用 pre-intake settlement，或向 journal 写入 terminal evidence？
- **验证方式**：读 pre-intake settlement 的 eligibility 与 repository 事务，以及 journal terminal 写入/retirement 路径。
- **结论（拒绝理由）**：pre-intake settlement 要求 block `delivery_stream_id=''` 且 descriptor `SubmissionCount==0`（`orchestrator/pre_intake_delivery_settlement.go:21-55`；`sqlite/agent_delivery_pre_intake_settlement.go:110-124,237-256`），Form A 不满足；向 journal 伪造 terminal event 会声称系统无法证明的结果，违反 `durable-restart-reconciliation` AC-002.2 与 ADR-2026-10-12 的「retire 未知历史、不伪造 terminal」原则。采用「新增 sibling settlement + journal retirement」。
- **证据摘要**：`RetireSubmission` 同代可 retire `HasUnprovenOutcome` 记录，保留 payload/state，同代不 seal 当前 stream（`journal/journal_submissions.go:84-130`；`journal/retired_stream.go:11-14`）。

### 未知 3：Form B 的 successor 路径是否已存在？
- **验证方式**：读 `RecoverAgentPromptStreamWithIdentity`、`already_resumed_recovery.go` 与 orchestrator retry。
- **结论（试通证据）**：运行时已有 `loadedRecoverySuccessorMatches` + `proveHistoricalDeliveryOwnerDead` + `recoverLoadedSuccessorDelivery`（`lifecycle/delivery_recovery_without_execution.go:43-51`；`lifecycle/already_resumed_recovery.go:34-77`），只是 orchestrator 的 `recoverySessionMatches`（`orchestrator/session_delivery_recovery.go:421-429`）先 blocked。
- **证据摘要**：`recoverySessionMatches` 在 `session.AgentExecutionID != recovery.AgentExecutionID` 时返回 false（`orchestrator/session_delivery_recovery.go:213-216`）。

### 未知 4：部署门禁的位置与数据源？
- **验证方式**：读 deploy 脚本、launcher unit 生成、后端 shutdown 预算与 live DB。
- **结论（试通证据）**：脚本已 resolve home/port，gate 必须在 `publish_bundle` 之前（`scripts/deploy-user-service.sh:218-238`）；SQLite 只读查询可行；`TimeoutStopSec=30s`（`launcher/service.go:625`）小于 10s HTTP + 20s agents + 5s tracing 的预算（`backendapp/helpers.go:120-122`）。live DB 当前有 2 个 RUNNING session + 2 个 dispatching submission，说明从 session 内 deploy 会默认被拒绝。
- **证据摘要**：`scripts/deploy-user-service.sh:218-238`；`apps/backend/internal/launcher/service.go:625`；`apps/backend/internal/backendapp/helpers.go:120-122,2815-2870`。

### 未知 5：step_complete 信号为什么在断流窗口丢失，以及如何让它可恢复？
- **验证方式**：读 `ChannelBackendClient.RequestPayload`、agentctl agent-stream writer/read loop、客户端 event queue、`stuck_signal_watchdog` 与 writer health 诊断。
- **结论（试通证据）**：信号请求经无缓冲 `requestCh` 交给 agent-stream writer，发送端有 5s 超时；writer 与 agent-event 共用循环，没有 ping/pong 与写 deadline；客户端 event queue 满时直接关闭 stream（`errAgentEventQueueFull`），导致 writer 阻塞/断开，MCP 请求无法入流。backend 的 stuck-signal watchdog 只处理已持久化的 signal，无法发现「从未到达」的尝试。SQLite writer 争用（`writer_wait_count` / `writer_ping deadline_exceeded`）会放大 handler 阻塞与 event backlog。方案采用「step_complete 信号持久捕获 + 重连幂等重放 + completion-pending 可见 + WS ping/pong/写 deadline」。
- **证据摘要**：`apps/backend/internal/mcp/server/backend_client.go` 的 5s send timeout 与 unbuffered requestCh；`apps/backend/internal/agentctl/server/api/agent.go` 的 `writeMessage` / `runAgentStreamWriterLoop`；`apps/backend/internal/agent/runtime/agentctl/agent.go` 的 `errAgentEventQueueFull` / `readUpdatesStream`；`apps/backend/internal/orchestrator/stuck_signal_watchdog.go`；`apps/backend/internal/persistence/requiredstores/health.go` 的 `writer_ping` / `writer_wait_count`。

## Impact

- **后端代码**：
  - `internal/agentctl/journal/`：retained journal 的 retire 请求与校验；不新增删除语义。
  - `internal/agentctl/server/api/`：control server 的 retained retire 路由（file-only 路径）；复用 unowned-session 与 owner marker 校验。
  - `internal/agent/runtime/agentctl/` + `internal/agent/runtime/lifecycle/`：retained 结算/retirement 客户端、runtime 证据返回、`nativeResumeSQLRows` 忽略 retired peer。
  - `internal/orchestrator/`：retry 的 abandoned settlement 分支、successor identity 规则、recovery metadata 清理与队列释放。
  - `internal/mcp/server/`、`internal/agent/runtime/agentctl/`、`internal/agentctl/server/api/`、`internal/agentctl/journal/`：agent-stream 的 ping/pong、读写 deadline、typed disconnect reason、MCP 控制请求的背压隔离，以及 step_complete 信号的持久捕获与重连重放。
  - `internal/orchestrator/`（step completion watchdog / handlers）与 `internal/persistence/requiredstores/`：completion-pending 可见性、signal delivery failure notice、writer contention 观测。
  - `internal/task/models/` + `internal/task/repository/`（SQLite 与 PostgreSQL 行为测试）：sibling settlement 事务、新 effect type、block 解析。
- **脚本 / 启动器**：`scripts/deploy-user-service.sh` 及其 shell 测试；`Makefile` 的 `DEPLOY_FORCE` 透传（如需）；`internal/launcher/service.go` 与 `service_test.go`。
- **文档 / 技能**：`docs/public/run-as-a-service.md`、`docs/run-as-a-service.md`、`docs/specs/executors/system-design/agent-survival-across-restart-01.md`、`.agents/skills/kandev-upgrade/SKILL.md`；新增 ADR 记录 abandoned settlement 与 agent-stream signal resilience 决策；根 `AGENTS.md` 的 observability 条目同步新增 signal/transport metrics。
- **数据 / 迁移**：无新表或新列；effect type 复用现有文本列；signal 持久捕获优先复用 agentctl journal 的 bbolt bucket，不增加 backend SQLite schema；不删除 journal 记录，只 retire；`fully_settled_at` 保持 NULL。
- **前端**：无新增 UI 概念；retry 继续返回既有 `settled`，completion-pending 复用既有 session recovery/error surface；若必须新增文案，按 7 个 locale 同步。
- **测试**：Form A 覆盖 lifecycle 的 dead-owner / nil-proof / terminal-retained 分支、orchestrator 的 settle/幂等/崩溃收敛、journal retirement 与 native resume 忽略 retired peer、SQLite + PostgreSQL 事务行为；Form B 覆盖 successor 不再 blocked 与 live 非 successor 仍 blocked；部署门禁覆盖 busy/idle/`--force`，launcher 单元测试断言新的停止预算；Form C 覆盖断流后信号重放/去重、event backlog 下的控制请求、completion-pending 可见性、ping/pong/write deadline 的 typed disconnect reason。
- **明确不做**：不伪造 terminal event/sequence；不在 owner 未证明死亡或存在独立 live/ambiguous 工作时结算；不做启动/后台自动 sweep；不放宽 archived task、Office/automation 的恢复边界；不做 remote executor 的保留 journal 手术；不改变 pre-intake absence proof 的语义；不删除 journal 记录；不把 durable replay 扩展到所有非幂等 MCP 工具；不重做 NTFS/SQLite 性能；不改变 step_complete 的 accepted/stale 语义；不新增前端概念（复用既有 recovery/error surface）。
