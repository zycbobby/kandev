# cross-instance-inbox

## 背景

远端实例上阻塞等待回答的 clarification bundle 只在远端面板可答：本机侧边栏的中继通知不可回答（occurrence 只携带 title/body），本机 Needs-you Inbox 只列本机 workspace 的 bundle。操作者不看远端面板就会漏掉被阻塞的远端 agent。本 capability 让本机 Inbox 组合远端 answerable 状态，建立在 `multi-instance-management`（注册表、PAT、后端联邦客户端）与 `needs-you-inbox`（本机 Inbox 契约）之上；跨实例通知的中继与展示仍归 `cross-instance-notifications`，本 capability 不改任何通知投递行为。

## 方案与决策

- **本机后端 fan-out**：本机后端按实例直连远端（PAT 注入，`relay.fetch` 先例），读远端自身 clarification-inbox 端点并聚合为每实例一页；拒绝 SPA 侧经 proxy base 组合（capability cookie 仅 select 时 mint、DTO 刻意省略 proxy port）。
- **实例整体聚合**：远端页 = PAT 身份可见的全部远端 workspace 的 answerable 聚合（「远端默认 workspace」服务端不可构造；配置式目标会静默隐藏被阻塞 agent）。行携带实例与来源 workspace 身份；本机活动 workspace 切换不改变远端页。
- **转发而非重实现**：回答/dismiss/snooze/restore 转发到远端自身端点，win-loss（200/409）与 sidecar 语义原样保留；sidecar 状态留在远端，本地 sidecar 表不扩列。
- **数据隔离**：远端行不写任何本地投影表；Inbox 仍是计算视图（含远端页）。
- **收敛 = re-read 即契约**：远端行移除与计数变化只由远端页响应落地。

## ADDED Requirements

### Requirement: 远端 answerable 行组合
The local backend SHALL, for each enabled registered instance while both `features.needsYouInbox` and `features.multiInstance` are on, read that remote instance's own clarification-inbox endpoint over the instance's registered credential, aggregate the PAT-visible remote workspaces into one instance page (ordered by bundle creation time ascending, bounded by the same default page size as the local read), and the local Inbox SHALL render one answerable section per instance with rows carrying instance and origin-workspace identity, without writing remote rows into any local task table, board cache, or sidecar table.

#### Scenario: 看本机时远端问题出现在 Inbox
- **WHEN** the panel is viewing the local instance and a remote instance holds an answerable clarification bundle in any workspace its credential identity can see
- **THEN** the local Inbox lists that bundle in that instance's section, answerable in place

#### Scenario: 远端行不落本地表
- **WHEN** a remote answerable row is listed or answered
- **THEN** no local task table, board cache, or clarification sidecar row is written for it, and every cross-instance identifier the Inbox holds carries instance identity

#### Scenario: 本机 workspace 切换不动远端页
- **WHEN** the operator switches the active local workspace
- **THEN** local rows are replaced per the existing workspace-scoped contract and each remote instance's section is unchanged

#### Scenario: 任一 flag 关闭完全惰性
- **WHEN** either feature flag is off
- **THEN** no remote read is issued and the Inbox, badge, and notification behavior are identical to before this change

### Requirement: 就地回答转发
Answering or rejecting a remote row from the local Inbox SHALL be forwarded through the local backend to the remote instance's own clarification respond endpoint, preserving the existing resolution semantics (first-write-wins, idempotent replay, task resume, and the 200 win/loss and 409 not-active outcomes) unchanged, and the shared clarification answer component chain SHALL accept an additive optional submission-target override so the task session and Quick Chat hosts keep identical behavior.

#### Scenario: 就地回答远端行并恢复远端任务
- **WHEN** the operator submits an answer for a remote row from the local Inbox
- **THEN** the resolution is recorded on the remote instance, the owning remote task resumes, and the row is removed from the local view by the instance page re-read without navigation

#### Scenario: 远端侧抢先回答
- **WHEN** a remote row the local Inbox lists is answered on the remote instance first
- **THEN** the next re-read of that instance page removes the row and reduces the count, without a failed submission

#### Scenario: 转发失败不丢行
- **WHEN** a forwarded submission fails for any reason other than losing the race or the bundle no longer being active
- **THEN** the row and its count are unchanged, the entered answer is preserved, and a retry is offered; the late-answer fallback path is never offered for a remote row

### Requirement: 远端 sidecar 转发
Dismiss, snooze, and restore on a remote row SHALL be forwarded through the local backend to the remote instance's own clarification-inbox sidecar endpoints, so the sidecar state stays on the remote under the credential identity, the local sidecar schema is not widened, and a pending identifier is always addressed through its owning instance so identical identifiers on different instances never collide.

#### Scenario: 本机 snooze 远端行落在远端
- **WHEN** the operator snoozes a remote row from the local Inbox
- **THEN** the snooze is recorded on the remote instance's sidecar under the credential identity and the row is excluded by that remote's next page read, with no local sidecar row written

#### Scenario: 跨实例 pending_id 碰撞不混淆
- **WHEN** two instances report bundles with the same pending identifier
- **THEN** addressing either row resolves and mutates only that row on its owning instance

### Requirement: 角标计数包含远端行
The sidebar actionable count SHALL include loaded remote instance pages alongside the local page, equal to the number of rows the composed view lists from loaded sources, boot hydration SHALL continue to seed only the local page, and a remote instance contributes to the count only once its page has landed.

#### Scenario: 远端行计入角标
- **WHEN** a remote instance page lands holding answerable rows
- **THEN** the sidebar count increases by that page's listed rows and equals the rows the composed view lists from loaded sources

#### Scenario: 未加载或降级实例不计入
- **WHEN** a remote instance's page has not landed or that instance is degraded
- **THEN** it contributes no rows and no count, and the count never reports rows the view does not list

### Requirement: 远端降级态
An unreachable, credential-required, or unsupported remote instance SHALL render a degraded state for that instance's section naming the instance and offering a retry, SHALL NOT render an answer form that cannot succeed, and SHALL NOT affect the local page, other instances' sections, or their counts.

#### Scenario: 远端停止时降级
- **WHEN** a proxied or fanned-out remote read cannot reach the remote instance
- **THEN** that instance's section shows a degraded state with a retry instead of rows or an error page, and other sections are unaffected

#### Scenario: auth-on 远端提示可修复
- **WHEN** a remote instance requires credentials and none are registered
- **THEN** the degraded state says so, and registering a PAT for the instance restores the section on the next read

### Requirement: 远端行跳转
The local Inbox SHALL provide, on every remote row, a control that switches the panel to the row's instance via the standard instance-switch path and opens the referenced task's route.

#### Scenario: 点击远端行跳转
- **WHEN** the operator opens a remote row's task
- **THEN** the panel switches to that instance via the standard switch path and lands on the task's route

## Known Failure Modes

| Symptom | Cause | Fix |
|---|---|---|
| 看本机时收不到远端 answerable 行 | fan-out 挂在了「当前活动实例」上 | 组合读生命周期归属注册表而非面板选择；测试断言面板看本机时远端 section 仍出现 |
| 远端行移除靠本地 splice | 复制本机 optimistic 移除路径 | 行移除与计数只由远端页响应落地；转发失败路径必须保留行 |
| 远端行答案提交到本机 resolver | 共享组件 base 覆盖未贯穿或 host 未传 | 覆盖为 additive 可选参数；e2e 断言远端任务的恢复发生在远端实例 |
| 跨实例 pending_id 串号 | 转发路由只按 pending_id 寻址 | 一切远端 sidecar/respond 寻址必须先解析所属实例；碰撞用例必须双实例断言 |
| 降级实例拖垮整个 Inbox | fan-out 串行且无实例级隔离 | 每实例独立读、独立超时、独立降级态；单实例故障不阻塞本机页与其他实例页 |
| 角标与本机 AC .40 producer 集冲突 | 把远端计数做成第三个独立 producer | 远端计数只随其源页响应产生（页与数同源）；boot 仍只 seed 本机页 |
| 远端行出现死答案框 | 降级态下仍渲染可提交表单 | 降级 section 只渲染命名实例的重试态，不含答案组件 |
| auth-on 远端被当作不可达且无提示 | 健康探测不带凭据的已知限制 | 降级态文案区分「需凭据」路径并指向 PAT 登记 |
