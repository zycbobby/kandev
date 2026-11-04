## Context

本 change 处理 2026-11-04 部署事故暴露的三类问题：

1. **Form A**：agentctl 在写 terminal event 前退出，journal submission 停留在 `interrupted_unknown`，没有 `terminal_event_retained` / `terminal_sequence`。SQL 侧该 submission 可能是 active `interrupted_unknown`，也可能已被其它路径标为终态。retry 的 retained 路径固定返回 `terminal_outcome_missing`；native resume 对 journal-only unknown peer 要求不存在的 terminal evidence，对 SQL 已终态 peer 又拒绝 retire；journal 的 `Unresolved` 同时阻塞新 prompt 与 resume。
2. **Form B**：recovery metadata 绑定旧 execution（如 `e9c352eb`），但同一次 resume 已把 `executors_running.agent_execution_id` 重绑到 successor execution（如 `11faeca6`）。`recoverySessionMatches` 的 execution ID 等值比较在读取任何 journal/runtime 证据之前 fail，运行时已有的 `loadedRecoverySuccessorMatches` + `proveHistoricalDeliveryOwnerDead` 路径没有机会执行。
3. **部署安全**：`scripts/deploy-user-service.sh` 在 publish 后直接 `service restart`，不检查在途工作；systemd unit 的 `TimeoutStopSec=30s` 小于后端 10s HTTP + 20s agents + 5s tracing 的优雅关闭预算，重启会截断 agent drain。
4. **Form C（agent-stream 信号韧性）**：backend↔agentctl 的 `/api/v1/agent/stream` WebSocket 同时承载 agent 输出事件和 MCP 请求。`step_complete_kandev` 请求经无缓冲 `requestCh` + 5s send timeout 进入 writer；writer 与 agent-event 共用循环，没有 ping/pong 与写 deadline；客户端 event queue 满时关闭 stream。2026-11-04 12:15:18 / 12:17:30 两次断线导致信号从未到达 backend，workflow 停在 completion-pending 直到新用户轮次；并行任务也丢了 3 次 MCP 工具调用。现场 SQLite 写争用（`writer_wait_count=2281`、`writer_ping deadline_exceeded`）会放大 handler 阻塞。

本 change 的 proposal 与四个 spec delta 已定义 WHAT；本文档定义 HOW、互操作接口、失败边界与交付顺序。现有 `durable-delivery-pre-intake-settlement` 与 `durable-delivery-admission-health` 的语义保持不变。

## Goals / Non-Goals

**Goals:**

- 为 journal-present、无 terminal evidence、owner 已证明死亡的 durable submission 提供保守、可审计、幂等、崩溃可收敛的结算路径。
- 在同一 retry 流程中 retire 目标及所有可证明的 extra unproven journal peer，使 journal `Unresolved` 归零，新 prompt 与 native resume 恢复可用。
- 让 retry 接受 recovery 流程自己创建的 successor execution，同时保持 fail-closed 的 ownership 校验。
- 在部署 publish 之前只读检查活跃工作；busy 时拒绝并列出 task，`--force` 显式覆盖。
- 把生成的 systemd 停止预算提升到覆盖后端优雅关闭预算。
- 让 `step_complete_kandev` 信号在 agent-stream 瞬断/背压下持久捕获、重连重放，并让 completion-pending 可见、可恢复。
- 让 MCP 控制请求不被 agent-event backlog 饿死；agent-stream 具备 ping/pong、读写 deadline 与 typed disconnect reason。
- 所有行为有后端测试、shell 测试或 launcher 测试；不新增表/列，不改前端流程。

**Non-Goals:**

- 不伪造 terminal event、terminal sequence、`fully_settled_at` 或 `completed`/`cancelled` 结果。
- 不在 owner 未证明死亡、或存在独立 live/ambiguous 工作时结算。
- 不处理 journal `prepared`/`accepted`/`dispatching` 的 abandoned 形态；本期只处理 `interrupted_unknown` + 无 terminal。
- 不做启动期/维护期自动 sweep；只做用户触发的 retry。
- 不做 remote executor 的保留 journal 手术；只覆盖本地 retained runtime 可达的 journal。
- 不放宽 archived task、Office/automation 所有者的恢复边界。
- 不改变 pre-intake absence proof 的语义；不删除 journal 记录。
- 不修改 macOS launchd 的 `ExitTimeOut`。
- 不把 durable capture/replay 扩展到所有非幂等 MCP 工具；只对 `step_complete` 等幂等信号启用。
- 不重做 NTFS/SQLite 性能或 writer 架构；本 change 只观测争用并让 signal capture 不依赖 backend writer。
- 不改变 `step_complete` 的 accepted/stale 语义；不新增前端概念，completion-pending 复用既有 recovery/error surface。
- 不解决 backend 完全不可达、且 agentctl 本地 capture store 也损坏时的送达；该场景由 typed failure + agent 自救路径处理。

## Research Log

Plan 阶段没有执行新的 bounded research：proposal 的 `## Research Log` 已覆盖四条关键未知，且没有未解决 blocker。以下是设计所依赖的已确认输入摘要（详见 proposal）：

- retry retained 路径的固定 uncertain：`lifecycle/retained_delivery_recovery.go:54-57`；`lifecycle/delivery_recovery_without_execution.go:133-140`。
- native resume 的 terminal evidence / retirement 门槛：`lifecycle/native_resume_terminal.go:31-33,47-53`；`lifecycle/native_resume_delivery.go:110-120`；`lifecycle/native_resume_state_matrix_test.go:18-26`。
- journal `Unresolved` 到 admission 的传导：`agentctl/server/process/manager.go:553-566`；`lifecycle/durable_delivery_activation.go:48-49`。
- pre-intake 不可复用的边界：`orchestrator/pre_intake_delivery_settlement.go:21-55`；`sqlite/agent_delivery_pre_intake_settlement.go:110-124,237-256`。
- `RetireSubmission` 的同代语义：`journal/journal_submissions.go:84-130`；`journal/retired_stream.go:11-14`。
- Form B 的既有 successor 路径：`lifecycle/delivery_recovery_without_execution.go:43-51`；`lifecycle/already_resumed_recovery.go:34-77`；orchestrator guard：`orchestrator/session_delivery_recovery.go:213-216,421-429`。
- 部署脚本与预算：`scripts/deploy-user-service.sh:218-238`；`launcher/service.go:625`；`backendapp/helpers.go:120-122,2815-2870`。
- Form C 传输路径：`mcp/server/backend_client.go` 的 `ChannelBackendClient.RequestPayload`（无缓冲 `requestCh`、5s send timeout、pending response channel）；`agentctl/server/api/agent.go` 的 `runAgentStreamWriterLoop` / `writeAgentStreamMCPRequest`（无 ping/pong、无写 deadline）；`agent/runtime/agentctl/agent.go` 的 `readUpdatesStream`（event queue 满时 `errAgentEventQueueFull` 关闭 stream）；`orchestrator/stuck_signal_watchdog.go` 只处理已持久化 signal；`persistence/requiredstores/health.go` 的 `writer_ping` / `writer_wait_count`。
- 事故证据（用户任务详情）：12:15:18 / 12:17:30 两次 stream 断线重连；`step_complete_kandev` 信号未到 backend，workflow 停在 completion-pending 直到新用户轮次；同窗口并行任务丢失 3 次 MCP 工具调用；启动时 `writer_wait_count=2281`、`writer_ping deadline_exceeded`。

## Decisions

### D1：新增 sibling settlement 与独立 effect type

- **选择**：新增 `agent_delivery.abandoned_journal_settlement:<submission_id>` effect（state `completed`、stream `''`、sequence `0`、turn `''`、outcome `failed`）；SQL submission 置为 `failed`/`failed`，`terminal_sequence=0`、`fully_settled_at IS NULL`。block 的 `authorized_action` 使用 `durable_delivery_abandoned`，`delivery_outcome=failed`。
- **理由**：保留「从未写入 journal」（pre-intake）与「写入后遗弃」（abandoned）的审计区分；不复用 terminal effect，避免 `ListPendingAgentDeliverySettlements` 把无 terminal 的结算当作不完整 terminal 再处理。
- **已考虑 alternative**：复用 `agent_delivery.pre_intake_settlement`（语义冲突，审计不可区分）；terminal effect + sequence 0（会被 pending terminal 扫描，且 sequence 0 违反既有不变量）；向 journal 写 terminal event（伪造系统无法证明的结果）；删除 journal 记录（丢失证据）。

### D2：journal 只 retire、不删除；retire 所有可证明的 extra peer

- **选择**：对目标 submission 和 descriptor 中所有其它 `interrupted_unknown`、无 terminal evidence、SQL 行不存在或已终态的 peer 执行 `RetireSubmission`；要求 descriptor 的完整 unresolved inventory 只包含这些可 retire 记录。任何 `prepared`/`accepted`/`dispatching`、或 `completed`/`failed`/`cancelled` 但无 terminal evidence 的记录都会让整个结算 blocked。
- **理由**：journal `Unresolved` 是 session 级；只 retire 目标会留下其它 unresolved peer，新 prompt 仍被 `unresolved_durable_work` 阻塞。`RetireSubmission` 保留 state/payload，同代不 seal 当前 stream，是既有 ADR 认可的「acknowledge unknown history」机制。
- **已考虑 alternative**：只 retire 目标（admission 仍 blocked）；删除记录（违反保留证据不变量）；把所有 active SQL 行都结算（会覆盖无关工作）；把 journal state 改成 `failed`（重写历史标签）。

### D3：journal mutation 由 runtime 拥有；file-only 增加 control retained retire 路由

- **选择**：新增 `Manager.RetireDeliveryRecordSubmissions(ctx, DeliveryRecordRetirementRequest) (*DeliveryRecordRetirementResult, error)`。它重新读取 journal，校验 exact summaries、owner 终止、无 terminal，live instance 时使用 per-instance `RetireDeliverySubmission`，file-only 时通过新的 `POST /api/v1/delivery/retained/retire` control 路由 retire；随后重新读取并确认 retired + 无 unresolved。
- **理由**：journal mutation 需要 authenticated ownership、owner marker 和 death proof；orchestrator 不应直接写 bbolt。file-only 路径没有 per-instance client，必须有一个只做 retire 的 control 端点。
- **已考虑 alternative**：orchestrator 直接打开 bbolt 写（绕过 runtime ownership）；复用 `ReadRetainedRecovery` 的 Acknowledge 语义（不是 retire）；把 file-only 的 journal 复制到 backend 再写（副本失去 owner 证明）。

### D4：retry 在既有 reconciliation 之前显式尝试 abandoned settlement

- **选择**：在 `retryKnownSessionDelivery` 中，submission 非终态且 block 绑定匹配时，先调用新的 `retryAbandonedJournalDelivery`；它用 `InspectDeliveryRecordEvidence(StoredSubmissionID=recovery.SubmissionID, OriginalRuntime=recovery.OriginalRuntime)` 读取证据，做完整 eligibility 检查，调用 runtime retire，再提交 repository settlement。证据不匹配（terminal retained、submission absent、其它状态）时回落到既有 `retryVerifiedSessionDelivery`；证据存在但不符合 abandoned 形状时返回具体 blocked reason。
- **理由**：与 pre-intake 的「先证据、后事务」结构一致，便于测试 idempotency/崩溃窗口；不把 SQL 责任塞进 runtime，也不把 journal 写责任塞进 repository。
- **已考虑 alternative**：把 settlement 折进 `recoverRetainedJournal`（runtime 没有 SQL 仓库，无法完成事务）；启动 sweep（超出范围）；让 native resume 直接 settle（违反 AC-006.9 的“preserve uncertainty”）。

### D5：Form B 去掉 execution ID 等值比较

- **选择**：`recoverySessionMatches` 保留 session、incarnation、generation、submission、stream、promptGeneration 校验；移除 `(session.AgentExecutionID == "" || recovery.AgentExecutionID == session.AgentExecutionID)`。execution 归属完全交给运行时 `loadedRecoverySuccessorMatches` + `proveHistoricalDeliveryOwnerDead`。
- **理由**：数据库层不知道 successor 生命周期；运行时已经具备同 task/session/incarnation/generation、ready、initialized、无 pending prompt 和 dead-owner 证明。重复在 DB 层实现会漂移。
- **已考虑 alternative**：新增 DB successor 查询（重复 runtime 逻辑）；由 runtime 先回传 successor 布尔再进 DB guard（多一次 round trip，且仍需要 runtime 证据）。

### D6：native resume 忽略 retired peer

- **选择**：`nativeResumeSQLRows` 的 peer loop 跳过 `submission.Retired`。`nativeResumeRetirementIDs` 已有的 `if submission.Retired { continue }` 保持不变。`validateNativeResumeSQLRows` 只遍历 active SQL rows，retired peer 不在其中。
- **理由**：settlement 把 SQL 行置为 failed 后，目标不再出现在 active SQL rows；如果 peer loop 不跳过 retired 记录，它会重新进入 `peerMatchesTerminalHistory` 并报 “terminal evidence disagrees”，使结算后的 resume 再次失败。
- **已考虑 alternative**：把 journal state 改成 failed（重写历史）；让 SQL 行保持 interrupted_unknown（用户要求 settlement，且 active 行会继续参与其它检查）。

### D7：deploy gate 放在 publish 之前，只读 SQLite，busy 拒绝

- **选择**：`scripts/deploy-user-service.sh` 新增 `check_deploy_idle`，在 `refuse_isolated_home` 之后、`publish_bundle` 之前执行。数据源：unit/plist 的 `KANDEV_DATABASE_PATH`，否则 `<home>/data/kandev.db`。查询：
  - `task_sessions.state IN ('STARTING','RUNNING')`；
  - `agent_delivery_submissions.state IN ('prepared','accepted','dispatching','interrupted_unknown')`；
  - `session_recovery_blocks.state='open'`；
  - 通过 `tasks.archived_at IS NULL` 排除 archived task。
  DB 不存在：服务未运行则视为全新安装；服务运行则 refusal。DB 存在但不可读/表缺失：refusal。`--force` 跳过 gate 并在输出中说明。
- **理由**：拒绝必须在替换 live `runtime/bin` 之前；python3 已是脚本依赖；SQLite 只读查询不需要新 API。`--force` 解决从运行中 session 内自举部署的问题。
- **已考虑 alternative**：新增 backend HTTP endpoint（增加 API surface，且脚本仍需解析 port/auth）；Go launcher 子命令（更大改动）；`sqlite3` CLI（不保证安装）；把 gate 放在 restart 之前但 publish 之后（拒绝时已替换 runtime）。

### D8：systemd `TimeoutStopSec=60s`

- **选择**：`renderSystemdUnit` 输出 `TimeoutStopSec=60s`；更新 `service_test.go` 的精确断言和相关 docs/design。
- **理由**：后端预算 10s HTTP + 20s agents + 5s tracing = 35s，60s 留出 25s 余量；90s 会让 hung backend 的 force-kill 延迟更久。
- **已考虑 alternative**：90s（systemd 默认，但延迟 force-kill）；45s（余量不足）；只改脚本不改 unit（unit 才是 systemd 实际使用的值）。

### D9：沿用既有协议/事务形状，不新增 schema 或 UI

- **选择**：新 effect type 复用 `agent_delivery_effects` 的文本列；新 repository 事务复用 `lockSessionTurnWrites` / `lockAgentDeliveryWrites` / `advanceRecoverySessionRevisionTx` / `insertDeliveryEffectTx`；retry 仍返回既有 `settled`；新的 blocked reason 走 `publicDeliveryRecoveryReason` 的回退（`recovery_unavailable`）或既有 reason。
- **理由**：最小影响面；避免 schema migration 和 7 语言 i18n 变更。
- **已考虑 alternative**：新增列/表（不必要）；新增 UI 状态（settled 已足够）；新增 metric（可选，但本期不强制）。

### D10：agent-stream 健康检查与背压隔离

- **选择**：在 agentctl 的 agent-stream server 和 backend 的 stream client 两侧加入 ping/pong 与显式 read/write deadline；每次断线记录 typed reason（`event_queue_full`、`write_timeout`、`ping_timeout`、`read_timeout`、`owner_changed`、`read_error`）；writer 循环对 MCP 控制请求使用独立的有界优先通道，不再与 agent-event backlog 共用单一阻塞路径；client 的 event queue 满时关闭 stream 的行为保留，但必须带 typed reason 并触发 capture/retry。
- **理由**：本次事故的直接机制是 writer 被事件/断线阻塞时，MCP 请求连入流都做不到；没有 ping/pong 时半开连接只能等 TCP 超时，扩大丢失窗口。
- **已考虑 alternative**：单纯增大 5s send timeout（治标，仍会丢失）；无限缓冲（内存不可控）；只在客户端重连（server 侧 writer 仍会阻塞）。

### D11：step_complete 信号的持久捕获与幂等重放

- **选择**：在 agentctl 本地 journal 新增 `pending_signals` bucket（bbolt），记录 request ID、action、payload、session ID、created_at、delivered_at、attempt count。`ChannelBackendClient.RequestPayload` 对 `ws.ActionMCPStepComplete`（以及明确标记为幂等的 signal action）先 capture 再发送；连接建立/重连时 writer 先重放未 delivered 的 signal；收到 backend accepted 或 typed terminal error 后标记 delivered。capture store 满/不可用时返回 typed failure，不退回无界内存。
- **理由**：信号是幂等的 workflow signal，durable capture 能在 agentctl 重启和多次重连后仍送达；使用 agentctl 本地 journal 避免依赖 backend SQLite writer。
- **已考虑 alternative**：只做内存重试（无法跨 agentctl 重启，窗口受 agent 调用 context 限制）；持久化到 backend SQLite（与 writer 争用耦合）；对所有 MCP 工具 durable replay（非幂等工具会重复副作用）。

### D12：completion-pending 可见与 agent 自救

- **选择**：capture 超过 bounded delivery window 仍未被 backend accepted 时，agentctl 追加一个 durable `signal_delivery_failed` agent event（含 request ID、action、session、reason、attempts、first_attempt_at，不含 payload），随 journal 重放在重连后送达 backend；backend 用既有 session recovery/error surface 显示 completion-pending，并保留重发/聊天保底/用户重试。agent 侧 `step_complete_kandev` 在窗口到期后返回 typed failure；重新调用仍走同一 dedup 路径。
- **理由**：backend 从未收到请求时，现有 `stuck_signal_watchdog` 没有持久 signal 可看；durable failure event 让「已尝试但未送达」成为可见事实，同时不新增前端概念。
- **已考虑 alternative**：只依赖 10 分钟 stuck-signal watchdog（对「从未到达」无效）；新增专用 UI 状态（需要 7 语言文案，超出最小范围）；让 agent 无限等待（阻塞 turn）。

### D13：SQLite writer 争用只观测与隔离，不重做性能

- **选择**：signal capture 使用 agentctl journal，不写 backend SQLite；backend 侧 `handleStepComplete` 的延迟由现有 `writer_wait_count` / `writer_ping` / writer duration 指标观测；新增的 transport/signal metric 只记录原因、队列深度和时延，不记录 session/submission/payload 身份。NTFS/SQLite 性能优化不在本 change。
- **理由**：本次事故中 writer 争用是放大器，不是唯一根因；先把控制面信号从 writer 关键路径隔离，避免范围膨胀。
- **已考虑 alternative**：在本 change 内重做 SQLite writer 池或改变 NTFS 存储布局（风险高、与信号韧性目标耦合弱）；把 signal capture 写进 backend DB（会放大争用）。

### D14：只对幂等信号启用 durable replay

- **选择**：`pending_signals` capture/replay 只覆盖 `step_complete` 及明确标记为幂等的 signal action；其它 MCP 工具请求保留有界重试与 typed retryable error，不写入 capture store。
- **理由**：任意 MCP 工具可能有外部副作用；在断流后自动重放可能重复执行工具。`step_complete` 本身是幂等 signal，backend 已有 dedup。
- **已考虑 alternative**：所有 MCP 请求都 durable replay（会重复副作用）；所有请求都不 replay（本次问题不解决）。

## Risks / Trade-offs

- [Risk] journal 已 retire 但 SQL settlement 失败 → Mitigation: eligibility 接受已 retired 的 target；retry 重新提交同一 effect；测试 crash window。
- [Risk] evidence 读取到 retire 之间 journal 变化（TOCTOU）→ Mitigation: runtime retire 重新读取并校验 exact summary/hash/state；descriptor 在 retire 前后比较。
- [Risk] retire extra peers 误伤其它工作 → Mitigation: 要求完整 descriptor inventory + SQL 交叉检查（active SQL 行不允许作为 extra）；任何未知形态 blocked。
- [Risk] `OwnedSessionTerminated` 在 Windows/remote executor 不可用 → Mitigation: fail closed，返回到既有 blocked；remote 不在本期范围。
- [Risk] control retained retire 路由扩大 mutation surface → Mitigation: 仅 unowned session、owner marker、exact identity、death proof；不返回 payload；不提供删除。
- [Risk] deploy gate 误报（从运行中的 session 内 deploy）→ Mitigation: `--force` / `DEPLOY_FORCE=1`；skill 说明该行为。
- [Risk] `TimeoutStopSec` 增大会延迟 hung backend 的 force-kill → Mitigation: 60s 有界；systemd 仍会在超时后 SIGKILL。
- [Trade-off] `fully_settled_at` 保持 NULL → 接受：没有 terminal proof，不能声称完整结算；payload 不进入 terminal-retention 清理，与 pre-intake 的既有权衡一致。
- [Trade-off] rollback 到旧 backend 后，已 retired peer 可能重新触发 terminal evidence 检查 → 接受并记录：回滚后用新版本重新收敛，不进行离线删除。
- [Risk] durable signal replay 可能重复推进 workflow → Mitigation: backend 既有 `handleDuplicateStepComplete` / pending-signal dedup；同一 request ID 重放；跨重发按 step/session 校验；测试覆盖重复送达。
- [Risk] capture store 无限增长或满 → Mitigation: bounded records/TTL；溢出返回 typed failure + metric；不退回无界内存。
- [Risk] ping/pong 与写 deadline 误断健康但慢的 agent → Mitigation: 宽松 deadline、只在读写阻塞时断开、typed reason、自动重连；测试慢消费者。
- [Risk] completion-pending notice 迟到或 UI 不显眼 → Mitigation: 使用 durable event 随 journal 重放；复用既有 recovery/error surface；Apply 时验证可见性；必要时只补最小文案。
- [Risk] 非幂等 MCP 工具在长断流下仍失败 → Mitigation: 有界重试 + typed retryable error；agent/用户自救路径保留；明确不做 durable replay。
- [Trade-off] SQLite writer 争用不在本 change 根治 → 接受：先隔离 signal capture，避免范围膨胀；保留 writer 指标供后续优化。
- [Trade-off] 不新增前端 completion-pending 概念 → 接受：复用既有 recovery/error surface；若现有 surface 不足以表达，Apply 时补充最小文案（7 locale）。

## Migration Plan

- **Schema**：无 migration；新 effect type 是现有文本列中的数据。
- **Form C signal store**：agentctl journal 新增 `pending_signals` bbolt bucket（additive）；确认 compaction/retention 复制该 bucket，不因保留策略丢失 pending signal。旧 backend 不读取该 bucket；旧 agentctl 不产生它。回滚后 pending signal 由新 agentctl 重放，或由 agent 自救路径重发。
- **部署顺序**：backend 与 agentctl 同一 release；旧 agentctl 没有 retained retire route 时，retry 返回 blocked，不产生误结算；旧 agentctl 没有 signal capture 时行为退回当前路径，不会误重放。
- **回滚**：回滚二进制不会重新激活已结算的 `failed` submission；已 retire 的 journal 记录保留 payload/state。若 journal 已 retire 但 SQL 未结算，旧 backend 可能继续 blocked；重新部署新 backend 后可幂等收敛。不要通过删除 journal 记录来“修复”回滚状态。回滚到旧 agentctl 会忽略 `pending_signals`，未送达信标由 agent/用户自救；重新部署新 agentctl 后可重放。
- **验收**：新增测试通过；`openspec validate --strict` 通过；部署后对受影响会话执行 Retry，预期得到 `settled`，block 消失，新 prompt 可发送；在断流测试中 `step_complete` 信号在重连后送达且只推进一次。

## Open Questions

以下均为非阻塞命名/实现细节，Apply 前可由实现者按推荐值落地：

1. runtime 方法名与 control 路由路径：推荐 `RetireDeliveryRecordSubmissions` 与 `POST /api/v1/delivery/retained/retire`。
2. retire 请求是否携带 descriptor 指纹：推荐携带 exact submission summaries + hash，由 runtime 重新读取校验；不引入整份 descriptor 指纹。
3. 是否新增 `agent_delivery_abandoned_settlement_total` metric：本期不做；若 Apply 发现排障需要，可作为后续 change。
4. recovery metadata 清理位置：推荐 orchestrator 在 SQL settlement 提交成功后调用既有 `clearAgentDeliveryRecoveryNotice`，不在 repository 事务内跨表删除 metadata。
5. `make deploy` 的覆盖变量名：推荐 `DEPLOY_FORCE=1`，与现有 `NO_BOOT_START=1` 风格一致。
6. Form C 的 delivery window / capture TTL / max records：推荐 window 30–60s、TTL 24h、最多 256 条；Apply 时按测试结果定值。
7. capture store 位置：推荐复用 agentctl journal 的新 bucket，并验证 compaction 复制；若 journal 关闭/不可用，回退为 typed failure，不退回内存。
8. completion-pending 的 backend surface：推荐复用既有 session recovery/error card；若不足以表达，只补最小文案，不新增 UI 概念。
9. ping/pong 与 deadline 参数：推荐 ping 30s、pong timeout 90s、write deadline 10s，全部可配置；Apply 时用慢消费者测试校准。
10. Form C metric 名称与 label：推荐 `agent_stream_disconnect_total{reason}`、`agent_stream_mcp_request_failures_total{reason}`、`workflow_step_signal_capture_total`、`workflow_step_signal_replay_total`、`workflow_step_signal_delivery_failed_total`；label 仅闭集 reason，不带 session/submission/payload。
11. 其它可 durable replay 的幂等 signal action：本期只覆盖 `step_complete`；新增 action 必须先证明幂等并补 dedup 测试。
