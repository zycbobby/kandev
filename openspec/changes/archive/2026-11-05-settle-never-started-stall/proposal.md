## Why

Provider 上游偶发在 prompt dispatch 阶段挂起：agent 进程存活，但 300 秒内没有产生任何 turn 事件。当前 watchdog 把这种 never-started stall 当作 launch failure 处理：强制杀掉 turn、把 session 和 task 置为 FAILED，同时把 delivery submission 留在 `interrupted_unknown`。用户看不到「消息未送达」的明确提示，也无法自动恢复；两次线上事故里同一条消息被静默丢失，20:34 发出的 prompt 直到 20:43 人工 resume 后由用户重发才送达。根因不在上游（Kandev 无法修复 provider 挂起），而在 aftermath：我们已经拥有「零 turn 事件 + 无 terminal evidence + 执行进程已停止」的强证据，却没有一条把「确定未处理」结算为 `failed`、把 session 恢复成可用的路径。native resume 只会 retire journal 记录并保留 SQL 不确定；resume 完成后 retry 也失去入口。需要把 never-started stall 的 aftermath 变成一次可审计的结算、一个可恢复的 session 状态，以及一次有界的自动恢复。

## What Changes

**never-started stall 的 session 终态**
- From: watchdog 判定 `never_started` 后，session/task 直接 FAILED，错误只有 `agent produced no output since start`，没有恢复动作。
- To: session 进入 `WAITING_FOR_INPUT`（保留可恢复性），turn 被结算，task 不进入 FAILED；session 错误信息和持久化通知说明「provider 在 300 秒内无事件，消息未送达，可 resume 或重新发送」。
- Reason: fail-closed 不等于 opaque-and-terminal；零事件是可证明「没有输出、没有副作用」的证据，应该换来可恢复状态而不是死路。
- Impact: 行为变更；影响 never-started 分支，不影响 partial-output 的 advisory stall。

**delivery submission 的零事件结算**
- From: force-kill 后 prompt goroutine 把 submission 记为 `interrupted_unknown` / `prompt_dispatch_failed` 并创建 `unknown_prompt_outcome` block；native resume 只 retire journal 记录，SQL submission 永远不确定，且 resume 后没有 retry 入口。
- To: 当零事件证据、journal 无 terminal evidence、历史 owner 已证明终止、身份与哈希精确匹配时，在同一条可审计路径里 retire 对应 journal 记录，并把 SQL submission 结算为 `failed`（新 effect type，`terminal_sequence=0`，`fully_settled_at` 保持 NULL），仅解析绑定该 submission 的 block；任一证据缺失或冲突时不做任何 journal/SQL 写入，保持现有 uncertain/block 路径。
- Reason: 只改 SQL 不 retire journal 会让后续 native resume 因跨存储状态不一致而 blocked；只 retire journal 不结算 SQL 则留下永久不确定行。两者必须在同一恢复语义下收敛。
- Impact: 新增结算事务与 effect type；不改变 pre-intake settlement、abandoned settlement 的既有语义。

**有界自动恢复**
- From: watchdog 强制停止后没有任何自动动作，必须等用户手动 resume；两次事故分别等待了 20 分钟和 4 分钟。
- To: 结算完成后，按现有 auto-resume / auto-start 守卫自动 resume 一次（只重启 agent，不重放历史 prompt），每个 stall episode 至多一次；自动恢复失败时保持 `WAITING_FOR_INPUT` 和通知，不循环重试。
- Reason: resume 只重启进程、不会重复投递消息，天然避免重复处理；watchdog 每个 prompt generation 只发布一次 stall 事件，所以「一次」有明确边界。
- Impact: 复用既有 `tryEnsureExecution` / `ensureSessionRunning` 路径；archived、deferred launch、auto-start 禁用、Office/automation、passthrough 的边界保持不变。

**partial-output stall 保持不变**
- From / To: 已有 turn 事件的 stall 继续走 advisory notice + Cancel turn，不结算、不改 session 状态、不自动恢复。
- Reason: 部分输出意味着可能已经产生效果，必须保持 uncertain / fail-closed。

**明确不做**
- 不新增 agentctl 侧 first-event dispatch timeout（可选二级项，本 change 不做；300 秒 watchdog 已提供上界）。
- 不重放历史 prompt，不把结算当成重发入口。
- 不改变 advisory stall 的 5 分钟阈值、通知样式或 Cancel turn 行为。

## Capabilities

### New Capabilities

- `durable-delivery-never-started-settlement`: 定义 journal-present、零 turn 事件、无 terminal evidence、owner 已证明终止时的保守结算证据、journal retirement、SQL 事务、幂等与边界。
- `provider-stall-aftermath`: 定义 never-started stall 的可恢复 session 终态、用户通知、有界自动 resume，以及与 partial-output advisory 路径的边界。

### Modified Capabilities

（无。`openspec/specs/` 下没有同名的既有 capability；durable 产品契约在 `docs/specs/agents/` 与 `docs/specs/platform/`，将在 Apply 阶段同步更新，见 Impact。）

## Research Log

### 未知 1：never-started stall 当前会发生什么
- **验证方式**：读 lifecycle watchdog、orchestrator stall handler、prompt 失败路径，并用线上事故日志对照时间线。
- **结论（试通证据）**：watchdog 在零 turn 事件达到 300 秒时发布一次 `agent.stalled`（`never_started=true`）；`handleAgentStalled` 写错误消息、把 session/task 置 FAILED，再 force-stop execution；prompt goroutine 随后返回 `ErrUncertainPromptDelivery`，把 submission 记为 `interrupted_unknown` / `prompt_dispatch_failed` 并创建 `unknown_prompt_outcome` block。
- **证据摘要**：`internal/agent/runtime/lifecycle/session.go:1509-1540`；`internal/orchestrator/event_handlers_stall.go:44-190`；`internal/orchestrator/task_operations.go:1247-1290,7578-7600`；日志 18:36:25.793 / 18:36:26.750 / 20:39:28.206 / 20:39:29.140。

### 未知 2：事故会话的持久化状态是否支持「零事件、未处理」
- **验证方式**：只读查询 `kandev.db` 的 `task_sessions` / `agent_delivery_submissions` / `session_recovery_blocks`，并复制保留 journal 到临时目录后离线解析（未改动原文件）。
- **结论（试通证据）**：`prompt:b5d60bea` 与 `prompt:0e9ea5c7` 均为 `interrupted_unknown` / `prompt_dispatch_failed`、`terminal_sequence=0`、`fully_settled_at=NULL`，没有结算 effect；block 在每次 stall 期间 open，被人工 resume 解析；journal 中两条记录最终为 `completed` + `retired=true`，但 `terminal_event_retained=false`、`terminal_sequence=0`，即零输出。报告中的「没有 open block」只在 resume 之后成立；真正的 durable 缺陷是从未结算的 submission。
- **证据摘要**：session `6b9dd01c-352f-4b6a-97dc-2be0d4ad5642`；submission `prompt:b5d60bea` / `prompt:0e9ea5c7`；block `a4415995` created 10:36:26Z / resolved 12:43:34Z；journal `/media/zuo/AigoData/kandev-home/agentctl-journals/6b9dd01c-.../delivery.bbolt`。

### 未知 3：能否只结算 SQL，或只 retire journal
- **验证方式**：读 native resume 跨存储状态矩阵、retained retirement 契约与 `ClassifyAbandonedDispatches`。
- **结论（拒绝理由）**：SQL terminal + journal 非终态（`dispatching` / `interrupted_unknown`，无 terminal evidence）会被 native resume 判为 blocked；现有 retirement 契约只接受 `interrupted_unknown` target，而 stall 时 journal 记录是 `dispatching`。因此结算必须同时 retire journal，并为 `dispatching` 增加显式的零事件授权（或等价的两阶段 classify-then-retire），不能只改一侧。
- **证据摘要**：`internal/agent/runtime/lifecycle/native_resume_state_matrix_test.go:17-30`；`internal/agentctl/journal/retained_retirement.go:150-200`；`internal/agent/runtime/lifecycle/delivery_record_retirement.go:96-110`；`internal/agentctl/journal/abandoned_dispatch.go`。

### 未知 4：零事件是否足以安全结算，以及现有可恢复 pipeline
- **验证方式**：读 `PromptAttemptEvidence`、provider turn failure 的 managed-input disposition，以及 auto-resume 入口。
- **结论（试通证据）**：`PromptAttemptEvidence` 把任何真实 turn 事件同时视为 output 与 effect 证据；零事件即 evidence-known / no-output / no-effect。既有 `settleRetainedTurnFailure` 已用 `execution_failed_without_observed_effects` 把 managed input 结算为 failed，并把 session 置 WAITING_FOR_INPUT。auto-resume 入口 `tryEnsureExecution` / `autoResumeEligibility` 已存在，并受 auto-start、deferred launch、archived 等守卫约束。
- **证据摘要**：`internal/agent/runtime/lifecycle/types.go:796-810`；`internal/orchestrator/event_handlers_turn_failure.go:137-200`；`internal/orchestrator/managed_input_lifecycle.go:193-220`；`internal/orchestrator/session_ensure.go:384-425`；`internal/orchestrator/task_operations.go:5094-5160`。

### 未知 5：partial-output stall 与 Office/passthrough 的边界
- **验证方式**：读 advisory stall handler、`settleRetainedTurnFailure` 的 Office/automation 分支、事故 task 的 origin。
- **结论（试通证据）**：partial-output stall 只写 advisory notice + Cancel turn，不改状态；事故 task 为 `origin=manual`、非 passthrough。Office/automation 与 passthrough 在现有 turn-failure 路径有独立分支，本 change 不应默认为它们引入新的可恢复语义。
- **证据摘要**：`internal/orchestrator/event_handlers_stall.go:93-118`；`internal/orchestrator/event_handlers_turn_failure.go:100-127`；task `16c86be4-...` 的 `origin=manual`，session `is_passthrough=0`。

## Impact

- **后端代码**：
  - `internal/agent/runtime/lifecycle/`：`AgentStalledPayload` 补充 exact delivery identity（submission / stream / incarnation / generation）与可用的 historical runtime identity；stall 路径保持 activity-epoch 守卫。
  - `internal/orchestrator/event_handlers_stall.go`：never-started 分支改为「通知 + 结算 + WAITING_FOR_INPUT + 有界 auto-resume」，保留 force teardown。
  - `internal/orchestrator/task_operations.go`、`pre_intake_delivery_settlement.go`：新增 sibling settlement；抑制同一 prompt generation 的迟到 uncertain 信号重建 block 或把状态翻回 RUNNING。
  - `internal/task/repository/{interface,sqlite}`：新增结算事务与 effect type（建议 `agent_delivery.never_started_settlement:<submissionID>`），复用 pre-intake 的锁、recovery revision 与 block resolution；SQLite + PostgreSQL 行为测试。
  - `internal/agentctl/journal/`、`internal/agentctl/server/api/`、`internal/agent/runtime/lifecycle/delivery_record_retirement.go`：retained retirement 增加显式零事件授权，允许 `dispatching` 等无 terminal evidence 的记录被 retire；保留「dispatch label alone is not proof」的默认拒绝。
- **数据**：无新表或新列；新增 effect type 文本常量；journal 记录只 retire、不删除、不伪造 terminal event/sequence。
- **接口**：agentctl retained retirement 请求新增向后兼容字段；无新增用户可见 HTTP API。
- **观测**：新增有界 metric（settlement outcome、auto-resume attempted/succeeded/failed），label 不使用 task/session/submission 标识；同步根 `CLAUDE.md` 的 observability 条目。
- **文档 / 规格**：`docs/specs/agents/requirements/agent-stall-recovery.md`（AC .7/.10/.11 与新增 AC）、`docs/specs/agents/system-design/agent-stall-recovery.md`；新增或更新 ADR 取代 `2026-08-18-never-started-agent-stall-terminal.md` 的 FAILED 结论；`docs/specs/platform/requirements/durable-agent-delivery.md` 与 `docs/specs/platform/system-design/durable-agent-delivery-settlement.md`；`docs/specs/platform/system-design/interrupted-prompt-recovery.md` 的 stall 特例。
- **前端**：无新增 UI 概念；复用持久化 session message 与现有 resume/错误展示。若新增用户可见文案，按 7 个 locale 同步。
- **测试**：never-started stall → submission settled + session WAITING_FOR_INPUT + 通知（不是 FAILED）；partial-output stall → 不结算、状态不变；证据缺失/截断/身份不匹配 → 保持 blocked；journal retire 后 SQL 失败可收敛；迟到 uncertain 信号不重建 block；有界 auto-resume 只触发一次且失败不循环；native resume 不被已结算 submission 阻塞。
- **明确不做**：不处理已有两条 stuck 行的自动 backfill（可后续用同一契约手动 retry）；不改变 partial-output 的 advisory 行为；不新增 background sweep；不改变 Office/automation/passthrough 的现有恢复边界；不引入 agentctl first-event timeout；不重放历史 prompt。
