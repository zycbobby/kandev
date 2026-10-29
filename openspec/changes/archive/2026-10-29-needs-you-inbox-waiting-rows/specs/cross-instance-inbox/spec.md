# cross-instance-inbox

## 背景

landed 的 remote-questions change 已把远端实例的 answerable clarification bundle 组合进本地 Inbox：按实例直连远端、每实例一页、就地回答、sidecar 写回远端、按实例降级、远端行不落本地表。本 change 在同一组合机制上增加第二种远端行：远端实例处于手工工作流步骤、且没有执行中或排队 turn 的任务。它让「远端任务在等我」和「远端任务在问我」一样可见，同时不重开 question 行、sidecar 转发或 workspace 映射的既有契约。

## 方案与决策

- **复用 landed 组合，不新建 fan-out 机制**：本地 Inbox 仍按实例读取本地后端的 `GET /api/v1/instances/:id/needs-you-inbox`（+`/hidden`），本地后端再以该实例的注册凭据读远端自己的聚合 needs-you 端点。
- **等待行不带答案框**：远端等待行只提供打开任务、dismiss、snooze；sidecar 状态仍在远端，经既有 sidecar 转发写入。
- **版本协商靠降级而非探测**：远端没有聚合 needs-you 读（旧版本）时按 `inbox_disabled` 类降级态处理，不把整个本地读判失败。
- **本地 workspace 切换不影响远端页**：沿用既有契约，远端等待行属于实例页，不随本机 workspace 过滤。
- **数据隔离不变**：远端等待行只存在于每次读取的组合结果，不写本地 task 表、board cache 或 sidecar 表。

## MODIFIED Requirements

### Requirement: 角标计数包含远端行

The sidebar actionable count SHALL include loaded remote instance pages alongside the local page, equal to the number of rows the composed view lists from loaded sources across row kinds (clarification and waiting), boot hydration SHALL continue to seed only the local page, and a remote instance contributes to the count only once its page has landed.

#### Scenario: 远端行计入角标

- **WHEN** a remote instance page lands holding clarification rows or waiting rows
- **THEN** the sidebar count increases by that page's listed rows and equals the rows the composed view lists from loaded sources

#### Scenario: 未加载或降级实例不计入

- **WHEN** a remote instance's page has not landed or that instance is degraded
- **THEN** it contributes no rows and no count, and the count never reports rows the view does not list

## ADDED Requirements

### Requirement: 远端等待行组合

The local backend SHALL, for each enabled registered instance while both `features.needsYouInbox` and `features.multiInstance` are on, read that remote instance's own aggregated needs-you endpoint over the instance's registered credential and attach the remote's manual-step waiting rows to that instance's page through the same per-instance composition the remote clarification rows use. Remote waiting rows SHALL carry instance and origin-workspace identity together with the task and step, SHALL resolve by re-reading when the remote no longer reports the wait, SHALL NOT be written into any local task table, board cache, or sidecar table, and an instance that is unreachable, credential-required, or runs a version without the waiting read SHALL render that instance's degraded state without preventing local rows or other instances from listing.

#### Scenario: 看本机时远端等待行出现在 Inbox

- **WHEN** the panel is viewing the local instance and a remote instance holds a task in a manual step with no executing or queued turn in any workspace its credential identity can see
- **THEN** the local Inbox lists that wait in that instance's section with the task and step, and the row carries the instance and origin-workspace identity

#### Scenario: 远端不再报告时行消失

- **WHEN** the remote task leaves the manual step, starts a turn, completes, or archives
- **THEN** the next re-read of that instance page no longer lists the row, without navigation or local derivation

#### Scenario: 旧版本远端降级

- **WHEN** a remote instance runs a version without the aggregated needs-you read
- **THEN** that instance's section renders the degraded state naming the instance and offering a retry, and no dead row is listed

#### Scenario: 降级实例不影响本地读

- **WHEN** one remote instance is unreachable or requires credentials
- **THEN** local rows and other instances' sections still list, and the local read is not treated as failed

#### Scenario: 远端等待行不落本地表

- **WHEN** a remote waiting row is listed, dismissed, or snoozed
- **THEN** no local task table, board cache, or local sidecar row is written for it; its sidecar state stays on the remote under the credential identity

## Known Failure Modes

| Symptom | Cause | Fix |
|---------|-------|-----|
| 远端等待行出现死答案框 | 复用了 question 行的展开/回答组件 | 等待行只渲染打开任务与 dismiss/snooze 菜单，不渲染答案组件 |
| 旧版本远端把整个 Inbox 拖成错误态 | 远端 404 被当成 transport failure | 每实例降级：404 映射为 unsupported/inbox_disabled，本地与其他实例照常列出 |
| 远端行被本机 workspace 过滤掉 | 等待读误用了本机 workspace id | 远端读沿用 landed 的 workspace 映射，本机 workspace 切换不影响实例页 |
| 远端等待行落进本地 sidecar | dismiss/snooze 走了本地 sidecar 入口 | 远端行一律经 instance 转发；本地 sidecar 只接受本机 `mw:` 行 |
| 远端等待行重复或漏行 | 远端聚合页的 limit 与合并排序未与本地一致 | 复用同一 limit 与 (reference time, id) 排序契约；每实例页一次读 |
