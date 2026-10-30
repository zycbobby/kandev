## Triage

- **现象**：预期——远端实例视图下点 `Preview HTML`，iframe 像本机一样加载已发布的原生 HTML 预览；实际——发布成功（POST 经代理端口转发，远端预览服务器启动，SPA 显示预览就绪），iframe 却永远 404（Go ServeMux 内置 404 页）。master spec 同时要求远端视图下预览面显示明确的「不可用」提示——当前两个都没做：既不桥接、也不提示，看起来就绪却 404。
- **受影响 spec**：`openspec/specs/multi-instance-management/spec.md` → Requirement「宿主绑定能力降级」（明文 "arbitrary TCP port proxying across instances SHALL NOT be provided"）；关联 `docs/specs/ui/requirements/native-html-preview.md` AC-UI-NATIVE-HTML-PREVIEW-001.6（承诺预览走 session 授权的 port-proxy 路径）。
- **性质判定**：B spec 语义变更（方向修订）+ 修代码。「跨实例 SHALL NOT 提供 port proxy」是 v1 联邦的保守决策，实际把 AC-001.6 承诺的预览能力在远端视图下留成永久 404；本次显式修订：session 作用域、经认证桥接的 port-proxy 子树 SHALL 提供，任意 TCP 端口代理/隧道仍 SHALL NOT（用户在诊断阶段已确认走桥接方向）。
- **复现**：已复现（诊断阶段实测）：`GET http://127.0.0.1:54990/port-proxy/x/49152/index.html` → `404 page not found`；同端口 `GET /api/v1/workspaces` → 200；发布接口经同端口转发正常。
- **根因初判**：`apps/backend/internal/instances/proxy/server.go:77-81` 的 mux 仅注册 `/healthz`、`/api/v1/`、`/ws`，无 `/port-proxy/` 路由。

## Why

远端实例视图下原生 HTML 预览是坏的：发布成功、预览服务器在远端启动、SPA 显示就绪，iframe 却永远 404——既没有桥接、也没有 spec 要求的「不可用」提示，用户看到的是假就绪。AC-UI-NATIVE-HTML-PREVIEW-001.6 承诺预览走 session 授权的 port-proxy 路径，而 multi-instance-management v1 的 SHALL NOT 决策与该承诺冲突，结果是该能力在远端视图整体不可用且以最差方式呈现。Explore 已验证桥接可行（认证链、capability 透传均有代码证据），修复面只在本机代理一层，远端与前端零改动。

## What Changes

**本机实例代理桥接 /port-proxy/ 子树（核心）**

- From: 实例代理 mux 只转发 `/healthz`、`/api/v1/`、`/ws`；浏览器对 `/port-proxy/<sessionId>/<port>/...` 的请求命中 ServeMux 内置 404。
- To: 注册 `/port-proxy/` 子树并包 `requireBridgeAuth`：普通 HTTP 走现有 `httpProxy`（PAT 注入、Set-Cookie/CORS 剥离语义原样生效），WebSocket upgrade 走泛化后的 bridge 到远端 gateway 同路径。
- Reason: 这是 AC-001.6 承诺的 session 授权路径在远端视图下缺的最后一跳；发布已走同一代理端口工作，桥接后预览闭环。

**WS bridge 泛化**

- From: `s.bridge`/`wsTarget` 硬编码拼 `/ws` 路径并丢弃 RawQuery。
- To: dial 目标参数化：保留请求原始 path 与 query（预览 dev server 的 HMR WS 带 query），保留 `run.trackConnections` 的 Stop 清理语义。
- Reason: `/ws` 专用形态无法承载子树桥接；连接跟踪是 Stop 时不泄漏 hijacked 连接的既有纪律。

**spec 修订（multi-instance-management）**

- From: 「宿主绑定能力降级」把 session port-proxy/文件预览列为不可用提示 + SHALL NOT 任意跨实例端口代理。
- To: 修订为「session 作用域、经认证桥接的 port-proxy 子树 SHALL 提供」；宿主绑定降级收窄到桌面特权动作与任意端口转发/隧道；「每实例本机代理端口」增加桥接场景（HTTP/WS、凭据归属、桥接认证）；Known Failure Modes 补两行。
- Reason: 把 v1 的 SHALL NOT 显式改为受边界约束的 SHALL——边界（session 授权、PAT 归属、bridge capability）全部是已存在并测试过的机制。

**测试**

- `internal/instances/proxy` 新增路由测试：HTTP GET 经桥达远端 port-proxy（断言凭据注入与 path 透传）、未匹配路径仍 404、auth-on 无本地 bridge cookie 返回 401、WS 桥接保留 path+query。

**不在本 change 范围**

- 不改 gateway port-proxy、agentctl port-proxy、workspace preview server（远端链路零改动）。
- 不改前端 `config.ts` / `process-api.ts`（iframe URL 已正确指向代理端口）。
- 不动 `modifyUpstreamResponse` 的 Set-Cookie/CORS 剥离纪律；不在本地桥重复实现远端 capability 语义。
- 不提供通用任意 TCP 端口代理；port-forward/tunnel 面继续走不可用提示。
- 不改认证语义：bridge capability cookie、PAT 注入、远端 `CheckSessionExecAccess` 授权均沿用现状。

## Capabilities

### New Capabilities

（无：本 change 不引入新能力，只扩展现有每实例代理端口的面。）

### Modified Capabilities

- `multi-instance-management`: 「宿主绑定能力降级」修订——session 作用域的 port-proxy/原生 HTML 预览改为经认证桥接提供，SHALL NOT 收窄到任意 TCP 端口代理/隧道；「每实例本机代理端口」增加 port-proxy 桥接场景。

## Impact

- **代码**：`apps/backend/internal/instances/proxy/server.go`（注册子树 + 泛化 dial）、`bridge.go`；对应路由测试。无其它包改动。
- **接口**：本机实例代理端口新增 `/port-proxy/` 子树转发；远端契约不变。发布接口 `POST /api/v1/task-sessions/<sid>/html-previews` 现状已工作。
- **安全**：浏览器侧仍由本地 bridge capability cookie 把关；上游凭据仍只有注册 PAT；远端仍按 PAT 归属用户做 session 授权。**凭据归属要求**：注册 PAT 必须属于远端该 session 的 owner。`modifyUpstreamResponse` 继续剥离远端 Set-Cookie——远端 capability 以 `kandev_cap` query param 嵌在重写后的资产 URL 透传，cookie 形态不跨桥。
- **已知限制**：不带 `kandev_cap` 又无 cookie 的动态子请求在 auth-on 远端会 401（与单实例 `?token=` 打开的预览同类限制）；无凭据 + auth-on 远端 → iframe 内显示远端 401。
- **文档**：`docs/public/multi-instance.md`「Host-bound actions while viewing a remote」需同步——预览从不可用清单移出，任意端口转发/隧道仍不可用。
- **验证**：`go test ./internal/instances/proxy/ -count=1`；`bash scripts/lint-go-changed <changed-go-files>`。

## Research Log

### 未知 1：iframe 404 的根因是否确为代理缺路由
- **验证方式**：读实例代理 mux 注册代码 + 诊断阶段 curl 实测。
- **结论（试通证据）**：mux 仅注册 `/healthz`、`/api/v1/`、`/ws`，无 `/port-proxy/`；`GET /port-proxy/x/49152/index.html` → `404 page not found`，同端口 `/api/v1/workspaces` → 200。
- **证据摘要**：`apps/backend/internal/instances/proxy/server.go:77-81`；SPA iframe URL 构造 `apps/web/lib/api/domains/process-api.ts:31-47`；remote-instance 视图下 `__KANDEV_API_PORT` 指向代理端口（`apps/backend/internal/webapp/shell.go:177-178`、`apps/web/lib/config.ts:22-36`）。

### 未知 2：桥接后远端认证链是否走得通
- **验证方式**：读远端 auth 中间件与 gateway 认证代码。
- **结论（试通证据）**：本地桥注入 `Authorization: Bearer <instance PAT>`；远端 `/port-proxy/` 是 auth 中间件 deferred path，bearer 凭据仍解析进 gin context；gateway `requireConnectionAuth` 见身份放行；`HandlePortProxy` 的 `CheckSessionExecAccess` 按 PAT 归属用户鉴权。凭据归属要求：PAT 必须属于远端该 session 的 owner。
- **证据摘要**：`instances/proxy/server.go:116-133`（upstreamHeaders）、`internal/auth/httpmw/middleware.go:380`（deferred path）、`internal/gateway/websocket/access.go:88-126`（requireConnectionAuth）、`internal/gateway/websocket/port_proxy.go:144`（CheckSessionExecAccess）。

### 未知 3：远端 capability cookie 被剥离后子请求如何认证
- **验证方式**：读两侧 ModifyResponse 与 capability 校验代码。
- **结论（试通证据）**：本地 `modifyUpstreamResponse` 剥离远端 Set-Cookie；capability 同时以 `kandev_cap` query param 嵌在重写后的资产 URL（body 原样透传），远端 `resolvePortProxyCapability` 接受 query 形式。残留限制：不带 cap 又无 cookie 的动态子请求在 auth-on 远端 401。
- **证据摘要**：`instances/proxy/server.go:161-169`、`internal/gateway/websocket/access.go:152-159`、`internal/gateway/websocket/port_proxy.go:333-363`（重写 + strip）。

### 未知 4：现有 WS bridge 能否直接承载子树流量
- **验证方式**：读 bridge 与 wsTarget 实现。
- **结论（拒绝理由）**：不能直接用——`wsTarget` 硬编码拼 `/ws` 且丢弃 RawQuery，子树桥接必须保留原始 path+query；需参数化 dial 目标并保留 `run.trackConnections` 的 Stop 清理语义。
- **证据摘要**：`instances/proxy/server.go:182-192`、`instances/proxy/bridge.go:12-38`。

### 未知 5：远端链路与前端是否需要改动
- **验证方式**：读远端 gateway→agentctl→preview 链路与前端 URL 构造。
- **结论（试通证据）**：远端 gateway 已把 `/port-proxy/:sid/:port/*` 重写为 agentctl `/api/v1/port-proxy/:port/*`，runtime shim、Location 重写、Referer 清洗全在远端完成；前端 iframe URL 已正确指向代理端口。两侧零改动。
- **证据摘要**：`internal/agentctl/server/api/server.go:226-227`、`internal/gateway/websocket/port_proxy.go:184-231`、`process-api.ts:31-47`。

### 未知 6：测试能否复用现有模式
- **验证方式**：读 `internal/instances/proxy` 既有测试。
- **结论（试通证据）**：已有 httptest fake remote + newTestServer + goleak TestMain，新路由测试照此覆盖。
- **证据摘要**：`instances/proxy/server_test.go:23,34,42`。
