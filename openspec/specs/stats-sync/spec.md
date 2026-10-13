# stats-sync Specification

## Purpose
TBD - created by archiving change add-cross-instance-stats-sync. Update Purpose after archive.
## Requirements
### Requirement: 版本化 raw-row 推送契约

The sync payload SHALL be a versioned envelope carrying `schema_version`, a stable `source_instance` identifier, and raw rows for tasks, task sessions, turns, messages, repository identities, task-repository links, and session commits (git snapshots optional), with all identifiers and timestamps preserved from the source instance.

#### Scenario: 载荷携带版本与来源标识
- **WHEN** the remote sync script pushes a payload
- **THEN** the envelope includes `schema_version`, `source_instance`, and the raw row arrays above

#### Scenario: 版本不匹配被拒绝
- **WHEN** the ingest endpoint receives a payload whose `schema_version` it does not support
- **THEN** the endpoint rejects the whole payload atomically and nothing is written

### Requirement: watermark 增量与失败重推

The remote sync script SHALL persist a watermark locally and advance it only after the local ingest endpoint acknowledges success; a failed or skipped push SHALL retain the watermark so the next run re-pushes the accumulated delta.

#### Scenario: 推送失败不丢数据
- **WHEN** a push fails (network error, non-2xx response)
- **THEN** the watermark stays unchanged and the next scheduled run re-pushes the same delta

### Requirement: 幂等 upsert

The local ingest SHALL upsert rows keyed by (source_instance, original row ID); re-pushing rows already stored SHALL update them in place without creating duplicates.

#### Scenario: 重复推送产生相同结果
- **WHEN** the same delta is pushed twice
- **THEN** the second push updates existing mirror rows and the merged statistics are unchanged

### Requirement: REMOTE 侧仅用既有能力

The remote side SHALL require no Kandev code changes: the push runs as a scheduled automation command whose agent profile pre-approves the script's commands, and the script reads its own database read-only (engine-probed; non-SQLite engines fail with an explicit error in v1).

#### Scenario: 旧版本实例可配置完成接入
- **WHEN** an operator configures a scheduled automation on a stock older Kandev build
- **THEN** the automation runs the sync script on schedule (cron + timezone) without any product code change on that instance

### Requirement: LOCAL ingest 端点与共享密钥认证

The local instance SHALL expose a dedicated ingest endpoint that authenticates callers with a shared secret compared in constant time, and SHALL reject requests with a missing or wrong secret.

#### Scenario: 错误密钥被拒绝
- **WHEN** a request arrives without the secret header or with a wrong secret
- **THEN** the endpoint returns 401 and writes nothing

#### Scenario: 启用 auth 的部署保持可用
- **WHEN** `features.auth` is enabled on the local instance
- **THEN** the ingest endpoint remains callable with only the shared secret (allowlisted as a self-authenticating route)

### Requirement: 镜像存储不污染核心表

Mirrored rows SHALL be stored in dedicated mirror tables owned by the stats-sync feature, written through a dedicated repository write path that emits no task events, creates no live sessions, and leaves task lists, boards, and workflow admission untouched.

#### Scenario: 镜像任务不出现在任务列表
- **WHEN** remote tasks are ingested
- **THEN** the local task lists and boards show no new tasks and no task events are published

### Requirement: 合并统计视图

The local /stats page SHALL render merged statistics per workspace mapping: analytics queries include mirrored rows of mapped sources alongside local rows under the same inclusion rules (non-ephemeral, non-automation-origin), across global, tasks, daily-activity, completed-activity, model-usage, repositories, and git sections.

#### Scenario: 合并后的区块数字
- **WHEN** a user views /stats for the mapped workspace
- **THEN** each section counts local rows plus mirrored remote rows according to the shared inclusion rules

#### Scenario: 未映射的 source 默认归入主 workspace
- **WHEN** a payload arrives from a source instance without a configured workspace mapping
- **THEN** the ingest files its rows under the instance's primary workspace and they count toward the merged statistics

#### Scenario: 映射无法解析被拒绝
- **WHEN** an explicit workspace mapping names a workspace that does not exist, or no default workspace is available for the unmapped source
- **THEN** the ingest rejects the payload and no statistics change

### Requirement: 周期性全量对账处理远端删除

Periodically (every N successful pushes), the sync script SHALL include a full ID inventory of the source; the local instance SHALL delete mirrored rows of that source that are absent from the inventory so remote deletions converge within one reconciliation cycle.

#### Scenario: 远端删除在下一个对账周期收敛
- **WHEN** a task is deleted on the remote instance and the next inventory-bearing push completes
- **THEN** its mirrored rows are removed locally and merged statistics no longer count it


## Known Failure Modes

| Symptom | Cause | Fix |
|---|---|---|
| Boot fails during another store's table-rebuild migration with `error in view v_stats_tasks: no such table: main.tasks` | Views over core tables created during statsync schema init run before later stores DROP/rebuild their tables, leaving the view referencing a missing table | Create views only after every store schema has migrated; `statsync.EnsureViews` runs at the end of boot and rebuilds them idempotently on each start |
