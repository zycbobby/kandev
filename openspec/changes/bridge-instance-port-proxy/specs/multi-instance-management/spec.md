# multi-instance-management

## 背景

原生 HTML 预览（`Preview HTML`）走 session 授权的 port-proxy 路径（`/port-proxy/:sessionId/:port/*`），这条路径在本机、Docker、SSH、Kubernetes session 上都已工作（AC-UI-NATIVE-HTML-PREVIEW-001.6）。但切换到远端实例视图后，SPA 的 API base 指向本机实例代理端口，而代理 mux 只注册了 `/healthz`、`/api/v1/`、`/ws`——iframe 对 `/port-proxy/` 的每个请求都命中 Go ServeMux 的内置 404。实测表现是：发布成功（POST 经 `/api/v1/` 转发、远端预览服务器启动、SPA 显示就绪），iframe 却永远 404。

v1 spec 的「宿主绑定能力降级」需求为这种情况规定的语义是「显示不可用提示 + SHALL NOT 提供跨实例 port proxy」。实现两者都没做，用户看到的是假就绪；而预览能力本身（AC-001.6 承诺）在远端视图下被这条 SHALL NOT 永久排除。诊断与代码证据确认：桥接所需的全部边界机制都已存在并经过测试——本地 bridge capability cookie（`requireBridgeAuth`）、注册 PAT 注入（`upstreamHeaders`）、远端 deferred-path 认证（`auth/httpmw` + gateway `requireConnectionAuth`）、远端 session 授权（`CheckSessionExecAccess`）、远端 capability 经 `kandev_cap` query param 透传。因此本次把「session 作用域的 port-proxy 子树」从 SHALL NOT 中显式豁免出来，改为经认证桥接提供；任意 TCP 端口代理/隧道的 SHALL NOT 保持不变。

## 方案与决策

- **只扩本机代理一层。** 远端 gateway→agentctl→workspace preview 链路与前端 URL 构造零改动；发布接口现状已工作，桥接补齐的是预览加载的最后一跳。
- **HTTP 走现有 `httpProxy`，WS 走泛化后的 bridge。** HTTP 复用 PAT 注入与 Set-Cookie/CORS 剥离语义；现有 `wsTarget` 硬编码 `/ws` 且丢弃 query，无法承载子树，需参数化 dial 目标（保留原始 path+query，如预览 dev server 的 HMR websocket），并保留 `run.trackConnections` 的 Stop 清理语义（hijacked 连接不在 `http.Server.Close` 范围内）。
- **不在本地桥重复实现远端 capability。** 本地桥只认自己的 bridge capability cookie；远端 mint 的 `kandev_port_proxy` cookie 被 `modifyUpstreamResponse` 剥离（既有纪律，不放宽），capability 以 `kandev_cap` query param 嵌在重写后的资产 URL 里原样透传，远端 `resolvePortProxyCapability` 已接受该形式。
- **凭据归属是前提。** 远端 `CheckSessionExecAccess` 按 PAT 归属用户鉴权，注册 PAT 必须属于远端该 session 的 owner；把这一要求写成显式 Scenario 而不是留给实现注释。
- **降级需求收窄而非删除。** 桌面特权动作（open in editor/folder、桌面通知）、Browser 面板 port-forward、tunnel 开关继续不可用提示；SHALL NOT 收窄到「任意 TCP 端口代理与隧道」，与本 change 桥接的 session 授权子树明确区分。
- **不把实现细节写进 Requirement。** Requirement 陈述行为（子树经认证桥接、凭据注入、query 透传）；dial 如何参数化属于实现选择。

## MODIFIED Requirements

### Requirement: 每实例本机代理端口

For each enabled registered instance the local backend SHALL provide a reverse proxy on a dedicated local port bound to the same hostname as the local UI, forwarding the remote instance's HTTP API, its session-scoped port-proxy subtree (`/port-proxy/:sessionId/:port/...`), and a bidirectional WebSocket bridge, injecting the registered credential on upstream requests when present, and surfacing remote unreachability as an explicit error to the browser client. The port-proxy subtree SHALL require the same local bridge authentication as the rest of the proxy surface, and the remote gateway remains the sole authority for session-scoped port access.

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

#### Scenario: 预览 iframe 经桥加载
- **WHEN** the panel views a remote instance and a session publishes a native HTML preview
- **THEN** the preview iframe loads the published document and its assets through the proxy port's port-proxy subtree, with the credential injected upstream and the remote-minted capability traveling as the `kandev_cap` query parameter (remote Set-Cookie stays stripped by the bridge)

#### Scenario: 预览凭据归属
- **WHEN** the remote instance enforces authentication and the registered credential belongs to the remote owner of that session
- **THEN** the remote gateway authorizes the port-proxy request via its session access check; a credential of a different user yields the remote's not-found answer, surfaced as the preview's load failure

#### Scenario: 桥接子树的认证与路由边界
- **WHEN** authentication is enabled and a request without the local bridge capability cookie hits the proxy port's port-proxy subtree, or any request hits a path outside the registered proxy subtrees
- **THEN** the subtree answer is 401 without forwarding, and unknown paths still answer 404 without reaching the remote

### Requirement: 宿主绑定能力降级

Cross-instance operation SHALL degrade cross-instance host-bound capabilities gracefully: desktop-privileged actions (open in editor/folder, desktop native notifications), Browser-panel port-forward open actions, and the tunnel start toggle SHALL show an explicit unavailable-for-remote-instance affordance instead of failing or silently doing nothing. Arbitrary TCP port proxying and port tunneling across instances SHALL NOT be provided. The session-scoped port-proxy subtree that serves the native HTML preview is exempt from this degradation: it SHALL be bridged through the per-instance proxy under the same bridge authentication and remote session authorization as the rest of the proxy surface.

#### Scenario: 远端任务上的宿主动作
- **WHEN** the user triggers a host-bound action on a task of a remote instance
- **THEN** the UI explains the action is unavailable for remote instances instead of opening a local folder or failing silently

#### Scenario: 任意端口转发与隧道仍不可用
- **WHEN** the user tries to open a Browser-panel port-forward or start a tunnel for a remote session
- **THEN** the UI keeps showing the unavailable-for-remote explanation and no arbitrary TCP proxying or tunneling is provided

#### Scenario: 原生 HTML 预览不再降级
- **WHEN** the user activates `Preview HTML` on a remote session whose instance has a healthy proxy
- **THEN** the preview loads through the bridged port-proxy subtree instead of an unavailable affordance or a silent failure

## Known Failure Modes

| Symptom | Cause | Fix |
|---|---|---|
| 预览 iframe 内动态子请求 401 | 远端 capability 只随重写后的资产 URL（`kandev_cap` query param）透传，远端 Set-Cookie 被本地桥剥离；不带 cap 又无 cookie 的动态子请求被 auth-on 远端拒绝 | 已知限制（与单实例 `?token=` 打开的预览同类）；为该实例登记 PAT 且由 session owner 打开预览 |
| 预览显示远端 401 页 | 该实例未登记凭据（或凭据不属于该 session 的 owner），远端 `requireConnectionAuth` 无凭据可用 | 为该实例登记属于远端 session owner 的 PAT |
