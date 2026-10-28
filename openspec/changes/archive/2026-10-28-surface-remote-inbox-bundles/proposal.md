## Why

多实例联邦下，远端实例的 agent 调用 `ask_user_question_kandev` 阻塞等待回答时，这个问题只在远端面板可答：本机侧边栏虽有一条中继通知，但它不可回答（relay occurrence 只携带 title/body，无 questions/options），本机 Needs-you Inbox 也只列本机 workspace 的 bundle。操作者不看远端面板就会漏掉被阻塞的远端 agent——这正是 Inbox 要消灭的「问题长期无人回答」失败在跨实例场景的重演。本 change 让本机 Inbox 组合远端 answerable 状态：每个启用注册实例一页，就地回答、dismiss/snooze 转发回远端、标准路径跳转，远端降级时渲染降级态而非死答案框。

## What Changes

**远端 answerable 行进入本机 Inbox**
- From: 本机 Inbox 只列本机活动 workspace 的 answerable bundles；远端澄清只能以不可回答的通知形式出现在侧边栏通知模块。
- To: 本机 Inbox 在本机页之外，为每个「已注册且启用」的远端实例组合一页 answerable bundles（每实例一页，行携带实例与来源 workspace 身份），就地可答、可跳转。
- Reason: 让被阻塞的远端 agent 在操作者常驻的本机面板可见、可解除。
- Impact: 非 breaking；`features.needsYouInbox` 与 `features.multiInstance` 任一关闭时行为与现状完全一致。

**读路径：本机后端 fan-out 组合**
- From: Inbox 读路径是单实例、workspace 作用域的本地 DB 计算视图。
- To: 本机后端作为联邦客户端，按实例 fan-out 读取远端自身的 clarification-inbox 端点（PAT 可见的全部远端 workspace 聚合为一实例页，按 `created_at` 升序、默认 limit 50），远端行不写任何本地投影表。
- Reason: SPA 侧组合被三个硬事实卡住（见 Research Log 探测 2），后端直连是既有的信任面（relay/health 先例）。

**回答 / dismiss / snooze 转发**
- From: 回答与 sidecar 写只作用于本机实例。
- To: 远端行的回答（含拒绝）经本机后端转发到远端自身的 `POST /api/v1/clarification/:id/respond`，200/409 win-loss 语义原样保留；dismiss/snooze/restore 转发到远端自身 sidecar 端点，sidecar 状态留在远端，本地 sidecar 表不扩列。共享答案组件链获得一个 additive 的可选「提交目标」覆盖，任务会话与 Quick Chat 不传、行为不变。

**ADR 与规格修订**
- 修订 ADR `docs/decisions/2026-10-28-multi-instance-federation.md` Decision 3：远端澄清状态的 answerable 展示从「仅侧边栏通知模块」放宽到「通知行为不变的前提下，允许 Inbox 组合读」。
- 修订 `docs/specs/ui/requirements/needs-you-inbox.md` 与 system-design 01/02/03：workspace 措辞的远端豁免、远端行生命周期（收敛靠远端页 re-read）、降级态、计数 producer 集合的显式扩展。

## Capabilities

### New Capabilities

- `cross-instance-inbox`: 本机 Inbox 组合已注册启用远端实例的 answerable clarification bundles——实例为单位的组合读、就地回答转发、远端 sidecar 转发、角标计数、降级态与跳转，保持远端数据不落本地表。

### Modified Capabilities

（无：`multi-instance-management` 注册表不加字段（workspace 映射取实例整体聚合，不配置目标 workspace）；`cross-instance-notifications` 的通知投递行为不变，relay 的 `session.clarification_requested` occurrence 仅新增一个内部 re-read bump 消费者，不构成该 capability 的语义变更。）

## 方案与决策

以下决策在 Explore 证据上裁定，替代方案与拒绝理由记录于此供评审否决：

1. **读路径 = 后端 fan-out**（拒绝 SPA 侧经 proxy base 组合）：bridge capability cookie 只在 `POST /instances/:id/select` 时 mint，看本机时浏览器对远端 proxy port 无凭据（auth-on 全部 401）；`instanceAPI` DTO 刻意省略 proxy port；SPA 侧方案需要新增「为全部启用实例 mint」的面并暴露端口，扩大 bridge 信任面。
2. **workspace 映射 = 实例整体聚合**（拒绝 per-instance 配置目标 workspace）：远端不存在服务端可查的「默认 workspace」（active-workspace 是浏览器 cookie，proxy 向上游剥 Cookie；read 缺 `workspace_id` 即 400）。配置式目标一旦遗漏或配错，会静默隐藏被阻塞的远端 agent——正是本能力要消灭的失败。远端页 = PAT 身份可见的全部远端 workspace 的 answerable 聚合；行携带来源 workspace 身份；本机活动 workspace 切换不改变远端页。
3. **角标计数 = 远端行计入**：sidebar actionable count 是「操作者现在可动手的阻塞数」，远端可回答 bundle 是真实阻塞（failed-tab 不计数先例针对的是故障而非阻塞，不适用）。badge = 已加载源页行数之和（本机页 + 各已加载远端页）；boot 只 seed 本机页（不为 N 个远端阻塞页面加载），远端贡献随该实例页落地计入，沿用 failed-inbox 的 count-known 模式。
4. **v1 交互面 = 就地回答 + 标准跳转两者**：跳转复用 `switchInstance(instanceId, targetPath)`（通知模块与 e2e 先例）；就地回答是本能力存在的理由。
5. **远端行收敛 = re-read 即契约**（沿用本机 Inbox 原则）：远端页触发集 = Inbox 页挂载、标签页可见性恢复、有界周期 re-read、relay 收到该实例 `session.clarification_requested` occurrence 时的 bump；行移除与计数变化只由远端页响应落地，不做本地 splice。
6. **降级 = 实例 section 级**：远端不可达（结构化 502）、auth-on 未登记 PAT（健康探测已知限制）、版本不支持 → 该实例 section 渲染降级态（命名实例、可重试），本机页与其他实例页不受影响，绝不渲染可提交但必失败的答案框。

## Research Log

（承自本 change 前的 openspec-explore 探测，均已在当前代码上验证。）

### 探测 1：relay occurrence 能否做 Inbox 数据源
- **验证方式**：读 `apps/backend/internal/instances/relay/relay.go`。
- **结论（拒绝理由）**：`Occurrence` 只有 `{title, body, task_id, session_id}` 通知字段，无 questions/options/messages；Inbox 行集必须来自 bundle 读路径（AC .6）。relay 仅可作远端页 re-read 的 bump 触发。
- **证据摘要**：`relay.go:22-34`、`relay.go:185-187`（allowed 闭集含 `session.clarification_requested`）。

### 探测 2：SPA 侧组合可行性
- **验证方式**：读 `instance_routes.go`、`proxy/bridgeauth.go`、`proxy/server.go`、`switch-instance.ts`。
- **结论（blocker→拒绝）**：capability cookie 仅 select 时 mint（`instance_routes.go:253-257`）；DTO 刻意省略 proxy port（`instance_routes.go:50-52`）；`__KANDEV_API_PORT` 仅活动实例注入。后端 fan-out 绕开全部三点。
- **证据摘要**：`bridgeauth.go:60-77`；e2e `multi-instance.spec.ts:331-344`。

### 探测 3：Inbox 契约中须显式修订的假设
- **验证方式**：读 `needs-you-inbox.md`、design 01/02/03、`inbox_handlers.go`、`use-needs-you-inbox-controller.ts`、`use-clarification-group.ts`。
- **结论（试通证据）**：须修订 AC .5/.31/.38 的 workspace 措辞、AC .40 封闭的两个 count producer；`postClarification` 硬编码 `getBackendConfig().apiBaseUrl`（`use-clarification-group.ts:245-249`），就地回答需 additive 覆盖贯穿 `useClarificationGroup → overlay → panel section`（符合 AC .39 additive 规则）；optimistic 本地 message-store 更新对远端行安全 no-op；late-answer 回退必须对远端行抑制。
- **证据摘要**：`needs-you-inbox.md:167-178`；design-02 Control flow（本机 5 个 refresh trigger 全基于本地 WS 事件）。

### 探测 4：「远端默认 workspace」是否存在
- **验证方式**：读 `inbox_handlers.go:487-492`、boot_state cookie、`proxy/server.go` upstreamHeaders。
- **结论（拒绝理由）**：不可构造——read 缺 `workspace_id` 即 400；远端 active-workspace 是浏览器 cookie，proxy 剥 Cookie（`server.go:121`），服务端无从得知。真实选项仅「配置目标」vs「实例整体聚合」，取后者（见决策 2）。
- **证据摘要**：`inbox_handlers.go:490-492`；`server.go:116-121`。

### 探测 5：跳转 / 降级 / flag / e2e 前提
- **验证方式**：读 `switch-instance.ts`、ADR 2026-10-28、`runtimeflags/registry.go`、`e2e/tests/multi-instance/multi-instance.spec.ts`。
- **结论（试通证据）**：`switchInstance` 已是标准跳转路径（落 `/t/:id?kandev_instance=...`）；proxy 全方法转发 `/api/v1/**` + PAT 注入 + 结构化 502；两 flag 均在 registry（`registry.go:114`、`:361`）且 shipped profiles 默认关；双后端 e2e fixture（`fixtures/remote-backend.ts`）可直接扩展。
- **证据摘要**：ADR `2026-10-28-multi-instance-federation.md:15`（Decision 3 原文）；e2e flow 5/7。

## 后续叠加变更预留（决策交接）

后续叠加包 `needs-you-inbox-waiting-rows`（人工节点等待行 + 统一通知桥 + 本地聚合读 `GET /api/v1/needs-you-inbox`，REQ-UI-NEEDS-YOU-INBOX-002~004）将复用本 change 落定的组合机制。此处显式记录三个已裁定决策与读路径的扩展方式，供该包直接继承：

1. **读路径 = backend fan-out（终选，叠加包直接复用）**：本机后端作为联邦客户端直连远端实例（PAT 注入的 direct-dial），SPA 侧组合被 bridge capability cookie 只在 select 时 mint、`instanceAPI` DTO 省略 proxy port 两个事实拒绝。对叠加包的含义：远端 API 面的形状是「远端实例自身端点，经本机后端聚合」，不是浏览器直连 proxy。
2. **远端行计入 actionable count**：sidebar 角标 = 已加载源页行数之和（本机页 + 各已落地远端页）；boot 只 seed 本机页，远端贡献随该实例页落地计入（count-known 模式）。叠加包的等待行沿用同一计数语义。
3. **workspace 映射 = 实例整体聚合**：远端页 = PAT 身份可见的全部远端 workspace 聚合，行携带来源 workspace 身份，不做 per-instance 目标 workspace 配置（配置遗漏会静默隐藏被阻塞 agent，正是本能力要消灭的失败）。

**remoteinbox 读路径向多行种类的扩展（kind 判别）**：叠加包的本地聚合端点 `GET /api/v1/needs-you-inbox` 将成为远端 API 面。扩展方式：行增加 kind 判别字段（本 change 隐含 `clarification` 单一种类，叠加包增加 `manual_step_waiting` 等待行种类）；`remoteinbox` 客户端的 per-workspace 读从「仅 clarification-inbox 端点」切换为该聚合端点，聚合合并保留每行的 kind 与来源 workspace 身份；转发端点按 kind 路由（clarification 行继续走 respond 转发，等待行走各自的动作端点）。per-instance page、降级态、generation 陈旧丢弃、count-known 计数、`retainRemoteInboxInstances` 注册表收敛这些机制均与 kind 无关，原样复用。

## Impact

- **后端**：新增远端 inbox fan-out/转发面（组合读 + 回答/ sidecar 转发；形态如 clarification 或 instances 域的新路由，plan 阶段定夺）；复用 instances 注册表、PAT secret、`relay.fetch` 式直连；`internal/instances/AGENTS.md` 与根 CLAUDE.md 相关段落随实现更新。
- **前端**：`needs-you-inbox` slice/controller 增加实例感知源页（含 count-known 模式）；行组件加实例标识与跳转；共享答案组件链 additive 提交目标覆盖；新降级态组件。
- **规格/决策文档**：`docs/decisions/2026-10-28-multi-instance-federation.md`（Decision 3 修订）；`docs/specs/ui/requirements/needs-you-inbox.md` 与 `docs/specs/ui/system-design/needs-you-inbox-01/02/03.md`（workspace 措辞豁免、远端行生命周期、降级态、计数 producer 扩展、新增 copy 契约）。
- **flag**：不改注册表 identity；交互规则 = `features.needsYouInbox && features.multiInstance` 同时开才出现远端行，任一关完全惰性。
- **i18n**：全部新增 copy 走 `t()`，7 语言（en + pt-pt/zh-cn/zh-hk/zh-tw/ja/ko）+ pseudo，zh-hant 对经 `pnpm run i18n:zh-hant`；无 em dash。
- **e2e**：扩展 `apps/web/e2e/tests/multi-instance/`（复用 `fixtures/remote-backend.ts`）：远端 seed 澄清 bundle → 本机 Inbox 列出 → 就地回答 → 远端任务恢复且行移除；跳转流；kill 远端降级流；计数流。
- **不触碰**：本地 sidecar 表结构、本地 task/board 缓存、WS origin/CORS 策略、本地通知行为（本变更不改任何通知投递，toast/原生通知/声音维持现状；注意「远端安静、仅侧边栏展示」是当前已发布行为而非终态契约，统一通知方向见下方「后续叠加变更预留」）、current-turn 谓词与 resolution 语义。
