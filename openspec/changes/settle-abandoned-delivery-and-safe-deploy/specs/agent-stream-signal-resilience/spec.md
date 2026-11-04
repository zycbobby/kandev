# agent-stream-signal-resilience

## 背景

backend↔agentctl 的 `/api/v1/agent/stream` WebSocket 同时承载 agent 输出事件和 MCP 工具请求/响应。`step_complete_kandev` 是 agent 在 workflow step 结束时调用的 MCP 工具；它的请求从 agentctl 的 MCP server 经 `ChannelBackendClient` 交给 agent-stream writer，再由 backend 的 `handleStepComplete` 写入 session metadata。workflow 依赖这个信号推进 on_turn_complete 转换。

2026-11-04 12:15:18 / 12:17:30 的负载窗口里，agent-stream 两次断线重连。`step_complete_kandev` 在 5s 发送窗口内没有进入 stream，backend 从未收到；workflow 停在 completion-pending，直到用户新开一轮才恢复。同窗口并行任务也丢了 3 次 MCP 工具调用。现场同时出现 NTFS SQLite 写争用（启动时 `writer_wait_count=2281`、`writer_ping deadline_exceeded`）。

## 问题

- `ChannelBackendClient.RequestPayload` 使用无缓冲 `requestCh` 和 5s 发送超时；writer 忙于写事件或 stream 断开时，请求无法入流，MCP 工具调用直接失败。
- agent-stream writer 与 agent-event 共用同一个 select 循环；没有 ping/pong、没有写 deadline。慢消费者或半开连接会让 writer 阻塞，进而让 MCP 请求无法被消费。
- 客户端 event queue 有界；handler 被 SQLite writer 争用阻塞时 queue 会满，`readUpdatesStream` 直接关闭 stream（`errAgentEventQueueFull`），触发断线重连。
- 信号一旦在发送前失败，backend 没有任何记录；`stuck_signal_watchdog` 只能处理已经持久化的 signal，无法发现「agent 已尝试但信号从未到达」。
- workflow 因此无声停在 completion-pending，agent 的聊天保底/重发虽然可用，但系统没有把「信号投递失败」变成可见状态。

## 方案与决策

- **传输诊断与加固**：agent-stream 增加 ping/pong 与显式读写 deadline；断线必须带 typed reason（`event_queue_full`、`write_timeout`、`ping_timeout`、`read_error`、`owner_changed` 等）；记录 queue depth、write duration、MCP request send latency 等有界 metric。
- **控制面与事件面隔离**：MCP 控制请求不能因为 agent-event backlog 而失败。step_complete 信号在发送前持久化到 agentctl 本地 journal（bbolt bucket 或等价的有界持久队列），断线/背压时在重连后按 request ID 幂等重放；其它非幂等 MCP 工具只做有界重试和 typed error，不做 durable replay。
- **信号持久捕获**：capture 记录 request ID、action、payload、session、created_at、delivered_at；只在 backend 确认 accepted 或 typed terminal error 后标记完成；capture store 满/不可用时向 agent 返回 typed failure，不静默丢弃。
- **completion-pending 可见**：信号在 bounded window 内仍未送达时，agentctl 向 backend 发出 typed `signal_delivery_failed` notice（重连后通过 durable event 或 MCP action 送达）；backend 用既有 session recovery/error surface 显示 completion-pending 和可用恢复动作（重发/聊天保底/用户重试），不新增前端概念。
- **agent 自救路径保留**：agent 可以继续通过 chat fallback 或重新调用 `step_complete_kandev` 恢复；backend 的 `handleDuplicateStepComplete` / pending-signal dedup 保证只推进一次。
- **SQLite writer 争用**：signal capture 不走 backend SQLite writer；agentctl 本地 journal 独立于 backend DB。backend 侧 `handleStepComplete` 的写延迟通过既有 writer metrics 观测，不在本 change 重做 NTFS/SQLite 性能。

## ADDED Requirements

### Requirement: Step-complete signal delivery SHALL survive transient agent-stream disconnects

A `step_complete_kandev` signal MUST be durably captured before it is published to the agent stream, and MUST be replayed after reconnect so the workflow can advance without a new user turn.

#### Scenario: 断流期间的信号
- **WHEN** agent 在 agent-stream 断开或 writer 无法接收请求时调用 `step_complete_kandev`
- **THEN** 信号先写入 agentctl 本地 capture store；连接恢复后按原 request ID 重放；backend 收到并推进 workflow，无需用户新开一轮

#### Scenario: agentctl 重启后的信号
- **WHEN** 信号已 capture，但 agentctl 在送达前退出并重启
- **THEN** 新的 stream 建立后仍能重放该 signal；capture 记录在确认送达前不会丢失

#### Scenario: 重复或重放的信号
- **WHEN** 同一 signal 因重连、重试或 agent 重新调用而被送达多次
- **THEN** backend 的 dedup 只应用一次 step transition；重复响应返回 `already_signaled` 或等价语义

### Requirement: MCP control requests MUST NOT be starved by agent-event backpressure

The agent stream MUST keep MCP control requests deliverable while agent events are backlogged, and MUST return a typed retryable result instead of a silent send timeout.

#### Scenario: event queue 饱和
- **WHEN** agent-event queue 达到上限或 handler 被慢速 SQLite 写入阻塞
- **THEN** `step_complete_kandev` 请求仍被 capture 并在连接恢复后送达；其它 MCP 工具得到 typed retryable error，而不是 5s 静默超时

#### Scenario: writer 被慢消费者阻塞
- **WHEN** agent-stream 的写操作超过 write deadline 或对端不读取
- **THEN** 连接以 typed `write_timeout`/backpressure reason 关闭并进入重连；待发送的 capture 记录保留

### Requirement: Agent-stream connections MUST detect half-open and slow peers

Agent-stream WebSocket connections MUST use ping/pong and explicit read/write deadlines, and every disconnect MUST carry a bounded typed reason.

#### Scenario: 半开连接
- **WHEN** 对端不再响应 pong 或读循环超过 read deadline
- **THEN** 连接被关闭，disconnect reason 记录为 `ping_timeout` 或 `read_timeout`，重连流程启动

#### Scenario: 传输健康可观测
- **WHEN** 发生断线、重连、请求失败或 queue 饱和
- **THEN** 有界 metric/log 记录 reason、queue depth、send latency 与重试次数；label 不包含 session/submission/payload 身份

### Requirement: Undelivered signals MUST surface completion-pending

When a captured signal cannot be delivered within a bounded window, the system MUST surface a visible completion-pending state with a recovery action instead of silently waiting for a new user turn.

#### Scenario: 超过投递窗口
- **WHEN** capture 存在且超过 bounded delivery window 仍未收到 backend accepted
- **THEN** agentctl 向 backend 发送 typed `signal_delivery_failed` notice；backend 通过既有 session recovery/error surface 显示 completion-pending，并保留重发/聊天保底/用户重试动作

#### Scenario: 通知后恢复
- **WHEN** 用户在 notice 后触发重发，或 agent 通过 chat fallback 重新调用 `step_complete_kandev`
- **THEN** workflow 只推进一次；notice 被清除；重复调用不产生第二次 transition

### Requirement: Signal durability MUST NOT depend on the contended backend SQLite writer

Signal capture and retry MUST use agentctl-local durable storage, and MUST fail closed with a typed error when that storage is full or unavailable.

#### Scenario: SQLite writer 争用
- **WHEN** backend 出现 `writer_wait_count` 升高或 `writer_ping deadline_exceeded`
- **THEN** signal capture 仍能在 agentctl 本地完成；backend 处理延迟只影响送达时间，不导致信号丢失

#### Scenario: capture store 不可用
- **WHEN** agentctl 本地 capture store 满、损坏或不可写
- **THEN** `step_complete_kandev` 返回 typed failure，agent 可使用聊天保底/重发；系统不静默丢弃信号

## Known Failure Modes

| Symptom | Cause | Fix |
|---------|-------|-----|
| `timeout sending request to agent stream` | 无缓冲 requestCh + 5s send timeout，writer 被事件或断线阻塞 | 控制面与事件面隔离 + signal capture/replay |
| workflow 停在 completion-pending，直到新用户轮次 | 信号从未到达 backend，watchdog 无持久记录可看 | capture + typed `signal_delivery_failed` notice |
| 并行 MCP 工具调用同时失败 | 同一 WS 的 event backlog 拖住 writer | 背压隔离 + 有界重试 |
| 断线后长时间不重连 | 没有 ping/pong，半开连接只能等 TCP 超时 | ping/pong + read/write deadline |
| SQLite writer 争用放大断线 | handler 阻塞导致 event queue 满、stream 关闭 | capture 隔离 + writer 观测；性能优化另行处理 |
