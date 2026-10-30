# bridge-instance-port-proxy 任务

按序推进；每条任务有独立文件边界与准出标准，完成后在下一条开始前勾选。任务 1 内部按 TDD 顺序（先失败测试、再实现、再转绿）。全部完成并验证后进入 verify / archive；apply 过程中的实现决策只回写本 change 的 ledger，不改 spec delta（已定稿）。

## Verification Strategy

- **主验收：integration-test**——`internal/instances/proxy` 包内集成测试（httptest fake remote），能完整行使新子树的路由、认证、凭据注入、query 透传与 WS 桥接，是本 change 最小可信闭环。
- **次验收：e2e-script**——multi-instance e2e 扩展一段真实双后端链路（本机代理 → 远端 gateway → 远端 agentctl → workspace preview server），证明用户路径而非仅包行为。
- **辅助**：`scripts/lint-go-changed`（golangci 新代码阈值）；既有全包测试作为回归边界（`/ws`、`/api/v1/` 行为不得变化）。
- unit-test 不作为唯一验收；未使用 agent/skill 沙箱类验证（测试对象是具体程序代码）。

## E2E 测试图

用户路径：远端实例视图 → 文件查看器 `Preview HTML` → 发布（`POST /api/v1/task-sessions/<sid>/html-previews` 经代理端口）→ iframe 加载 `/port-proxy/<sid>/<port>/<path>` → 资产/动态子请求（`kandev_cap` 随重写 URL 透传）。

关键节点与覆盖层级：

| 节点 | 覆盖 |
|---|---|
| 代理 mux 注册 `/port-proxy/` + `requireBridgeAuth` | integration：`TestPortProxyRequiresBridgeCapability`、`TestPortProxyUnknownPathStill404` |
| HTTP 转发（凭据注入、Cookie/Origin 剥离、query 透传） | integration：`TestProxyBridgesPortProxyHttp`；e2e 步骤 2-3 |
| WS upgrade 分发 + path/query 保留 + 连接跟踪 | integration：`TestPortProxyWsBridgePreservesPathAndQuery`（goleak TestMain 兜底泄漏） |
| 真实远端链路（gateway→agentctl→preview server） | e2e：`multi-instance.spec.ts` 首个 test 新增 preview bridge 段 |

## 实现清单

- [x] 1. port-proxy 桥接路由与 WS 泛化（TDD）
  - Files: `apps/backend/internal/instances/proxy/server.go`（注册 + `portProxy` + `wsSubtreeTarget`）、`apps/backend/internal/instances/proxy/bridge.go`（`bridge` 签名改 `dialURL string`，`/ws` 调用点改传 `wsTarget(target)`）、Create `apps/backend/internal/instances/proxy/portproxy_test.go`
  - 边界: 不动 gateway / agentctl / workspace preview / 前端 / `modifyUpstreamResponse` / `wsTarget`；`/api/v1/`、`/ws`、`/healthz` 行为不变
  - 步骤: 先写 `portproxy_test.go` 四个测试（见 design 决策 4 的矩阵与断言）→ 跑测试确认按预期失败（404）→ 按 design 决策 1-2 实现 → 转绿
  - 准出: `cd apps/backend && go test -trimpath ./internal/instances/proxy/ -count=1` 全绿；`bash scripts/lint-go-changed <改动 go 文件>` 干净（repo 根执行）
  - Commit: `fix(instances): bridge the port-proxy subtree through the instance proxy`

- [x] 2. multi-instance e2e 补预览桥接断言
  - Files: `apps/web/e2e/tests/multi-instance/multi-instance.spec.ts`（首个 test 追加 preview bridge 段并改名纳入；test 名同步描述）
  - 边界: 只扩展既有 test 的远端 chat 建立之后段落，复用 `bootRemoteBackend`/`seedRemoteBackendTask` 与既有 helper；不新建 fixture、不新增 test、不加 sleep（等待用 `causal-waits.ts`）
  - 步骤: 按 design 决策 5 的 4 步实现（读 `__KANDEV_API_PORT` → 代理端口发布 → 同端口 GET 预览断言 marker → `GET /nope` 断言 404）
  - 准出: `cd apps/web && pnpm e2e:run` 中 multi-instance 分片全绿（遵守单 worker/内存分片预算，不加 all-worker 覆盖）；改动文件过 prettier hook
  - Commit: `test(multi-instance): cover the preview port-proxy bridge end to end`

- [x] 3. 用户文档同步
  - Files: `docs/public/multi-instance.md`（「Host-bound actions while viewing a remote」段）
  - 边界: 只动该段——五项不可用清单与「Arbitrary TCP port proxying across instances is not provided.」句保留，仅在两句之间补一句：session 文件查看器的 `Preview HTML` 经实例代理桥接可用（session-scoped port-proxy）
  - 准出: 段落能区分「Preview HTML 可用」与「Browser 预览面板 / port-forward / tunnel 不可用」；无其他章节改动
  - Commit: `docs(multi-instance): distinguish bridged HTML preview from host-bound previews`

## 验证清单

- [ ] `cd apps/backend && go test -trimpath ./internal/instances/proxy/ -count=1` 全绿（含新增 4 测试与既有回归）
- [ ] `cd apps/backend && go test -trimpath ./internal/instances/... -count=1` 全绿（instances 邻包无回归）
- [ ] `bash scripts/lint-go-changed apps/backend/internal/instances/proxy/server.go apps/backend/internal/instances/proxy/bridge.go apps/backend/internal/instances/proxy/portproxy_test.go` 干净
- [ ] `cd apps/web && pnpm e2e:run` 的 multi-instance 规格全绿
- [ ] `openspec validate bridge-instance-port-proxy --strict --no-interactive` 通过
- [ ] 手工冒烟（可选，需双实例环境）：远端实例视图发布 Preview HTML，iframe 加载出文档与资产
