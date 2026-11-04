# agent-stream-signal-resilience Specification

## Purpose
Define durable capture, reconnect replay, and completion-pending visibility for `step_complete_kandev` signals, plus transport health and control-plane isolation for the backend↔agentctl agent stream.
## Requirements
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
| agentctl keepalive pump 在 stream 结束后继续运行 | ping pump 使用 runtime-bound context，读循环只取消 dial context | 为每条 stream 建立独立生命周期，结束时取消 pump 并关闭 socket |
| 非幂等 MCP 工具在背压下返回无类型超时 | 发送路径只对 step_complete 返回 typed error | 发布前失败统一返回 `RequestDeliveryError`（稳定 code + Retryable） |

