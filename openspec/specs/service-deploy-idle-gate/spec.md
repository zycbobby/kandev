# service-deploy-idle-gate Specification

## Purpose
Define the pre-publish idle check, active-work reporting, explicit override, and systemd stop budget for user-service deploys.
## Requirements
### Requirement: Deploy SHALL refuse to restart while active work exists

The deploy script MUST check the live Kandev database for active work before publishing a new runtime bundle or restarting the service, and MUST refuse the deploy when active work is present.

#### Scenario: 存在 RUNNING/STARTING session
- **WHEN** live 数据库中存在 `task_sessions.state IN ('STARTING', 'RUNNING')` 且 task 未归档
- **THEN** 脚本在 `publish_bundle` 之前退出非零，列出受影响的 task ID、title 和 `session` 原因，并提示等待、先 `service stop` 或使用 `--force`

#### Scenario: 存在 in-flight submission 或 open recovery block
- **WHEN** live 数据库中存在 `prepared`/`accepted`/`dispatching`/`interrupted_unknown` submission，或 `state='open'` 的 recovery block，且关联 task 未归档
- **THEN** 脚本拒绝部署，并列出绑定 task 与原因

#### Scenario: 服务空闲
- **WHEN** 以上三类活跃工作均不存在
- **THEN** 脚本继续执行 publish、install 与 restart，行为与改动前一致

#### Scenario: 服务未运行且没有数据库
- **WHEN** live home 下不存在数据库文件，且对应的 user service 未运行
- **THEN** 脚本视为全新安装并继续部署

### Requirement: Idle detection MUST be read-only, bounded and conservative

The gate MUST read the database without modifying it, MUST run before the runtime bundle is replaced, and MUST fail closed when the database exists but cannot be inspected.

#### Scenario: 数据库不可读
- **WHEN** 数据库文件存在，但只读连接失败、schema 不兼容或查询出错
- **THEN** 脚本拒绝部署并提示使用 `--force` 显式覆盖，不静默当作 idle

#### Scenario: 服务运行但数据库不可定位
- **WHEN** user service 处于 active 状态，但 unit/plist 的 `KANDEV_DATABASE_PATH` 与默认 `<home>/data/kandev.db` 都不存在或不可读
- **THEN** 脚本拒绝部署并提示使用 `--force`，因为无法证明服务当前 idle

#### Scenario: 已归档 task
- **WHEN** 活跃行只关联到 `archived_at IS NOT NULL` 的 task
- **THEN** 该行不计入活跃工作

#### Scenario: 拒绝发生在 publish 之前
- **WHEN** gate 判定 busy
- **THEN** live `runtime/bin` 未被替换，`service install` 与 `service restart` 均未执行

### Requirement: `--force` SHALL explicitly override the gate

The deploy script MUST accept an explicit `--force` flag that skips only the idle gate and records the override in its output.

#### Scenario: 操作者显式覆盖
- **WHEN** 脚本以 `--force` 调用且存在活跃工作
- **THEN** 脚本继续部署，并在输出中说明 idle gate 被显式跳过；其他校验（bundle 完整性、home 隔离）仍然执行

### Requirement: Generated systemd units MUST allow the graceful shutdown budget

Generated Linux user and system units MUST set a stop timeout large enough for the backend's HTTP, agent, and tracing shutdown budgets.

#### Scenario: 生成的 unit 停止预算
- **WHEN** `kandev service install` 生成 systemd user 或 system unit
- **THEN** 单元包含 `TimeoutStopSec=60s`（或更大的固定值），且 launcher 单元测试同步更新

#### Scenario: macOS 不在本期范围
- **WHEN** 部署运行在 macOS launchd
- **THEN** 本次不修改 plist 的 `ExitTimeOut`，idle gate 仍按本文档执行

## Known Failure Modes

| Symptom | Cause | Fix |
|---------|-------|-----|
| 部署重启后大量会话进入 delivery deadlock | restart 前没有检查在途 session/submission | 在 publish 前加入 idle gate |
| 拒绝部署时 live runtime 已被替换 | gate 放在 publish 之后 | gate 必须在 `publish_bundle` 之前 |
| agent drain 被 systemd 截断 | `TimeoutStopSec` 小于后端 35s 预算 | 提升到 60s |
| 从 session 内 `make deploy` 永远被拒 | 调用者自身是 RUNNING session | 提供 `--force` / `DEPLOY_FORCE=1` 显式覆盖 |

