# provider-stall-aftermath

## 背景

当 provider 在 prompt dispatch 阶段挂起时，agent 进程仍然存活，但 300 秒内没有任何 turn 事件。lifecycle watchdog 把这种 prompt 分类为 never-started，并发布 `agent.stalled`。当前 orchestrator 的 never-started 分支把它当作 launch failure：写一条 `Agent produced no output since start.` 错误消息，把 session 和 task 置为 FAILED，然后 force-stop execution。用户既看不到「消息未送达」的明确说明，也无法自动恢复；线上两次事故都要求人工 resume，其中一次在 resume 后由用户重新输入原消息才继续。

本 capability 定义 never-started stall 的 aftermath：session 进入可恢复状态而不是 FAILED；用户收到明确通知；系统在零事件结算成功后自动恢复一次；已经有 turn 事件的 stall 保持现有 advisory 行为不变。

## 方案与决策

- **never-started 是可恢复的 turn failure，不是 launch failure**：session 进入 `WAITING_FOR_INPUT`，turn 被结算，task 不进入 FAILED。错误信息和持久化通知说明 provider 在 300 秒内无事件、消息未送达、可以 resume 或重新发送。
- **只在零事件结算成功时改变终态**：如果零事件结算因证据缺失而 blocked，保留现有 `interrupted_unknown` + open block 的 fail-closed 路径；不把不确定结果伪装成可恢复状态。
- **只自动 resume，不自动重投**：恢复动作复用现有 `tryEnsureExecution` / `ensureSessionRunning`，只重启 agent 进程，不重放历史 prompt。每个 stall episode 至多一次；watchdog 每个 prompt generation 只发布一次 stall 事件，因此「一次」有明确边界。
- **复用 provider turn failure 的结算 pipeline**：turn 完成/放弃、managed input 在「无 observed effects」时结算为 failed、session 进入 WAITING_FOR_INPUT，与 `settleRetainedTurnFailure` 的既有语义保持一致。
- **作用域保持窄**：只适用于 durable-delivery 的 interactive、非 passthrough、非 Office session；Office/automation 与 passthrough 保持现有行为，避免改写它们的调度语义。
- **partial-output 不变**：只要 dispatch 后出现过真实 turn 事件，就继续走 advisory notice + Cancel turn，不改状态、不结算、不自动恢复。

## ADDED Requirements

### Requirement: Never-started stalls SHALL settle the session recoverable, not FAILED

When the lifecycle watchdog classifies a prompt generation as never-started and the zero-event settlement succeeds, Kandev SHALL move the session to `WAITING_FOR_INPUT`, complete or abandon the captured turn, settle any managed input as failed when no effects were observed, and MUST NOT transition the session or its task to `FAILED`.

#### Scenario: 零事件 stall 后 session 可恢复
- **WHEN** a never-started stall's delivery submission is settled `failed`
- **THEN** the session enters `WAITING_FOR_INPUT`, its captured turn is completed or abandoned, the task does not transition to `FAILED`, and the conversation remains available for a later prompt

#### Scenario: 结算 blocked 时保持 fail-closed
- **WHEN** the zero-event settlement cannot prove its evidence
- **THEN** the session does not claim a recoverable outcome; the existing `interrupted_unknown` submission and open `unknown_prompt_outcome` block remain authoritative

#### Scenario: 恢复动作可用
- **WHEN** the session is `WAITING_FOR_INPUT` after a never-started stall
- **THEN** the composer accepts a new prompt and the existing resume control is available

### Requirement: The stall reason MUST be visible to the user

Kandev SHALL persist a user-visible notice that states the provider produced no events for the watchdog window, that the message was not delivered, and that the user can resume or re-send. The session's error message SHALL carry the same stall reason instead of only `agent produced no output since start`.

#### Scenario: 通知说明消息未送达
- **WHEN** a never-started stall is handled
- **THEN** exactly one persisted notice is created for that prompt generation, containing the no-event duration and the explicit statement that the message was not delivered

#### Scenario: 错误信息包含 stall 原因
- **WHEN** the session is read after the stall
- **THEN** its error message names the provider stall and the watchdog kill, not only the generic no-output phrase

### Requirement: Automatic recovery MUST be bounded and MUST NOT replay the prompt

After a successful zero-event settlement, Kandev SHALL attempt to resume the session at most once per stall episode. The automatic resume MUST use the existing resume/auto-start path, MUST NOT dispatch the historical prompt, MUST honor archived, deferred-launch, auto-start-disabled, Office/automation, and passthrough guards, and MUST NOT retry automatically when it fails.

#### Scenario: 自动 resume 只执行一次
- **WHEN** the session is settled after a never-started stall and auto-resume eligibility allows it
- **THEN** Kandev launches the agent once without re-sending the prompt, and no second automatic launch is attempted for the same stall episode

#### Scenario: 自动 resume 失败不循环
- **WHEN** the automatic resume fails or the agent cannot start
- **THEN** the session remains `WAITING_FOR_INPUT`, the notice remains visible, and no further automatic attempt is made

#### Scenario: auto-start 被禁用
- **WHEN** the session's workflow, task, or user setting does not allow automatic agent start
- **THEN** no automatic resume occurs; the session remains recoverable for a manual action

### Requirement: Partial-output stalls MUST remain advisory and unchanged

A stall that observed at least one turn event since dispatch MUST keep the existing advisory notice and Cancel turn behavior. It MUST NOT trigger settlement, session state change, or automatic resume.

#### Scenario: 已有输出后挂起
- **WHEN** the watchdog detects inactivity after at least one turn event
- **THEN** the existing neutral notice and Cancel turn action are persisted, and the session, task, submission, and execution remain unchanged

#### Scenario: 只收到 metadata frame
- **WHEN** the agent emitted only metadata frames and no turn event since dispatch
- **THEN** the prompt remains classified as never-started, not as partial output

### Requirement: A late uncertain-delivery signal MUST NOT override the settled outcome

The never-started settlement outcome SHALL be authoritative for its exact prompt generation. A later `ErrUncertainPromptDelivery` signal for that generation MUST NOT recreate an open recovery block for the settled submission, MUST NOT revert the session to `RUNNING`, and MUST NOT re-open the delivery.

#### Scenario: 结算后迟到的 prompt 错误
- **WHEN** the force-stopped prompt's error handler runs after the settlement completed
- **THEN** it observes the settled submission and the recovered session, creates no `unknown_prompt_outcome` block, and leaves `WAITING_FOR_INPUT` unchanged

#### Scenario: 迟到事件属于后续 generation
- **WHEN** a late signal belongs to a successor execution or prompt generation
- **THEN** it is ignored for the settled generation and does not alter the successor's state

### Requirement: Scope MUST preserve Office, automation, and passthrough ownership

The recoverable never-started outcome MUST apply only to interactive, non-passthrough, non-Office durable-delivery sessions. Office/automation-owned sessions and passthrough sessions MUST keep their existing stall handling and recovery ownership.

#### Scenario: Office 或 automation session
- **WHEN** a never-started stall belongs to an Office or automation-owned task
- **THEN** the existing stall handling remains in effect and the new automatic resume does not run

#### Scenario: passthrough session
- **WHEN** the stalled session is passthrough
- **THEN** the durable-delivery settlement and automatic resume do not run

### Requirement: The new outcomes MUST be observable without unbounded labels

Kandev SHALL expose bounded metrics for never-started settlement outcomes and automatic resume attempts, using closed label sets and no task, session, submission, execution, or payload identifiers.

#### Scenario: 指标反映结算结果
- **WHEN** a never-started settlement succeeds, is blocked, or preserves the partial-output path
- **THEN** a bounded counter records the outcome with a closed reason label

#### Scenario: 指标反映自动恢复
- **WHEN** an automatic resume is attempted, succeeds, or fails
- **THEN** a bounded counter records the attempt and outcome without session or execution identifiers

## Known Failure Modes

| Symptom | Cause | Fix |
|---------|-------|-----|
| 结算后 block 又出现 | prompt goroutine 的迟到 uncertain 信号仍走旧路径 | 以 prompt generation 为界，已结算 generation 禁止重建 block |
| session 被翻回 RUNNING | `handlePromptError` 回退到 previousSessionState | 结算 outcome 对该 generation 权威，迟到错误不得覆盖 |
| 自动恢复循环 | 每次 resume 都触发新的 stall 事件 | 按 stall episode 限制一次；resume 本身不重放 prompt |
| auto-start 被绕过 | 自动 resume 没有复用既有守卫 | 复用 `tryEnsureExecution` / `ensureSessionRunning` 的全部边界 |
| 部分输出被当作零事件 | watchdog 的 turn-event 判别被 metadata frame 污染 | 只有真实 turn 事件才清除 never-started 分类 |
| Office session 被自动恢复 | stall handler 未区分 task origin | 在入口排除 Office/automation 与 passthrough |
| 用户不知道消息丢了 | 只把 session 置 FAILED，通知仍是通用 no-output 文案 | 持久化「未送达」通知并写入 stall 原因 |
