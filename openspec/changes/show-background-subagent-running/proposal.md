# show-background-subagent-running

## Why

聊天 transcript 中的后台（异步）子代理卡片在父 agent 的 turn 结束后立即渲染为「已完成」勾号，而后台子代理实际仍在带外运行（实测单卡可连续工作 15-40 分钟）。用户无法从卡片得知后台子代理仍在运行、任务仍在推进。根因在前端：卡片状态解析把 tool status `complete`（对 Task 工具本身而言 dispatch 已终态，后端按规格如此设计）误当作子代理完成，且活跃性判定被「所在 turn 是否活跃」门控。后端语义（terminality、schema、usage 缺失）均由既有规格钉死，本 change 仅修复前端渲染。

## What Changes

- 仅改 `apps/web`：子代理卡片的状态解析引入独立的「后台运行中」显示态，不再把 tool status `complete` 映射为完成勾，也不再受所在 turn 活跃性门控。
- From: payload `async_launched`（或 `is_async` 且无终态 agent 结果）的卡片在 turn 结束后渲染完成勾 + 小号 "background" chip，session 显示 WAITING_FOR_INPUT，看起来已结束。
- To: 该卡片渲染活跃指示（spinner）+ 显式「后台运行中」标签，无论所在 turn 是否活跃；turn 结束后折叠为紧凑单行卡片，meta 行（含 background chip）照常显示。
- Reason: tool status `complete` 只证明 dispatch 终态，不证明子代理结束；`agent_status`（`async_launched`）是前端唯一可见的子代理级真相。
- Impact: non-breaking 的显示语义修正；前台（同步）子代理行为完全不变。

### 设计决策（Propose 阶段定案，Plan 沿用）

1. **判活谓词**：后台运行态 = payload `status === "async_launched"`，或 `is_async === true` 且 payload 无终态 agent 结果。两者都不依赖 `isContainingTurnActive`。`is_async` 的生产方已核实为两处，均出现在「tool 调用终态、子代理转后台」的帧上：Claude async envelope（`subagent.go:276`）、Cursor `isBackground`（`subagent.go:359`），谓词对两者语义一致。
2. **状态与布局解耦**：活跃指示 + 标签由后台运行态驱动（不受 turn 门控）；自动展开与 meta 行维持现有 turn 域语义。理由：11 张 fan-out 后台卡若永久自动展开会占满 transcript；折叠为带活跃指示的紧凑行同样传达「仍在运行」，且 meta 行的 background chip 保持可见。
3. **标签一致性**：后台运行态期间（turn 活跃或已结束）统一显示同一个显式标签（替代通用 "Working"），避免同一状态两种文案。
4. **终态优先**：payload 出现终态 agent 结果（`completed`/`failed`/`cancelled` 等）时按现行规则结算（勾/叉 + background chip），后台运行态让位。
5. **无完成信号是已确认的边界**：卡片将停留在后台运行态（见 Research Log 未知 1）；spec 记录该限制，禁止用粗粒度计数归零伪造完成。

## Capabilities

### New Capabilities

- `subagent-card-status`: 聊天 transcript 中子代理工具卡片的状态解析与渲染契约：依据 payload（`subagent_task`）区分后台运行态与终态，后台运行态不受所在 turn 活跃性门控，且不伪造完成与用量。

### Modified Capabilities

（无：openspec/specs 现有 6 个 capability 均不覆盖聊天子代理卡片；本 change 新建该 capability。）

## Research Log

### 未知 1：是否存在可将单张异步卡片结算为完成的前端可见信号
- **验证方式**：读后端事件链（adapter → orchestrator → WS）与前端消费点，逐候选排查（Explore 阶段完成，本步沿用）。
- **结论（blocker——正确信号不存在，采用「停留后台运行态」路径）**：Claude 的后台完成通知（task-notification → `EventTypeBackgroundComplete`）不带 ToolCallID/执行身份，不写 transcript 消息，卡片 metadata 永不更新；orchestrator 只能按 FIFO 退役最旧注册，代码注释明言无法归因到具体 workload。WS 粗粒度信号（`active_subagent_count`/`foreground_activity`）只在看板/线程/session 图标层消费，不进卡片，且 `foreground_activity:"background"` 受默认关闭的实验 flag 门控。`task_session_background_work` 投影为提案功能，所有 shipped profile 默认关闭。
- **证据摘要**：`adapter_updates.go:705-718`（ID-less 事件）；`turn_activity.go:552-565`（FIFO + 无法归因注释）；`config_test.go:230-231`（实验 flag 默认关）；`runtimeflags/registry.go:232`（agentBackgroundWork 默认关）。用「计数归零」做翻转会在重复通知时提前归零，等价伪造完成，违反 AC-7 精神，已排除。

### 未知 2：`is_async` 的生产方与语义（判活边界）
- **验证方式**：读 `subagent.go` 的 payload 构造路径（本步补查）。
- **结论（试通证据）**：`is_async` 仅两处赋值，均为「tool 调用终态、子代理转后台」的帧：Claude `_meta.claudeCode.toolResponse` 的 `isAsync:true`（伴随 `status:"async_launched"`）、Cursor `rawOutput.isBackground`。谓词「`async_launched` 或 `is_async` 且无终态 agent 结果」对两家语义一致；Cursor 后台卡今日渲染为完成勾 + chip，与本 change 的修正方向一致（dispatch 终态 ≠ 子代理结束）。
- **证据摘要**：`apps/backend/internal/agentctl/server/adapter/transport/acp/subagent.go:276,359,408-410`。

### 未知 3：修复的布局副作用（自动展开 / meta 行 / chip）
- **验证方式**：读 `tool-subagent-message.tsx` 的展示推导（Explore 阶段完成，本步沿用）。
- **结论（试通证据）**：现状 `autoExpanded = isActive`、meta 行仅在 `!isActive` 渲染；若直接把后台运行并入 `isActive`，11 张 fan-out 卡将永久展开且 background chip 不可见。故采用设计决策 2 的解耦方案。
- **证据摘要**：`apps/web/components/task/chat/messages/tool-subagent-message.tsx:386,417`；`subagent-meta.ts:56-58`（现有 `task:background` chip）。

## Impact

- **代码**（均在 `apps/web`）：`components/task/chat/messages/tool-subagent-message.tsx`（状态解析：新增后台运行态判定，`resolveSubagentTerminalStatus`/`SubagentStatusIcon` 不得在 payload `async_launched` 时把 tool status `complete` 渲染为完成勾）；如需可测试性，从组件提取纯谓词 helper（如 `isSubagentBackgroundRunning`）到独立模块；`components/task/chat/types.ts` 如需补充注释。Office chat panel、native message list、simple agent-turn-panel 均只透传 `isContainingTurnActive` prop，helper 层修复一处全部受益。
- **i18n**：新增 1 个 key（`task:statusBackgroundRunning`，en 值 "Running in background"，无 em dash）；补齐 pt-pt、zh-cn、zh-hk、zh-tw、ja、ko，繁体对用 `pnpm run i18n:zh-hant`。校验 `pnpm run i18n:check`、`pnpm run i18n:ratchet`。
- **测试**：为状态解析 helper 新增/更新 `*.test.ts`（重点补「async + turn 已结束」组合；现有 `tool-subagent-message.test.tsx:328` 覆盖 active-turn 内 async、`:297` 覆盖 sync 失活，均需按新语义核对）；同步（前台）子代理场景保持通过。
- **校验命令**：`cd apps/web && pnpm run typecheck`；`cd apps && pnpm --filter @kandev/web lint`；相关 vitest 文件。
- **明确不做**（后端按既有规格钉死）：不改 terminality 语义（`tool_status` 唯一裁决，`agent_status` 禁止参与——`subagent-context-persistence-03.md` Terminality 节）；不改 `task_session_subagents` schema；不改 adapter 对 async dispatch 强制 `complete`（`adapter_tools.go:692-704`）；不读 Claude outputFile、不补 usage 数值（`-08.md` Out of scope + AC-7）；不为持久化表加 UI/DTO/WS 面；不用 `active_subagent_count` 归零伪造完成。
- **规格引用**：`docs/specs/agents/system-design/subagent-context-persistence-03.md`、`-08.md`、`docs/specs/agents/system-design/background-work.md`（其中 `features.agentBackgroundWork` 投影上线后可作为未来完成信号来源，本 change 不依赖）。
