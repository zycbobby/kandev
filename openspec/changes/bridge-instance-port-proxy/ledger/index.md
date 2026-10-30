# bridge-instance-port-proxy Apply Ledger

Executor: inline（用户规则：未完成实现 task < 5 且线性依赖，不派 subagent）

## Per-Task Ledger

| task id | executor | 验证方法 | 结果 | 状态 | 时间戳 |
|---|---|---|---|---|---|
| 1 port-proxy 桥接路由与 WS 泛化（TDD） | inline | 先写 `portproxy_test.go` 四测试确认按 404/握手失败（红），实现后 `go test -trimpath ./internal/instances/proxy/ -count=1` 全绿、`./internal/instances/...` 邻包全绿、`bash scripts/lint-go-changed` 0 issues（goconst 触发后以 `wsScheme` helper 消除第三处 `"https"` 字面量） | 4 新测试 + 既有回归全部通过 | done | 2026-10-30 |
| 2 multi-instance e2e 补预览桥接断言 | inline | `pnpm e2e:run -- e2e/tests/multi-instance/multi-instance.spec.ts`（标准 runner，workers=1）：3 passed (1.4m)，exit 0；首测改名纳入 preview bridge，走真实双后端链路（代理端口发布 → /port-proxy 桥接 GET 200 含 marker → /nope 404）；prettier --check 与 e2e:sleep-ratchet 干净 | 全绿 | done | 2026-10-30 |
| 3 用户文档同步 | inline | 人工核对段落 diff：五项不可用清单与 Arbitrary TCP 句原样保留，两句之间新增一句区分「Preview HTML 经桥接可用」与「Browser 预览面板/port-forward/tunnel 不可用」；其余章节零改动 | 通过 | done | 2026-10-30 |

## Exit Gate

- [x] **Verification Strategy 入口通过**（2026-10-30，最终代码树复跑）：
  - 主验收 integration-test：`go test -trimpath ./internal/instances/proxy/ -count=1` → ok（4 个新测试行使子树路由、requireBridgeAuth 401、凭据注入与 Cookie/Origin 剥离、query 透传、WS 桥接保留 path+query；goleak TestMain 兜底连接泄漏；既有 `/ws`、`/api/v1/`、`/healthz` 回归不变）。
  - 次验收 e2e-script：`pnpm e2e:run -- e2e/tests/multi-instance/multi-instance.spec.ts`（标准 runner，workers=1，不加 all-worker 覆盖）→ 3 passed (1.4m)，exit 0。首个 test 的 preview bridge 段走真实双后端链路：远端视图经代理端口发布 HTML 预览 → `GET /port-proxy/<sid>/<port>/index.html?v=<version>` 200 且含 marker → 同端口 `GET /nope` 404。
  - 辅助：`./internal/instances/...` 邻包全绿（无回归）；`bash scripts/lint-go-changed <3 个改动 go 文件>` → 0 issues；改动 spec 文件过 prettier 与 e2e:sleep-ratchet。
  - 手工冒烟（可选项）：跳过——e2e 已在真实双后端环境行使同一用户路径。
- [x] **openspec validate**：`openspec validate bridge-instance-port-proxy --strict --no-interactive` → "Change 'bridge-instance-port-proxy' is valid"。
- [x] **验收方法与证据**：方法 = 包内集成测试（httptest fake remote）+ multi-instance e2e 真实双后端链路；场景 = 上表 Verification Strategy 两级验收 + 验证清单全部命令；结果 = 全部通过（证据见 Per-Task Ledger 各行与上方命令输出摘要）。

