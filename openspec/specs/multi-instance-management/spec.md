# multi-instance-management Specification

## Purpose

用户同时运行多个 Kandev 实例（例如本机一台、3090Ti 上一台）时，只能为每个实例开一个浏览器面板并手动来回切换。浏览器直连第二个实例被 WebSocket 同源校验（防跨站 WS 劫持，`internal/gateway/websocket/origin.go`）与跨站 Cookie 策略阻止，因此「一个面板管理多实例」由本机后端充当联邦客户端实现：本机后端持有多实例注册表，并为每个已注册实例提供反向代理，SPA 把 API/WS base 指向该代理即可像操作本机实例一样操作远端实例。

本 capability 覆盖：实例注册表、每实例代理端口、实例选择器与切换重水合、feature toggle 与桥接安全边界、宿主绑定能力的降级语义。跨实例通知的中继与展示归 `cross-instance-notifications`。

## Requirements
### Requirement: 实例注册表
The local instance SHALL maintain a user-managed registry of remote Kandev instances, each carrying a display name, a base URL, and an optional personal-access-token credential, editable from the Settings UI, with per-instance health status (reachability and reported version) surfaced on the settings page and refreshed by a periodic poller.

#### Scenario: 注册并看到健康状态
- **WHEN** the user adds a remote instance with a reachable base URL
- **THEN** the settings page shows the instance as healthy with its reported version within one poll interval

#### Scenario: 无凭据实例可注册
- **WHEN** the user registers a remote instance without a credential and that instance runs with auth disabled
- **THEN** the registry accepts it and proxying works without credentials

#### Scenario: 注册表为空时无切换入口
- **WHEN** no remote instance is registered (or the feature toggle is off)
- **THEN** the UI shows no instance selector and all existing behavior is unchanged

### Requirement: 每实例本机代理端口
For each enabled registered instance the local backend SHALL provide a reverse proxy on a dedicated local port bound to the same hostname as the local UI, forwarding the remote instance's HTTP API and a bidirectional WebSocket bridge, injecting the registered credential on upstream requests when present, and surfacing remote unreachability as an explicit error to the browser client.

#### Scenario: 直接操作远端任务
- **WHEN** the panel is switched to a remote instance and the user sends a prompt, moves, or stops a task
- **THEN** the action is forwarded through the proxy port and the remote task state updates, with the existing chat/kanban/task-detail UI rendering remote data

#### Scenario: WebSocket 流桥接
- **WHEN** a remote session streams turn/message events while the panel views that instance
- **THEN** the events arrive through the local WS bridge with ordering and reconnection behavior equivalent to a direct connection

#### Scenario: 远端不可达
- **WHEN** a proxied request cannot reach the remote instance
- **THEN** the proxy returns a structured error identifying the instance instead of hanging or fabricating data

#### Scenario: 不放宽既有信任策略
- **WHEN** any browser origin other than the same hostname attempts to use the proxy port
- **THEN** the existing origin checks apply unchanged and no global CORS or WebSocket origin policy is weakened

### Requirement: 实例选择器与切换重水合
The web UI SHALL provide an instance selector in the sidebar header area, and switching the active instance SHALL re-hydrate the entire panel for the selected instance via a full page reload, with the SPA's API and WebSocket base pointed at that instance's proxy port and the browser tab title carrying the instance name.

#### Scenario: 切换后数据全部来自所选实例
- **WHEN** the user switches from the local instance to a registered remote instance
- **THEN** the board, chat, task detail, and Needs You inbox render data fetched through that instance's proxy port after a full reload

#### Scenario: 单活动实例
- **WHEN** the panel is viewing one instance
- **THEN** the SPA store holds live session state for exactly that instance; no second instance's live state is held simultaneously

### Requirement: feature toggle 与桥接认证
The capability SHALL ship behind a runtime feature toggle default-off in every shipped profile, and when authentication is enabled the proxy bridge SHALL authenticate browser clients against the local session before forwarding, and SHALL use the registered PAT credential toward the remote instance.

#### Scenario: 默认关闭行为不变
- **WHEN** the toggle is off
- **THEN** no registry UI, proxy port, or relay is active and behavior is identical to before the change

#### Scenario: 开启认证后桥接仍可用
- **WHEN** local auth and remote auth are both enabled and a PAT is registered for the instance
- **THEN** a browser client authenticated to the local instance can operate the remote instance through the bridge without a separate remote login prompt

### Requirement: 宿主绑定能力降级
Cross-instance operation SHALL degrade cross-instance host-bound capabilities gracefully: desktop-privileged actions (open in editor/folder, desktop native notifications) and session port-proxy/file preview surfaces that bind to the serving host SHALL show an explicit unavailable-for-remote-instance affordance instead of failing or silently doing nothing, and arbitrary TCP port proxying across instances SHALL NOT be provided.

#### Scenario: 远端任务上的宿主动作
- **WHEN** the user triggers a host-bound action on a task of a remote instance
- **THEN** the UI explains the action is unavailable for remote instances instead of opening a local folder or failing silently

## Known Failure Modes

| Symptom | Cause | Fix |
|---|---|---|
| 切换实例后数据仍是本机的 | 代理端口未注入（shell 未读到实例参数）导致 API base 回落同源 | shell/boot 注入路径加回归测试：带实例参数重载后 API base 必须命中代理端口 |
| 远端重启后 WS 桥假死 | 桥接只建立一次不重连 | 桥接实现指数退避重连 + 重连后重新订阅，测试覆盖远端重启场景 |
| 代理端口与主机端口冲突 | 动态分配未感知占用 | 端口分配带冲突重试并持久化分配结果，冲突时健康状态置为降级 |
| 远端旧版本 API 形状漂移 | 远端实例 Kandev 版本落后于本机 | 健康探测携带版本并设最低支持版本，不满足时代理入口给出版本不匹配错误 |
| 凭据泄露进日志 | 代理把 PAT 打进调试日志 | 凭据走 secret store，结构化日志只输出实例名与状态（沿用现有凭据日志纪律） |
| auth 开启后桥接裸奔 | 代理端口复用主端口中间件但漏了认证 | 桥接路由显式挂本地会话校验，测试覆盖未认证浏览器访问代理端口被拒 |
| 开启认证的远端实例健康显示为不可达 | 健康探测不带凭据：远端对 `GET /api/v1/system/info` 回 401，被记为 Reachable=false（v1 目标为无认证远端，apply 阶段裁决保持现状） | 为该实例登记 PAT 后代理与中继可用，但健康横幅仍显示降级；后续让探测携带凭据以区分「需凭据」与「不可达」 |

