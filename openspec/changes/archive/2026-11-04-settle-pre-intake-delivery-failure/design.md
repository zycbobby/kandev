## Context

durable delivery 的恢复模型以「journal 是保留证据」为前提。当前实现有三条相互独立的 fail-closed 规则：

- `selectReconstructionEvidence`（`apps/backend/internal/orchestrator/session_delivery_reconstruction.go:139`）要求保留 journal 中恰好有一个未解决候选，才能重建丢失的 SQL 关联。
- `validateNativeResumeSQLRows`（`apps/backend/internal/agent/runtime/lifecycle/native_resume_evidence.go:35`）要求 SQL 中每个活跃 submission 都能在保留 journal 中找到匹配证据。
- `AdmitDeliverySubmission`（`apps/backend/internal/agentctl/server/process/manager.go:676`）在 `deliveryBlocked` 或 storage pressure 时拒绝新 prompt，但该状态不进入 `DeliveryCapability()`。

线上会话 062a1281 暴露了三条规则之间的空隙：agentctl 在 22:39:41 一次 durable event commit 失败后把 `deliveryBlocked` 置为进程级粘滞状态；22:47 的 send-now 替换 prompt 在写 journal 之前被拒收；后端仍按「结果未知」记为 `interrupted_unknown` + `prompt_dispatch_failed` + `unknown_prompt_outcome`。SQL 有活跃 submission，journal 永远没有它，retry 与 native resume 各自要求对方，会话永久死锁。第二个会话 5a9f6ea5 同属 fail-closed 家族，但其 journal 中是 `dispatching` 且任务已归档，属于本设计的 Non-Goal。

本 change 把「dispatch 在 journal intake 之前确定性失败」定义为一等证据类别，并修复 agentctl 的 admission health 可见性与可恢复性。

## Goals / Non-Goals

**Goals:**

- 确定性写前拒收不再产生 `interrupted_unknown` / `unknown_prompt_outcome`；后端直接结算为 `failed`。
- `session.recover` 的 retry 能对「SQL 有 `interrupted_unknown` submission、认证保留 journal 显式证明该 submission ID 不存在」的历史行做事务结算，解析绑定 block，返回 `settled`，并释放被 block 停住的队列。
- agentctl 的 admission health 进入 capability/status，使后端在创建 SQL submission **之前**拒绝不可接纳的 peer。
- 单个不可 journal 的事件（例如超过 per-event 上限）不再永久阻塞 admission 或停止 agent 输出泵；真实存储耗尽仍保持 fail-closed。
- 所有新增行为有后端测试；结算证据保守、可审计、幂等，且不重发历史 prompt。

**Non-Goals:**

- 不处理第二个会话（5a9f6ea5）的归档任务恢复，也不放宽 `dispatching` journal 的 retirement。
- 不增加启动期/维护期的自动 sweep；只做 user-triggered retry 与写前快速结算。
- 不改变 terminal settlement、workflow outcome、native resume 的成功语义。
- 不新增表或列；复用 `agent_delivery_effects` 的文本 `effect_type`。
- 不改前端恢复 UI；复用既有 `settled` 文案。

## Research Log

Plan 阶段的额外调研只覆盖「absence 证据如何通过现有 wire 表达」，未重复 Explore 已确认的事实。

### 未知 P1：如何在现有 agentctl evidence wire 上表达「submission 不存在」
- **验证方式**：读 `journal/retained.go`、`lifecycle/delivery_record_evidence.go`、`delivery_record_transport.go`、`agentctl/server/api/durable_delivery.go:460-481`、`agent/runtime/agentctl/client_delivery.go`。
- **结论（试通证据）**：`ReadRetainedReconstructionEvidence` 已支持 `StoredSubmissionID`，`journal.GetSubmission` 返回 `ErrSubmissionNotFound`；HTTP 层把该错误映射为 404 + `SUBMISSION_NOT_FOUND`；live 路径的 `GetDeliverySubmission` 走同一错误映射。因此可以新增 `AbsentSubmissionID`，复用现有 404 语义，不需要新路由或 descriptor 扩容。
- **证据摘要**：`agentctl/journal/retained.go:76-141,355-357`；`agentctl/server/api/durable_delivery.go:472-473`；`agent/runtime/agentctl/client_delivery.go:151-158`。

### 未知 P2：结算事务能否复用现有锁与 effect 存储
- **验证方式**：读 `sqlite/agent_delivery_settlement.go`、`sqlite/agent_delivery_locks.go`、`sqlite/session_recovery_revision.go`、`sqlite/agent_delivery.go:68-88`。
- **结论（试通证据）**：`agent_delivery_effects` 的 `stream_id`/`sequence` 是 `NOT NULL`，可用空串与 0；`ListPendingAgentDeliverySettlements` 按 `effect_type = 'agent_delivery.terminal_settlement'` 过滤，新 effect type 不会被误结算；`lockSessionTurnWrites`/`lockAgentDeliveryWrites`/`advanceRecoverySessionRevisionTx` 可直接复用。
- **证据摘要**：`sqlite/agent_delivery.go:68-82`；`sqlite/agent_delivery_settlement.go:363-396`；`sqlite/agent_delivery_locks.go:18-38`；`sqlite/session_recovery_revision.go:11-17`。

### 未知 P3：写前拒收的 typed error 能否穿过 lifecycle/executor 到 orchestrator
- **验证方式**：读 `agent/runtime/agentctl/agent.go:477-526`、`lifecycle/session.go:2216,2314-2374`、`orchestrator/executor/executor_interaction.go:687-720`。
- **结论（试通证据）**：`client.prompt` 返回的错误由 `callAgentctlPrompt` 原样返回；`sendPrompt` 用 `fmt.Errorf("failed to trigger prompt: %w", err)` 包装；`errors.Is` 可穿透。executor 路径不吞错。需要新增的只是 `ws.ErrorPayload.Details["kind"]` 的解析。
- **证据摘要**：`pkg/websocket/message.go:78-82,129-146`；`agent/runtime/agentctl/agent.go:497-526`；`lifecycle/session.go:2205-2216,2314-2347`。

## Decisions

### D1：absence 证据用显式 ID 查询，不扩 descriptor 库存
- **选择**：在 `RetainedReconstructionEvidenceRequest` 与 `DeliveryRecordEvidenceRequest` 增加 `AbsentSubmissionID`；在结果中增加 `SubmissionAbsent bool`。live 路径用 `GetDeliverySubmission` 的 404/`SUBMISSION_NOT_FOUND` 判定；retained 路径用 `GetSubmission` 的 `ErrSubmissionNotFound`。request 级 before/after descriptor 比较保证快照稳定。
- **理由**：on-demand、payload 小、不影响每次 status 轮询；absence 是精确 ID 事实，不需要枚举全部 submission。
- **已考虑 alternative**：在 `RecoveryDescriptor` 增加 `RetainedSubmissionIDs []string` —— 会让每次 capability/status 响应随会话历史增长，并需要新的截断边界；仅凭 `SubmissionCount == 0` 推断 —— 无法区分「从未写入」与「已 prune/retire/属于其它 generation」。

### D2：pre-intake settlement 使用独立 effect type，submission 结算为 `failed`
- **选择**：新增 `agent_delivery.pre_intake_settlement` effect type，key 为 `agent_delivery.pre_intake_settlement:<submissionID>`，`state=completed`、`stream_id=''`、`sequence=0`、`turn_id=''`、`outcome='failed'`。submission `state=failed`、`outcome=failed`、`updated_at=settledAt`；`terminal_stream_id`/`terminal_sequence`/`terminal_turn_id` 保持空，`fully_settled_at` 保持 NULL。
- **理由**：没有 terminal event、turn 或 workflow outcome，伪造 `fully_settled_at` 会声称一个系统无法证明的完整 terminal settlement，并与 retention proof 冲突。空 stream/sequence 不满足 `agent_delivery.terminal_settlement` 的既有不变量，因此必须使用独立 effect type。`failed` 是既有终态，`terminalDeliverySubmission`、admission 和 recovery-notice 清理都按终态处理。
- **已考虑 alternative**：复用 `agent_delivery.terminal_settlement` + sequence 0 —— `ListPendingAgentDeliverySettlements` 会把它当 pending terminal 再结算并报 sequence 错误；删除 submission 行 —— 丢失不可变审计记录。

### D3：retry 只在保守证据全满足时结算
- **选择**：`retryMissingCanonicalDeliverySubmission` 先以 `AbsentSubmissionID = block.DeliverySubmissionID` 读取一次 evidence。满足以下全部条件才结算：block consumer 为 `agent_delivery`、`delivery_submission_id` 精确匹配、`delivery_stream_id=''`、incarnation/generation 匹配；descriptor `Durable`、版本受支持、`SubmissionsTruncated=false`、`SubmissionCount==0`；`SubmissionAbsent=true`；SQL submission `interrupted_unknown`、outcome `prompt_dispatch_failed`、`terminal_sequence=0`、`fully_settled_at IS NULL`、owner 匹配。
- **理由**：`SubmissionCount==0` 排除其它未解决候选；空 stream 保证 block 是 pre-intake 形态；owner/generation 检查防止把其它 generation 的缺失当成当前缺失。
- **已考虑 alternative**：只要 absence 就结算 —— 忽略其它未解决候选与 block 绑定；允许 `delivery_stream_id` 非空 —— 那是「journal 曾收到但结果未知」的形态，不能当作未投递。

### D4：写前拒收使用 typed wire 语义并即时结算
- **选择**：`SubmissionDelivery.Admit` 区分两类失败：`PutSubmission`/pressure/blocked 失败返回 `ErrDeliveryAdmissionRejected`（确定未写）；`PutSubmission` 成功但 `TransitionSubmission` 失败返回 `ErrDeliveryAdmissionUncertain`（可能已写）。`admitDurablePrompt` 把前者映射为 `Details{kind:"durable_delivery_admission_rejected", reason:...}`，后者映射为 `kind:"durable_delivery_admission_uncertain"`。`client.prompt` 把 rejected kind 转为 `agentruntime.ErrDurableDeliveryAdmissionRejected`。orchestrator 在 `!dispatchAccepted && errors.Is(execErr, ...)` 时调用即时结算；结算失败则回退到既有 `interrupted_unknown` + block 路径。
- **理由**：只有「确定未写」可以安全结算；「可能已写」必须保持不确定，由 journal 证据决定。即时结算避免制造新的 stuck 行；回退保证数据库错误不会丢失不确定性。
- **已考虑 alternative**：所有 admission 错误直接结算 —— 会把「已写入但响应丢失」误判为未投递；只做 retry 结算 —— 新事故仍会产生一条 stuck 行，违背「不再记录可 diverge 的 submission」目标。

### D5：admission health 进入 capability/status，但不影响 evidence 可读性
- **选择**：`journal.StorageCapability` 和 `lifecycle.DurableDeliveryCapability` 增加 `AdmissionBlocked bool`。`process.Manager.DeliveryCapability()` 在 `deliveryBlocked` 或 `deliveryPumpStopped` 时置位，同时保持 `Durable=true`（journal 仍可读）。`deliveryCapabilityForExecution` 在 `AdmissionBlocked` 时直接返回 `DurableDeliveryBlocked{Reason:"delivery_admission_blocked"}`；`resolveDeliveryProtocol` 对该 reason 返回 typed transient error（不建 recovery block、不建 submission）。
- **理由**：把拒绝提前到 SQL submission 创建之前；`Durable=true` 让 recovery/absence evidence 仍可读取被 blocked 进程的 journal。
- **已考虑 alternative**：让 `Durable=false` —— 会导致 `validateDeliveryRecordDescriptor` 拒绝 recovery evidence，也无法区分「journal 不可用」与「暂时不可接纳」。

### D6：不可 journal 的事件跳过而不是毒化进程
- **选择**：新增 `journal.ErrEventTooLarge`（同时 wrap `ErrStreamFull` 以兼容既有判断）。`prepareAppendEvents` 的超限与 writer 队列超限返回该错误。`forwardUpdates` 对 `ErrEventTooLarge` 记 ERROR + 有界 metric 后继续消费下一个事件，不调用 `recordDeliveryStorageFailure`。真实 ENOSPC/EDQUOT 仍由 `appendDeliveryWithRecovery` 等待/重试；若泵最终退出，`deliveryPumpStopped` 保持 admission blocked 直到进程重启。`applyDeliveryCapacityLocked` 在 `capacity.Recovered && !deliveryPumpStopped` 时清除 `storage_error`。
- **理由**：单个超大事件不应让整个 agent 进程永久失去 admission 与输出；跳过是可观测的（日志 + metric），且不影响真实存储耗尽的 fail-closed。
- **已考虑 alternative**：继续使用当前「停止泵 + 永久 blocked」—— 正是本次事故的放大器；把超大 payload 截断后写入 —— 改变输出语义，且 provider 输出可能无法安全截断。

### D7：新增两个有界 metric
- **选择**：`agent_delivery_admission_rejected_total{reason}`（closed set：`storage_pressure`、`storage_error`、`journal_full`、`event_too_large`、`journal_corrupt`、`owner_mismatch`、`unknown`）与 `agent_delivery_event_rejected_total{reason}`（`event_too_large`、`queue_too_large`），放在 `agentctl/journal/metrics.go`，并更新 `boundedDeliveryLabels`。
- **理由**：现有 `agent_delivery_journal_errors_total` 混合 journal fault 与 event rejection；恢复排障需要区分「确定未投递」与「存储故障」。
- **已考虑 alternative**：只写日志 —— 运维无法聚合；复用 journal errors —— 丢失原因维度。

### D8：retry 结算后释放 parked 队列；即时结算不释放
- **选择**：retry 成功结算后调用 `restorePendingQueueDispatchesForRecovery`（失败只记 WARN）；即时结算不调用，因为既有 dispatch failure 路径已经恢复 queue/send-now claim。
- **理由**：避免与 send-now worker 自己的 restore 竞态；retry 路径的队列确实被 block 停住，需要释放。
- **已考虑 alternative**：两个路径都释放 —— 可能双恢复或与 worker 竞态。

## Risks / Trade-offs

- [Risk] absence 证明依赖 journal submission 记录永不被删除 → Mitigation: 现状 `RetireSubmission` 只标记 `Retired`，`Compact` 复制所有 bucket；在 journal 测试中固定该不变量，并在 design/spec 中记录：未来若引入 submission 删除，必须先更新本证明。
- [Risk] 跳过超大事件会丢失一段 agent 输出 → Mitigation: ERROR 日志 + 有界 metric；与「永久 wedge 整个会话」相比是更小的损失。该取舍写入 ADR 与 Known Failure Modes。
- [Risk] `DeliveryCapability()` 新增读取 `deliveryActiveMu` 可能引入锁序问题 → Mitigation: 保持 journal→active 的既有顺序；新增并发测试（`go test -race`）。
- [Risk] 即时结算与迟到 journal 写入竞态 → Mitigation: 同一 submission ID 在非终态时不会被后端重新 admission（`admitBackendDeliverySubmission` 返回 `delivery_submission_requires_reconciliation`）；结算事务再次校验 SQL 状态与 `terminal_sequence`，冲突则回退到不确定路径。
- [Risk] PostgreSQL 锁行为与 SQLite 不同 → Mitigation: 新事务复用现有 `lockSessionTurnWrites`/`lockAgentDeliveryWrites`，并添加 `KANDEV_TEST_POSTGRES_DSN` gated 行为测试。
- [Trade-off] `fully_settled_at` 保持 NULL，pre-intake prompt payload 不会被 retention 清理 → 接受：payload ≤ 1 MiB，且不伪造 terminal proof；后续可单独为 pre-intake effect 增加 retention proof。
- [Trade-off] 第二个会话仍保持 blocked → 接受：任务已归档，按 AC-006.23 不属于普通恢复；单独后续处理。

## Migration Plan

- 无 schema migration；`agent_delivery_effects` 复用文本列，新 effect type 是数据。
- 部署顺序：backend 与 agentctl 同版本发布（同一 Go 模块）。旧 agentctl 不返回 `AdmissionBlocked` 时按 false 处理，行为退回当前路径；旧 backend 不认识新的 `kind` 时回退到既有 uncertain 路径，不会误结算。
- 回滚：回滚二进制即可；已结算的 `failed` submission 与 `pre_intake` effect 保持终态，不会被旧版本重新激活（旧版本把它们当 terminal）。
- 验收条件：新增测试通过；`openspec validate` 通过；部署后对受影响会话点击 Retry connection 得到 `settled`，block 消失，队列消息可按既有策略发送。

## Open Questions

无阻塞问题。以下为明确延期项：

- 启动期/维护期自动 sweep（需要对每个 session 取 runtime 证据，成本与授权边界未定）。
- 第二个会话的归档任务清理与 `dispatching` journal retirement 放宽。
- pre-intake settlement 的 prompt payload retention 证明。
