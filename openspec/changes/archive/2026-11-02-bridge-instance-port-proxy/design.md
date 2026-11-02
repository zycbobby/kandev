# bridge-instance-port-proxy 设计

实现 proposal（`./proposal.md`）与 spec delta（`./specs/multi-instance-management/spec.md`）定义的行为：本机实例代理桥接远端的 session 作用域 `/port-proxy/` 子树。远端链路（gateway → agentctl → workspace preview）与前端零改动；本 change 只动 `internal/instances/proxy` 一层加测试与文档。

## 决策

### 1. 路由注册与 WS/HTTP 分发

`Start` 的 mux 增加一行注册，子树统一包 `requireBridgeAuth`（与 `/api/v1/`、`/ws` 同一认证与 CORS 语义）；分发器判断 upgrade：

```go
mux.Handle("/port-proxy/", s.requireBridgeAuth(s.portProxy(target, run)))

// portProxy 把子树流量按形态分发：WebSocket upgrade 走桥接（保留 Stop 时的
// hijacked 连接跟踪），其余走现有 httpProxy。
func (s *Server) portProxy(target *url.URL, run *serverRun) http.Handler {
    proxy := s.httpProxy(target, run.transport)
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if websocket.IsWebSocketUpgrade(r) {
            s.bridge(run, wsSubtreeTarget(target, r), w, r)
            return
        }
        proxy.ServeHTTP(w, r)
    })
}
```

- 拒绝「全部走 httpProxy（ReverseProxy 原生支持 101 upgrade）」：hijacked 连接不进 `run.connections`，`Stop()` 的「先关 admission、再关 hijacked 连接」纪律（`bridge.go` close 注释与包内 goleak TestMain）会被绕过。
- 拒绝「为 port-proxy 另写一条 gorilla 桥」：与现有 bridge 复制 `copyFrames`/跟踪逻辑，无新增语义。

### 2. WS bridge 泛化签名

`bridge` 的 dial 目标从「内部按 target 拼 `/ws`」改为调用方传入完整 dial URL，`/ws` 语义不变：

```go
// bridge.go
func (s *Server) bridge(run *serverRun, dialURL string, w http.ResponseWriter, r *http.Request)
// 原 wsTarget(target) 调用点移到 Start 的 /ws handler：s.bridge(run, wsTarget(target), w, r)

// server.go —— 子树 dial 目标：host/scheme 来自 target，path+query 来自请求
func wsSubtreeTarget(target *url.URL, r *http.Request) string {
    u := *target
    u.Scheme = "ws"
    if target.Scheme == "https" { u.Scheme = "wss" }
    u.Path, u.RawPath, u.RawQuery = r.URL.Path, r.URL.RawPath, r.URL.RawQuery
    return u.String()
}
```

- `wsTarget` 保持原样（`/ws` 专用，TrimRight + 固定 path + 丢弃 query 的行为对 `/ws` 是正确的）。
- `RawPath` 必须透传：SPA 对 session id 逐段 `encodeURIComponent`，编码后的斜杠不能被二次解码。
- 唯一既有调用点在 `Start`（`server.go:80`），签名变更影响面为 1 行。

### 3. 测试文件归属

新建 `apps/backend/internal/instances/proxy/portproxy_test.go`。`server_test.go` 现在 192 行、追加也可，但新文件让新子树的测试自成一体（先例：`bridgeauth_test.go`、`security_test.go` 按面拆分），且完全规避 revive 800 行上限的长期风险。helper（`startProxy`、`proxyURL`、`testLog`）同包直接复用。

### 4. 集成测试矩阵（`portproxy_test.go`，fake remote = `httptest.NewServer`）

| 测试 | 断言 |
|---|---|
| `TestProxyBridgesPortProxyHttp` | 远端收到 `GET /port-proxy/s1/49152/index.html?kandev_cap=x`，`Authorization: Bearer <credential>`（无 PAT 时为空），Cookie/Origin 被剥离；本地经 `proxyURL(s)` 拿到 200 与远端 body；带 PAT/无 PAT 两个 subtest（对齐 `TestProxyForwardsApiAndInjectsCredential` 形态） |
| `TestPortProxyUnknownPathStill404` | `GET /nope` 与 `GET /port-proxyx` 均 404，远端零调用（ServeMux 子树边界） |
| `TestPortProxyRequiresBridgeCapability` | `startProxy(..., auth: true, ...)` 下无 cookie 的 `/port-proxy/` 请求返回 401 `bridge_capability_required`，远端零调用（wrapper 覆盖由 `bridgeauth_test.go` 既有测试保证，这里只钉路由被包住） |
| `TestPortProxyWsBridgePreservesPathAndQuery` | 远端在精确 `r.URL.Path == "/port-proxy/s1/49152/" && r.URL.RawQuery == "hmr=1"` 时才 upgrade；本地 gorilla dial `ws://…/port-proxy/s1/49152/?hmr=1` 完成双向帧（对齐 `TestWsBridgeBidirectionalFrames` 形态） |

### 5. e2e 设计（真实双后端链路）

扩展现有 `apps/web/e2e/tests/multi-instance/multi-instance.spec.ts` 第一个 test（改名纳入 preview 桥接），在远端 chat 会话建立之后追加一段：

1. `page.evaluate` 读 `window.__KANDEV_API_PORT`（切到 B 后即实例代理端口）。
2. 经该端口 `POST /api/v1/task-sessions/<sid>/html-previews`（`page.request`，body `{path:"index.html", content:"<!doctype html><title>kandev-preview-bridge</title>"}`）→ 200 `{port, path, version}`。
3. `page.request.get` 同端口的 `/port-proxy/<sid>/<port>/index.html?v=<version>` → 200 且 body 含 marker。
4. `GET /nope`（同端口）→ 404（顺手钉边界，不建新 test）。

约束：等待语义用 `e2e/helpers/causal-waits.ts`（`waitForHttp`/默认 timeout 的 `expect.poll`），不新增 sleep；e2e 无认证环境（`requireBridgeAuth` 关闭），auth-on 路径由集成测试矩阵覆盖。

### 6. 用户文档同步边界

`docs/public/multi-instance.md`「Host-bound actions while viewing a remote」段（62-71 行）：清单五项（open in editor/folder、file tree open-in-editor、port-forward open、Browser 预览面板、tunnel toggle）**全部保留**——Browser 预览面板走的就是 host-proxied port-forward，不属于本次桥接面。在该段末尾、「Arbitrary TCP port proxying across instances is not provided.」句之前加一句：session 文件查看器的 `Preview HTML`（native HTML preview）经实例代理桥接可用；两句并用划清边界。其余章节不动。

## 已知限制（明确不解决）

- 不带 `kandev_cap` 又无 cookie 的动态子请求在 auth-on 远端 401（capability 只随重写后的资产 URL 透传；与单实例 `?token=` 预览同类）。
- 无凭据 + auth-on 远端：桥无 PAT 可注入，iframe 内呈现远端 401 页。
- 两行均已写入 spec delta 的 Known Failure Modes。

## 风险与回退

- **.ServeMux 冲突**：`/port-proxy/` 与现有三个 pattern 无交集；回归由既有全包测试兜底。
- **响应被压缩破坏重写**：远端 gateway 出站已删 `Accept-Encoding` 并由 Transport 透明解压，body 经本地桥原样透传，无需在桥上再处理编码。
- **回退**：revert 单个提交即回到三路由现状；无 schema/配置/flag 变更。

## Research Log

### 未知 1：升级请求识别与 WS 测试形态是否有库内先例
- **验证方式**：grep + 读包内既有测试。
- **结论（试通证据）**：`websocket.IsWebSocketUpgrade` 有两处库内使用（`internal/agentctl/server/api/passthrough.go:182`、`internal/gateway/websocket/terminal_handler.go:185`）；fake remote 升级 + 双向帧回显的测试形态即 `TestWsBridgeBidirectionalFrames`（server_test.go:106-145）。
- **证据摘要**：上述文件行号。

### 未知 2：新测试放哪、会不会撞 800 行上限
- **验证方式**：`wc -l` 全包文件。
- **结论（试通证据）**：包内最大文件 192 行；选新文件 `portproxy_test.go` 是内聚优先而非上限所迫。
- **证据摘要**：`wc -l apps/backend/internal/instances/proxy/*.go`（合计 800，单文件最大 server.go / server_test.go 各 192）。

### 未知 3：multi-instance e2e 能否承载预览断言、是否有门控
- **验证方式**：读 `multi-instance.spec.ts` 头部与 `e2e/README.md`。
- **结论（试通证据）**：`bootRemoteBackend`/`seedRemoteBackendTask` fixture 已搭好双后端与远端任务；`apiClient.rawRequest`/`page.request` 可打代理端口；spec 无环境门控、随标准套件跑（README:435 有文档节）。
- **证据摘要**：`apps/web/e2e/tests/multi-instance/multi-instance.spec.ts:1-124`、`apps/web/e2e/README.md:435`。

### 依赖准出门禁
- gorilla/websocket（Dialer/Upgrader/IsWebSocketUpgrade）：库内依赖，上述先例即调研结论，无外部验证需求。
- `httptest` fake remote、`httputil.ReverseProxy` 转发、goleak TestMain：全部是本包既有测试在用的机制，非仓库外知识。
- 无第三方 API、外部 CLI、agent/runtime 行为依赖 → 无 blocker。
