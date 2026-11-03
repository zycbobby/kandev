## Why

durable delivery 的 fail-closed 设计假设「不确定」总意味着 submission 已经进入 agentctl journal、只是结果未知。但现实还有第二种情况：agentctl 在写 journal 之前就确定性拒收（transport 未发送、运行期 storage error 导致 `deliveryBlocked`、admission 前置检查失败）。此时后端仍把 submission 记为 `interrupted_unknown` + `unknown_prompt_outcome`，而 journal 里根本没有它。随后 retry 要求 journal 恰好有一个候选、native resume 要求 SQL 每个活跃 submission 都存在于 journal，两侧互相要求，会话永久死锁，只能离线改 SQLite。线上已有真实会话中招（062a1281 及 5a9f6ea5 家族）。一次 journal 写失败会让同一 agentctl 进程后续所有 prompt 都走进这个陷阱，因此现在必须把「确定性未投递」变成一等公民，而不是继续当作「不确定」。

## What Changes

**确定性拒收的落库语义**
- From: dispatch 返回错误一律记 `interrupted_unknown`（outcome `prompt_dispatch_failed`）并创建 `unknown_prompt_outcome` 恢复块。
- To: 仅当无法证明未入 journal 时保持不确定；agentctl 明确在写 journal 前拒收时，后端把 submission 结算为 `failed`，不创建恢复块。
- Reason: 把「确定没送到」误判为「可能送到」是本次死锁的根因。
- Impact: non-breaking；失败的 send-now/排队 prompt 仍保留用户消息，但不再锁死会话。

**pre-intake 缺失的恢复分支**
- From: `session.recover` 的 retry 只支持「journal 有唯一候选」的 reconstruction；journal 无记录时返回 `retained_evidence_ambiguous`，永久 blocked。
- To: retry 在满足认证证据时证明「该 submission 从未进入保留 journal」，在同一事务里结算为 `failed`、解析绑定 block、返回 `settled`，并释放被 block 停住的队列。
- Reason: 已存在的 stuck 会话需要一条真正的运行时恢复路径，而不是离线改库。
- Impact: 新增恢复结果语义；前端复用现有 `settled` 文案，无新增用户可见概念。

**agentctl admission health**
- From: `DeliveryCapability()` 只反映 journal 初始化错误与 unresolved submission，不反映运行期 `deliveryBlocked`；一次 persist 失败会把 blocked 置为进程级粘滞状态，并停止 durable event pump。
- To: capability/status 反映 admission health；backend 在创建 SQL submission 之前拒绝不可接纳的 peer；blocked 状态在容量恢复或成功提交后可恢复，event pump 可重启。
- Reason: 防止同一进程重复制造 SQL/journal 分叉。
- Impact: agentctl 行为修正；capacity 真正耗尽时仍保持 fail-closed，Stop/status/replay 可用。

**admission 拒收可观测**
- From: agentctl 只返回 "prompt submission could not be admitted"，底层 error 被丢弃，日志不可诊断。
- To: 返回 typed `kind`/`reason`（写前拒收、写后 transition 失败、unresolved work、storage pressure 可区分），backend 保留并记录；新增有界 metric。
- Reason: 恢复路径与告警需要区分「确定未投递」和「可能已投递」。
- Impact: 新增 wire 字段（向后兼容）与有界 metric 标签。

## Capabilities

### New Capabilities
- `durable-delivery-pre-intake-settlement`: 后端对「确定性未进入 journal」的 durable submission 的证据规则、结算事务、retry 恢复分支与幂等/边界约束。
- `durable-delivery-admission-health`: agentctl 的 admission health 对外可见性、blocked 状态可恢复性，以及 typed admission 拒收契约。

### Modified Capabilities
（无。`openspec/specs/` 下没有既有的 durable delivery capability；产品契约在 `docs/specs/platform/`，将在 Apply 阶段同步更新，见 Impact。）

## Research Log

### 未知 1：事故是否由 send-now 取消与提交竞争导致？
- **验证方式**：读后端日志时间线 + send-now 取消/派发代码（`queue_send_now.go:322-341`、`event_handlers_clarification.go:1011-1049`、`task_operations.go:10672-10723`）。
- **结论（试通证据）**：否。取消在 22:47:52.953 完成，send-now claim 22:47:52.976、prompt 22:47:53.017，晚于取消；失败发生在 agentctl journal admission。
- **证据摘要**：`/media/zuo/AigoData/kandev-home/logs/backend-logs.log` 中 2026-11-03T22:47:52.953 / 52.976 / 53.017 / 53.711 行。

### 未知 2：为什么 agentctl 拒收 prompt？
- **验证方式**：读 `AdmitDeliverySubmission` / `recordDeliveryStorageFailure` / `applyDeliveryCapacityLocked` / `DeliveryCapability`，并结合 22:39:41 日志。
- **结论（试通证据）**：22:39:41 一次 `persistDeliveryEvent` 失败触发 `deliveryBlocked=true` + `storage_error`；该状态在进程内不会被 `applyDeliveryCapacityLocked` 清除（`delivery_pressure.go:227-252`）；`AdmitDeliverySubmission` 因此返回 `errDeliveryPressure`（`manager.go:676-694`）。能力广告不含该状态（`manager.go:539-557`）。
- **证据摘要**：`agentctl/server/process/delivery_pressure.go:108-114,227-252`；`agentctl/server/process/manager.go:539-557,2507-2515`。

### 未知 3：journal 是否真的没有该 submission？
- **验证方式**：复制保留 journal 到临时目录后按 bbolt JSON 记录解析（未改动原文件），并查询 SQL。
- **结论（试通证据）**：会话 062a1281 的 journal 只有 3 条 completed submission，`prompt:send-now:eb53cfb8-e9e7-4bd7-8fec-85ca79413a71` 出现 0 次；SQL cursor 与 journal stream 一致。
- **证据摘要**：`agentctl-journals/062a1281-4012-41f0-a6bc-f503aa4999b4/delivery.bbolt`；SQL 查询 `agent_delivery_submissions` / `session_recovery_blocks` / `agent_delivery_cursors`。

### 未知 4：为什么 retry / resume 都无法恢复？
- **验证方式**：读 `RetrySessionDelivery`、`selectReconstructionEvidence`、`validateNativeResumeSQLRows`、`AcknowledgeNativeResumeDelivery`、`nativeResumeRetirementIDs`；并复验 23:05 日志。
- **结论（试通证据）**：retry 需要恰好 1 个 journal 候选，本例为 0 → `retained_evidence_ambiguous`；adoption 要求 SQL 活跃 submission 全部存在于 journal → "native resume peer and SQL submission evidence disagree"；新 prompt 被 open block 拒绝。
- **证据摘要**：`orchestrator/session_delivery_reconstruction.go:139-165`；`lifecycle/native_resume_evidence.go:35-50`；`lifecycle/native_resume_delivery.go:94-127`；日志 23:05:36.387。

### 未知 5：第二个会话是否同根因？
- **验证方式**：对比 SQL / journal / block / 任务归档状态与恢复代码。
- **结论（试通证据）**：同族不同根。会话 5a9f6ea5 的 journal 有该 submission 但为 `dispatching`；任务已归档，普通恢复被设计性拒绝；即便不归档，`dispatching` 会被 native resume retirement 与 `GetNativeResumeDeliveryEvidence` 的 `Unresolved` 检查双重拒绝，只有新 agentctl 进程的 `ClassifyAbandonedDispatches` 能改状态。
- **证据摘要**：session 5a9f6ea5 的 SQL/journal 记录；`lifecycle/native_resume_delivery.go:94-127`；`agentctl/journal/abandoned_dispatch.go:11-40`；`agentctl/server/process/manager.go:459-464`。

## Impact

- **后端代码**：
  - `apps/backend/internal/orchestrator/`：`session_delivery_reconstruction.go`、`session_delivery_recovery.go`、`agent_delivery_submission.go`、`task_operations.go`（pre-intake 结算分支与写前拒收快速路径）。
  - `apps/backend/internal/agent/runtime/lifecycle/`：`delivery_record_evidence.go`、`delivery_record_transport.go`（submission absence 证据）。
  - `apps/backend/internal/agentctl/server/process/`：`manager.go`、`delivery_pressure.go`、`delivery_batch.go`（health 可恢复、pump 重启、capability 反映 blocked）。
  - `apps/backend/internal/agentctl/server/api/agent.go` + `apps/backend/internal/agent/runtime/agentctl/agent.go`、`client_delivery.go`（typed admission 拒收）。
  - `apps/backend/internal/task/repository/interface.go` 与 `sqlite/agent_delivery_settlement.go`（新增结算事务与 effect 类型）；`internal/task/models/` 若新增 typed request/effect 常量。
- **数据**：预计不新增表或列；新增 `agent_delivery_effects` 的 effect type 常量（复用现有文本列）。若 plan 阶段确需新列，按仓库规则走 additive migration + SQLite fresh/replay + PostgreSQL 行为测试。
- **接口**：`session.recover` 复用既有 `settled` 结果；agentctl prompt WS 错误新增向后兼容的 `kind`/`reason` 字段。
- **规格/文档**：`docs/specs/platform/requirements/durable-agent-delivery.md`、`docs/specs/platform/system-design/durable-agent-delivery.md`、`docs/specs/platform/system-design/durable-agent-record-recovery.md` 需在 Apply 阶段同步；视需要新增 ADR。若新增 metric，同步根 `AGENTS.md` 的 observability 条目。
- **前端**：预计无新增 UI；复用既有 retry 动作与 `task:deliveryRecoverySettled` 文案。若暴露新的 blocked reason，需同步 7 个 locale。
- **测试**：后端 `*_test.go` 至少覆盖 dispatch-failed-before-intake 结算成功、adoption 容忍 settled submission、send-now 拒收后不留下可 diverge 的非终态 submission；agentctl 覆盖 health 恢复与 typed 拒收。
- **明确不做**：第二个会话的归档任务恢复、automatic 背景 sweep、放宽 `dispatching` journal 的 retirement、UI 改版。
