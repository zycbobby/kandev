# Verify — add-multi-instance-management

> 漂移检测时间：2026-10-28。方法：派发只读 subagent 对照 proposal.md、design.md、两个 spec delta、tasks.md、apply ledger 与完整 `git diff main...HEAD`（166 文件）逐条核对。

## Drift Report

**Status:** Clean

未发现三类漂移（Missing / Out of Scope / Design Deviation）。逐条核对结果：

### 一、multi-instance-management spec（5 Requirement / 9 Scenario，全部实现）

| Requirement / Scenario | 证据 |
|---|---|
| 实例注册表·注册并看到健康状态 | `internal/instances/health.go`（HealthPoller 复用 healthpoll 90s 节奏、首 pass 立即、`MinRemoteVersion="0.98.0"`、版本比对 + unsupported 错误）；`internal/backendapp/helpers.go:2350` startInstanceHealthPoller 装配；Settings 页 `apps/web/components/settings/instances/instances-settings.tsx` + `instance-health-banner.tsx`（ok/unsupported/degraded/waiting 四态含版本显示）；e2e flow1 断言 banner `data-state=ok` 且含 `v0.98.0` |
| 实例注册表·无凭据实例可注册 | `internal/instances/service.go:81`（credential 可空）；`internal/instances/proxy/server.go:116-133` upstreamHeaders 仅在凭据非空时注入 Authorization；e2e 无凭据注册 + 代理全流程通过 |
| 实例注册表·注册表为空时无切换入口 | `apps/web/components/app-sidebar/app-sidebar-instance-picker.tsx:46`（`!enabled \|\| items.length <= 1` → null） |
| 每实例代理端口·直接操作远端任务 | `internal/instances/proxy/server.go:79`（`/api/v1/**` 反向代理 + Rewrite）；e2e flow3 面板发消息、api-client 直连远端断言状态流转 |
| 每实例代理端口·WebSocket 流桥接 | `internal/instances/proxy/bridge.go`（upgrade → dial 远端 → copyFrames 双向）；`TestWsBridgeBidirectionalFrames` |
| 每实例代理端口·远端不可达 | `server.go:171-175` remoteError → 502 JSON `{error:"remote_unreachable", instance}`；`TestRemoteUnreachableReturnsStructuredError`；e2e flow7 双次断言 |
| 每实例代理端口·不放宽既有信任策略 | `server.go:177-180` allowedOrigin 委托 `httpmw.AllowedOrigin(origin, r.Host)`；`git diff main...HEAD -- internal/common/httpmw internal/gateway` 为空（策略文件零改动）；`proxy/security_test.go` 恶意 origin 403 且上游零调用 |
| 实例选择器与切换重水合·切换后数据全部来自所选实例 | `internal/webapp/shell.go:194-218`（OmitBootData 分支只注入 `__KANDEV_API_PORT`，不含 boot payload → SPA 走 app-state 回退）；`internal/backendapp/helpers.go:1513` shellPayloadFor；e2e flow2 断言 B 独有任务可见、A 任务 count=0、`GET /api/v1/app-state` 命中 |
| 实例选择器与切换重水合·单活动实例 | `lib/state/slices/instances/instances-slice.ts`（单 activeId）；单 WS 连接；标签页标题=实例名（`helpers.go` remoteInstanceShellPayload 设 TitlePrefix=instance.Name） |
| feature toggle 与桥接认证·默认关闭行为不变 | `internal/profiles/profiles.yaml`（prod/dev/e2e 全 `"false"`）；`internal/runtimeflags/registry.go`（features.multiInstance，RestartRequired）；flag off：无路由（`TestInstanceRoutesFlagOffGone` + e2e 断言 404）、无代理/relay（`instance_proxies.go:33`/`instance_relays.go:45` 早退）、shell 参数被忽略（`TestShellInstanceParamIgnoredWhenFlagOff` + e2e 断言含 `__KANDEV_BOOT_PAYLOAD__` 且无 `__KANDEV_API_PORT`）、registry service 不装配（`TestDefaultFlagKeepsRegistryInert`） |
| feature toggle 与桥接认证·开启认证后桥接仍可用 | `proxy/bridgeauth.go`（requireBridgeAuth 校验 HMAC 能力 cookie `kandev_bridge_<id>`，15min 滑动）；`backendapp/instance_routes.go:233-259` select 路由铸能力 cookie；`server.go` 上游注入 PAT；测试 `TestBridgeRequiresCapabilityWhenAuthOn`、`TestCapabilityMintVerifyRoundTrip`、`TestProxyForwardsApiAndInjectsCredential`、`TestSelectSetsInstanceAndBridgeCookies` |
| 宿主绑定能力降级·远端任务上的宿主动作 | `hooks/domains/instances/use-is-remote-instance.ts` + 9 个位点全部降级（file-actions-dropdown、editor-actions-dropdown、editors-menu、file-tree-editor-menu、port-forward-dialog-actions、port-forward-list 隧道开关、browser-panel 预览 iframe+inspect、use-open-session-folder、use-open-session-in-editor），统一 `instances:unavailableForRemote` 文案 + 禁用；任意 TCP 端口代理未实现（非目标） |

### 二、cross-instance-notifications spec（5 Requirement / 11 Scenario，全部实现）

| Requirement / Scenario | 证据 |
|---|---|
| 远端事件中继·看本机时收到远端事件 | `internal/instances/relay/relay.go`（每实例持久 WS 客户端 + `user.subscribe` 以远端身份订阅）；`backendapp/instance_relays.go` RelayManager 由注册表驱动（与面板选择无关）；e2e flow4：面板在 A、B 完成一轮 → 模块条目 + 双角标出现 |
| 远端事件中继·断线后补齐 | `relay.go:209-254`（60s 对账 + since 游标 + 200/页 + 卡秒边界推进 + "earlier events not synced" 日志）；`TestReconcileRecoversMissedWindow`、`TestReconcileStopsAtStuckSecondBoundary` |
| 远端事件中继·单实例故障不拖累其他 | relay 按实例独立 goroutine；`TestUnreachableInstanceDegradedAndIsolated`；e2e flow7：B 死后 A 建任务/看板正常 |
| 实例身份幂等投递·同一 occurrence 重放只投一次 | `store/sqlite/store.go:72-86`（`UNIQUE(instance_id,event,occurrence_id)`）+ `Record` ON CONFLICT DO NOTHING 仅新插入才 `BroadcastToUser`；前端 slice 按 id upsert；`TestRelayDedupAcrossLiveAndReconcile` |
| 实例身份幂等投递·parked 延迟语义保持 | 中继只消费已 dispatch 的语义 occurrence（远端 `notifications/service/service.go:254-262` recordOccurrence 在 dispatch 边界落库；WS 帧同边界）；`TestParkedDeferralInherited` |
| 实例身份幂等投递·不同实例 ID 碰撞不混淆 | `relay.go:38-41` 本地 ID = SHA1 UUID over `[instanceID,event,occurrenceID]`；唯一键含 instance_id |
| 侧边栏通知模块·未读角标随事件增长 | `notifications-section.tsx`（模块总角标 + 按实例角标）+ `selectors.ts`（快照缓存）；e2e flow4/6 断言 +1 与清零 |
| 侧边栏通知模块·点击条目跳转 | `notifications-section.tsx:172-177` `switchInstance(instanceId, linkToTask(taskId))`；e2e flow5 断言 URL `/t/:id?kandev_instance=:id` |
| 侧边栏通知模块·手机端可达 | `app-nav-sheet.tsx`：picker 在菜单滚动区外（workspace picker 同层）+ NotificationsSection 挂手机导航；`app-nav-sheet.test.tsx` 两个 describe 覆盖 |
| 提醒方式限定·远端事件不弹打扰 | `lib/ws/handlers/remote-notification.ts` 只写新 slice；e2e flow4 断言 `toast-message` count=0 |
| 提醒方式限定·本地通知行为不变 | diff 未触碰 `lib/ws/handlers/notifications.ts`、sonner、sound 相关任何文件（`--name-only` 验证为空） |
| 读取状态与留存·刷新后已读保留 | `is_read/dismissed` 列 + `/api/v1/remote-notifications/:id/read\|dismiss\|read-all`（`backendapp/remote_notification_routes.go`）；e2e flow6 reload 后角标保持为零 |
| 读取状态与留存·过期自动清理 | 本地侧 `RelayManager.startRetention`（1h ticker + maintenance guard，7 天窗口，`main.go:1198` 装配）；远端侧 store 构造启动清扫 + dispatch/list 节流清扫；`TestOccurrenceRetentionSweep`、`TestOccurrenceLifecycle` |

### 三、design.md 关键决策 D1–D12 与用户三决策

- **D1/D11** 每实例端口（`net.Listen("tcp", ":"+port)` 全接口、持久化重绑、被占回落 `:0`）✓；**D2** shell 注入端口 + 省略 payload（query 优先于 cookie，`webapp/handler.go:28-36`）✓；**D3** 本地 REST select + 整页重载 + `/t/:id?kandev_instance=` 深链 ✓；**D4** 零 body URL 重写（`modifyUpstreamResponse` 只删头不重写）✓；**D5** 帧事件名=action、`GET /api/v1/notifications/occurrences`（≤200，per-user）、版本下限探测 ✓；**D6** 去重键 ✓；**D7** per-owner + BroadcastToUser(owner) + 桥接守卫 ✓；**D8** Start/Stop + WaitGroup + goleak + timer/select 退避（1s→30s cap，无 time.Sleep）✓；**D9** 前端只加不改 ✓；**D10** flag 默认关 + 四层 disabled 测试 ✓；**D12** 提醒限定 ✓。
- **用户三决策**：v1 全量代理直接操作远端任务 ✓；无凭据兼容 + PAT 演进（可选凭据、secret store、文档安全前提节）✓；远端通知仅侧边栏模块 + 角标、无 toast/原生通知/声音、本地链路不动 ✓。
- **proposal 已知限制**：会话端口预览/port-proxy、桌面特权动作降级为不可用说明（上表 9 位点 + `docs/public/multi-instance.md`「Host-bound actions」节）✓。

### 四、反向抽查（Out of Scope）

166 个 diff 文件全部映射到计划内功能面或正常配套（测试、i18n 七语言 + pseudo、docs 导航互链、persistence 纪律 requiredstores/storeconformance/v0.93.0 manifest、e2e fixture）。四处结构性扩展（`lib/config.ts` 双 base、`__KANDEV_INSTANCE_ID` 注入、remote_notification handler 的 activeId==='local' 门控、`use-local-feature-flag` 本地 flag 缓存）均为 ledger 记录在案的 controller 裁决，用于解决「联邦端点不在代理后面/远端视图被远端 flag 困住」的设计缝隙，属实现必要而非新功能面。

### 边界观察（非漂移）

1. proposal Impact 写「e2e 以第二个后端实例模拟远端（auth 关闭 + 开启两档）」，实际 e2e 仅覆盖 auth 关闭档（`e2e/tests/multi-instance/multi-instance.spec.ts` 两个 test）；auth-on 桥接行为由 Go 单测覆盖（bridgeauth/security/server 测试）。属测试计划缩水，非 Scenario 缺实现。
2. 健康探测不带凭据（auth-on 远端报 401 记为 Reachable=false）——ledger Task 2「Parked (deferred minor)」已记录裁决，spec 未覆盖 auth-on 健康场景。
3. 代理入口本身不做版本门禁；版本下限由健康探测给出显式 unsupported 错误（design D5 原文机制），spec 的 Known Failure Modes 表提及「代理入口给版本不匹配错误」但无对应 Requirement/Scenario，unsupported 实例在 picker/设置页有琥珀色降级提示。不构成 Requirement 级漂移。
4. tasks.md Task 7 复选框未勾选但代码完备（instances-settings.tsx/instance-form/instance-list-row/instance-health-banner/instances-api.ts/settings 路由与 discovery/七语言 catalog/tests），ledger/index.md:31 记录 Task 7 完成（commit `d9621f2dc`），认定为记录遗漏，非未实现。
