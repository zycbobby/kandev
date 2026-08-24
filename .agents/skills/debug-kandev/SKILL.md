---
name: debug-kandev
description: >
  Debug the local Kandev runtime and its data. Loads the debug environment map —
  the SQLite data directory (KANDEV_HOME_DIR/data/kandev.db, default ~/.kandev), backend logs, task workspaces,
  and Claude Code session-transcript locations — and teaches how to trace workflow
  step transitions, task/session state, and the auto-advance / step_complete_kandev
  signal mechanism (ADR 0015). Use whenever the user reports unexpected Kandev
  behavior, asks why a task or workflow step advanced (or did something unexpected),
  wants to inspect Kandev runtime data (workflows, workflow_steps, tasks, task_sessions,
  task_step_transitions, session_step_history, task_plans), or needs to find a past
  session's transcript. Trigger even when the user doesn't say "Kandev" explicitly but
  describes runtime state, workflow steps (Explore/Propose/Plan/Apply/Review/Verify/Archive),
  or asks to "查一下数据 / 为什么提前推进 / 找那个会话 / debug 一下".
---

# Debug Kandev

排查本地 Kandev 运行时行为时先读本文件。核心事实：**Kandev 的所有运行时状态都在
「数据根目录」下的 SQLite 里，而不是仓库源码里**——工作流、任务、会话、step 推进历史、
agent/executor 记录全部落在这个 DB。仓库里的 `apps/backend/config/workflows/*.yml` 只是
内置模板，用户自建的工作流（如 `dev-openspec-workflow`）只存在于 DB。

> **先解析数据根目录，别硬套 `~/.kandev`。** 默认是 `~/.kandev`，但可被 `KANDEV_HOME_DIR`
> 覆盖（systemd 服务 unit 里 `Environment=KANDEV_HOME_DIR=...`）。本机开发环境已迁到
> `/media/zuo/AigoData/kandev-home`，此时 `~/.kandev` 是**过期备份**（其 `data/kandev.db`
> 是旧快照，绝不能当实时数据查）。拿真实路径：
>
> ```bash
> KH=$(systemctl --user show kandev.service -p Environment 2>/dev/null \
>   | tr ' ' '\n' | sed -n 's/^KANDEV_HOME_DIR=//p')
> KH=${KH:-~/.kandev}
> echo "$KH"   # 下面所有 $KH 都指这个值
> ```

## 数据地图

`$KH` = 上面解析出的数据根目录（默认 `~/.kandev`）。

```
$KH/
├── data/kandev.db              # SQLite，运行时唯一真相源（下面所有查询都打这里）
├── logs/backend-logs.log       # 后端日志，tail 看报错/路由
├── .kandev-backend.lock        # 后端进程锁
├── supervisor/                 # control.sock + launch.json（supervisor 控制）
├── sessions/                   # （通常为空，会话态在 DB 的 task_sessions）
├── tasks/<task-slug>/          # 任务工作区（git worktree/copy，含 openspec/ 等产物）
├── quick-chat/                 # quick-chat 工作区
├── passthrough-mcp/            # 每个会话的 passthrough MCP 配置
└── tools/, tmp/
```

Claude Code 会话转录（跟上面 DB 是两套，互不重叠）：

```
~/.claude/projects/<project-slug>/<session-id>.jsonl        # 会话全文（aiTitle 字段=自动标题）
~/.claude/projects/<slug>/<session-id>/subagents/agent-*.jsonl  # 子代理转录
~/.claude/session-index/<session-id>.json                   # {session_id, cwd, started_at}
~/.claude/history.jsonl                                     # 用户 prompt 历史（display/project/sessionId/timestamp）
```

- `project-slug` = cwd 把 `/` 换成 `-`（如 `/media/zuo/.../unimatrix-mono` →
  `-media-zuo-AigoData-GameCode-unimatrix-mono`）。嵌套仓库（`unimatrix-mono/doc/unimatrix-doc`）
  的会话也归到 `unimatrix-mono` 这个 slug 下。
- 用会话标题找会话：`grep -rho '"aiTitle":"[^"]*"' ~/.claude/projects/`。
- Codingspace 远端会话的转录**不在本地** `~/.claude`，只能走 codingspace 平台/MCP 拿。

## 排查配方（先跑这些）

查询统一用 `sqlite3 $KH/data/kandev.db`。注意 DB 可能被后端写锁，读查询一般没事；
如果 `sqlite3` 不存在就 `python3 -c "import sqlite3; ..."`。

### 1. 看一个工作流及其步骤（含 events 和 signal-gating）

```sql
-- 所有工作流
SELECT id, name, created_at, updated_at FROM workflows;

-- 某个工作流的步骤：position / name / is_start_step / auto_advance_requires_signal / events
SELECT position, name, is_start_step, auto_advance_requires_signal, events
FROM workflow_steps WHERE workflow_id='<id>' ORDER BY position;
```

`events` 是 JSON：`on_enter`（常含 `auto_start_agent`）、`on_turn_complete`（常含
`move_to_step` / `move_to_next`）、`on_turn_start`、`on_exit`。

### 2. 查一个 task 的 step 推进历史（"为什么提前推进" 的答案在这）

```sql
-- 先按标题找到 task
SELECT id, title, workflow_id, workflow_step_id, state, created_at, updated_at
FROM tasks WHERE title LIKE '%关键词%' ORDER BY created_at DESC;

-- 再拉它的 step 推进审计：trigger / actor_kind / from→to / 时间戳
SELECT id,
       substr(from_workflow_step_id,1,8) AS from_step,
       substr(to_workflow_step_id,1,8)   AS to_step,
       trigger, actor_kind, occurred_at
FROM task_step_transitions WHERE task_id='<task_id>' ORDER BY id;
```

`trigger` 常见值：`task_created`、`engine_transition`（= 自动推进，含 on_turn_complete 和
signal 驱动）、`manual_move`。`actor_kind` = `agent`/`human`。相邻两行的 `occurred_at` 间隔
就是每步耗时——如果只有几秒，几乎可以断定是「turn 结束即推进」而非真正完成。

更细的 per-session 记录在 `session_step_history`（带 `metadata`，ADR 0015 会在其中写
`signal_source`）。

### 3. 确认某步骤是否 signal-gated

```sql
SELECT name, auto_advance_requires_signal
FROM workflow_steps WHERE workflow_id='<id>' ORDER BY position;
```

`0` = legacy（任何一次 turn 结束都触发 `on_turn_complete` 推进）；`1` = 必须等 agent 显式调
`step_complete_kandev`（或 UI 手动兜底）才推进。

### 4. 看后端日志

```bash
tail -n 200 $KH/logs/backend-logs.log
grep -n "error\|warn\|routing\|step_completion" $KH/logs/backend-logs.log | tail -50
```

### 5. 找一个会话的转录

```bash
# 会话索引（session_id → cwd + 启动时间）
cat ~/.claude/session-index/<session-id>.json

# 会话全文；aiTitle 是自动生成的标题
grep -o '"aiTitle":"[^"]*"' ~/.claude/projects/<slug>/<session-id>.jsonl | head -1
```

## 工作流 step 推进机制（排障核心）

Kandev 工作流是状态机，每个 step 有 `events` 定义「进入/回合结束/退出」时做什么：

- `on_enter: auto_start_agent` → 进入该 step 时**自动把该 step 的 prompt 派给 agent**（这就是
  "向 agent 发送了 plan 指令" 的来源）。
- `on_turn_complete: move_to_step` / `move_to_next` → **agent 一停流（turn 结束）就自动推进
  到下一步**。这是"提前推进"的默认根因：turn 结束 ≠ step 真正做完。

ADR 0015（`docs/decisions/0015-explicit-completion-signal-for-auto-advance.md`）引入
`auto_advance_requires_signal` 开关解决这个问题：

- 默认 `false` = legacy：任何 turn 结束都推进（agent 问个问题、撞限流、到预算都会触发推进）。
- `true` = 只在 agent 显式调用 `step_complete_kandev` MCP 工具后才推进；裸 halt 只显示
  "completion pending" 提示，不推进。

所以「为什么 X 步骤还没结束就发了 Y 指令」的排查顺序：

1. 查该 step 的 `events` 是否有 `on_turn_complete: move_to_step`。
2. 查该 step 的 `auto_advance_requires_signal` 是不是 `0`（= 没 gate）。
3. 查 `task_step_transitions` 的时间戳间隔，确认是几秒级快速推进。
4. 若 `auto_advance_requires_signal=1` 但仍提前推进，则是 agent 过早调了 `step_complete_kandev`
   （signal 是 fire-and-forget，调完继续产 token 会跟异步 transition 竞争，见 ADR 0015 的
   "Late-signal re-close race"）。

信号相关代码：`apps/backend/internal/workflow/`（engine、models、handlers）、
`apps/backend/internal/orchestrator/event_handlers_workflow.go`、`internal/mcp/handlers`。
字段定义在 `apps/backend/internal/workflow/models/models.go`（`AutoAdvanceRequiresSignal`）。

**修复 signal-gating 要做两件事，只翻 flag 不够：**

1. 把该 step 的 `auto_advance_requires_signal` 设为 `1`（推进闸门）。
2. 同步改该 step 的 `prompt`：把「完成后直接结束本轮，工作流会自动流转到 X」这类 legacy
   措辞，改成显式「完成后调用 `step_complete_kandev`（必填 summary，概括本步骤产出）发出完成
   信号」——否则 agent 照旧 prompt 只停流不调信号，步骤会卡在 completion-pending。

后端 `internal/sysprompt/sysprompt.go` 在 `auto_advance_requires_signal=1` 时也会往系统提示里注入
信号指令，但 step prompt 自带指令更可靠，也能消除与 legacy 措辞的矛盾（ADR 0015 承认系统提示
prepend 在不同 client/CLI 上不可靠）。

改法用 REST API，不要直接写 SQLite（后端会原子更新 + 发事件，且无缓存不一致）：
`PUT http://127.0.0.1:38429/api/v1/workflow/steps/<step_id>`，body `{"prompt":"..."}`
或 `{"auto_advance_requires_signal":true}`。REST 无鉴权（本机）。

## 参考

- `references/db-schema.md` —— 关键表的字段速查（workflows / workflow_steps / tasks /
  task_sessions / task_step_transitions / session_step_history / task_plans / agents / executors）。
- ADR 0015 全文：`docs/decisions/0015-explicit-completion-signal-for-auto-advance.md`（仓库内）。
