# durable-delivery-admission-health Specification

## Purpose
TBD - created by archiving change settle-pre-intake-delivery-failure. Update Purpose after archive.
## Requirements
### Requirement: Delivery capability MUST reflect admission health
While the durable journal cannot accept new submissions, agentctl MUST NOT advertise usable durable delivery in its capability or recovery descriptor, and the backend MUST refuse the prompt before creating a durable SQL submission.

#### Scenario: storage error 期间不广告可用
- **WHEN** agentctl 的 delivery health 为 `storage_error` 或 admission 被阻塞
- **THEN** capability/status 返回不可接纳原因，backend 不创建 submission，用户得到明确的 blocked 结果

#### Scenario: 恢复后重新接纳
- **WHEN** 容量恢复或一次 journal 提交成功，blocked 状态被清除
- **THEN** capability 重新广告 durable_v1，下一次 prompt 正常 admission

### Requirement: Admission blocked state MUST be recoverable
A transient persistence failure MUST NOT permanently block prompt admission or stop durable event forwarding for the lifetime of the process; the manager SHALL restart or re-arm the durable writer/update pump after a transient failure, while genuine storage exhaustion MAY keep admission blocked until capacity recovers.

#### Scenario: 瞬态 persist 失败后恢复
- **WHEN** 一次 durable event commit 因瞬态原因失败，随后 journal 可写
- **THEN** event pump 恢复转发，admission 在健康检查通过后恢复

#### Scenario: 真实存储耗尽保持 fail-closed
- **WHEN** 磁盘或 quota 真正耗尽
- **THEN** 新 admission 保持拒绝，Stop/status/replay/ACK 仍可用

### Requirement: Admission refusal MUST be typed and observable
Agentctl SHALL return a structured refusal reason that distinguishes pre-write rejection, post-write transition failure, unresolved durable work, and storage pressure, and the backend MUST preserve the reason in diagnostics while using bounded metric labels.

#### Scenario: 写前拒收可区分
- **WHEN** admission 在写入 journal 之前失败
- **THEN** 返回 typed `durable_delivery_admission_rejected`（含 reason），backend 记录该原因并可走快速结算

#### Scenario: 写后失败保持不确定
- **WHEN** submission 已写入 journal 但状态转换失败
- **THEN** 返回不确定/冲突结果，backend 不把该 submission 当作未投递

## Known Failure Modes

| Symptom | Cause | Fix |
|---------|-------|-----|
| capability 显示 durable_v1 但 prompt 被拒 | capability 未反映 deliveryBlocked | 健康状态纳入 capability/status |
| 一次写失败后 agent 再无输出 | forwardUpdates 在 persist 失败后返回 | 重启/重新武装 writer 与 pump |
| 拒收原因不可诊断 | 只返回固定 message | typed kind/reason |

