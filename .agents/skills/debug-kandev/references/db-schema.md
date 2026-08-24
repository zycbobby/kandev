# Kandev SQLite schema — key tables for debugging

DB 路径：`$KH/data/kandev.db`（`$KH` = 数据根目录，解析方式见 SKILL.md；默认 `~/.kandev`）。
下面只列排障常用的表与关键字段；全表清单用
`sqlite3 $KH/data/kandev.db ".tables"` 看（有 200+ 张表，多数是 github/gitlab/jira/
linear/office 等集成表，与工作流排障无关）。

## 工作流

### `workflows`
| 列 | 说明 |
|---|---|
| `id` | workflow UUID |
| `name` | 显示名（如 `dev-openspec-workflow`） |
| `created_at` / `updated_at` | 创建/最后修改时间。**updated_at 变化 = 有人改过 workflow**（可用来判断 signal-gating 是否在事故后才打开） |

### `workflow_steps`
| 列 | 说明 |
|---|---|
| `id` | step UUID |
| `workflow_id` | 所属 workflow |
| `name` | step 名（Explore/Propose/Plan/Apply/…） |
| `position` | 顺序，从 0 开始 |
| `prompt` | 进入该 step 时派给 agent 的 prompt |
| `events` | JSON：`on_enter` / `on_turn_complete` / `on_turn_start` / `on_exit` |
| `is_start_step` | 起始 step（1/0） |
| `allow_manual_move` | 是否允许手动拖动 |
| `auto_advance_requires_signal` | **ADR 0015 的 signal-gating 开关**（0=legacy 任何 turn 结束都推进；1=等 `step_complete_kandev`） |
| `stage_type` | UX 语义提示（work/review/approval/custom），engine 不据此分支 |
| `wip_limit` / `pull_from_step_id` | 看板 WIP 相关 |

## 任务与会话

### `tasks`
| 列 | 说明 |
|---|---|
| `id` / `title` | task UUID / 标题（**"Add 新词挖掘 article to unimatrix-doc" 这类标题就在这**） |
| `workspace_id` / `parent_id` | 工作区 / 父 task（子任务） |
| `workflow_id` / `workflow_step_id` | 所属 workflow / 当前 step |
| `state` / `position` | 状态 / 在看板中的位置 |
| `created_at` / `updated_at` | 创建/更新 |

### `task_sessions`
一次 task 的会话态；`$KH/sessions/` 目录基本不用，会话在 DB 里。

### `task_step_transitions` —— **step 推进审计（核心排障表）**
| 列 | 说明 |
|---|---|
| `task_id` / `session_id` | 所属 task / session |
| `from_workflow_step_id` / `to_workflow_step_id` | 从哪个 step 移到哪个 step |
| `trigger` | `task_created` / `engine_transition`（自动推进）/ `manual_move`（人/agent 拖动） |
| `actor_kind` / `actor_id` | 触发者：`human` / `agent`（+ id） |
| `occurred_at` | 时间戳。**相邻行时间差 = 每步耗时**，几秒级就是"提前推进"铁证 |

### `session_step_history`
per-session 的 step 历史，比 `task_step_transitions` 多一个 `metadata`（ADR 0015 会在
signal 驱动推进时写 `signal_source` = `agent`/`manual_fallback` 和 `signal_summary`）。

### 会话消息/回合/子代理
- `task_session_messages` —— 会话消息
- `task_session_turns` —— 会话回合
- `task_session_subagents` —— 子代理记录
- `task_session_commits` / `task_session_git_snapshots` —— 会话的 git 快照/提交

## 计划与评审

- `task_plans` / `task_plan_revisions` —— 任务计划（`create_task_plan_kandev` / `get_task_plan_kandev` 存取）
- `task_review_runs` / `task_review_findings` —— 评审运行与发现
- `task_blockers` / `task_comments` / `task_documents` —— 阻塞/评论/文档
- `workflow_step_decisions` / `workflow_step_participants` —— step 决策与参与者

## Agent / Executor

- `agents` —— agent 记录
- `agent_profiles` / `dynamic_agent_profiles` —— agent 配置
- `executors` / `executors_running` —— executor 及其运行态
- `agent_wakeup_requests` / `agent_continuation_summaries` —— 唤醒请求 / 续写摘要
- `utility_agents` / `utility_agent_calls` —— 工具 agent 调用

## 其它常用

- `settings` / `kandev_meta` —— 设置 / meta
- `runtime_flag_overrides` —— 运行时 feature flag 覆盖（对应 CLAUDE.md 的 Feature Toggles）
- `users` / `workspaces` / `repositories` / `environments` —— 用户/工作区/仓库/环境
- `pending_moves` —— 挂起的 move（`move_task_kandev` 延迟路径）
- `queue_session_state` / `queue_session_locks` / `queued_messages` —— 队列相关

## 常用 SQL 模板

```sql
-- 某 task 的完整 step 推进轨迹
SELECT occurred_at, trigger, actor_kind,
       substr(from_workflow_step_id,1,8) f, substr(to_workflow_step_id,1,8) t
FROM task_step_transitions WHERE task_id='<id>' ORDER BY id;

-- 某 workflow 的 step 名（把短 id 映射回名字）
SELECT substr(id,1,8), name, auto_advance_requires_signal
FROM workflow_steps WHERE workflow_id='<id>' ORDER BY position;

-- 最近创建/修改的 task
SELECT substr(id,1,8), title, state, created_at FROM tasks ORDER BY created_at DESC LIMIT 20;

-- 哪个 workflow 最近被改过（判断 signal-gating 是否事后才开）
SELECT name, created_at, updated_at FROM workflows ORDER BY updated_at DESC;
```
