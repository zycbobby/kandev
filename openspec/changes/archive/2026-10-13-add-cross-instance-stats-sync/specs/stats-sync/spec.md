# stats-sync

## 背景

用户在两台机器上各运行一个 Kandev 实例：LOCAL（本机 dev 机，新代码）与 REMOTE（第二实例，旧 commit，构建冻结）。LOCAL 的统计页（/stats）只能看到本机活动。统计页的全部区块都在查询时从原始表实时计算，无法通过转发汇总数字合并，必须把 REMOTE 的原始行送进 LOCAL 的数据库。方向已定：LOCAL 作为聚合方新增 ingest API；REMOTE 用既有版本自带的 scheduled automation 定期推送，保持零产品代码改动、零直接 DB 写入。

## 方案与决策

- **角色分配**：REMOTE = 数据源（stock scheduled automation + 随仓库发布的同步脚本）；LOCAL = 聚合方（新 `statsync` 模块 + 镜像表 + analytics UNION）。新代码全部落在已经是新版的 LOCAL。
- **写库形态**：专用镜像表 + analytics 查询 UNION 镜像行。否决「镜像直写核心表」：外键强制开启会要求连带伪造 `repositories`/`task_repositories`/`task_environments` 行，且仓库约定要求任务行变更发事件；镜像表方案同时保证本机看板/任务列表不被污染。
- **认证**：ingest 端点用共享密钥自认证（常量时间比较，沿用 automation webhook 先例）；不采用 PAT（需要在本机开启全局 auth，行为面变化更大）。
- **幂等与容错**：payload 携带 `schema_version` 与 `source_instance`；每行以 (source_instance, 原始 ID) 幂等 upsert；watermark 状态文件存 REMOTE 侧，推送成功才推进；失败重推累积增量。
- **workspace 归并**：payload 携带 remote workspace 身份，LOCAL 侧以配置映射 source → 本地 workspace，默认映射到用户主 workspace（满足「完整合并统计」），v1 无 UI。
- **远端删除**：v1 周期性全量对账（每 N 次 push 附带全量 ID inventory，LOCAL 清理 inventory 外的镜像行）；tombstone 推迟。
- **v1 边界**：API-only，无设置 UI、无 i18n、无实时同步、无 LOCAL→REMOTE 反向同步；REMOTE 抽取脚本 v1 只实现 SQLite 路径（Postgres 是一等引擎，脚本须探测引擎并在非 SQLite 时显式报错，作为已知限制记录）。

## ADDED Requirements

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
