# show-background-subagent-running — Design

> Spec: `openspec/changes/show-background-subagent-running/specs/subagent-card-status/spec.md`
> Proposal: `openspec/changes/show-background-subagent-running/proposal.md`（含 Explore/Propose 阶段全部已确认事实）

## 目标与边界

前端（`apps/web`）把后台异步子代理卡片渲染为「后台运行中」态。判定只看 payload（`subagent_task`）：`status === "async_launched"`，或 `is_async === true` 且无终态 agent 状态。与 tool status（`metadata.status`）及所在 turn 活跃性解耦。无完成信号是已确认边界：卡片停留后台运行态，禁止伪造完成与用量。

**Non-goals**（proposal Impact 已钉死）：不改后端 terminality / schema / adapter 强制 `complete` / outputFile / usage；不用 `active_subagent_count` 归零翻转卡片。

## 文件结构与接口

```
apps/web/components/task/chat/messages/
├── subagent-status.ts          # 新建：纯状态解析（无 React、无 i18n）
├── subagent-status.test.ts     # 新建：纯谓词 + 显示状态解析的 *.test.ts 覆盖
├── tool-subagent-message.tsx   # 修改：移除被搬走的 helper，接线新解析
├── tool-subagent-message.test.tsx  # 修改：更新 :328 断言、增 async+turn 失活用例
└── turn-group-message.tsx      # 修改：hasRunningTool 增后台运行判定（1 行）
apps/web/src/locales/{en,pseudo,pt-pt,zh-cn,zh-hk,zh-tw,ja,ko}/task.json  # 新 key
```

### `subagent-status.ts`（新模块，从组件搬入 + 新增）

搬入（语义不变）：`LIVE_SUBAGENT_PAYLOAD_STATUSES`、`TERMINAL_SUBAGENT_PAYLOAD_STATUSES`、`isSubagentPayloadTerminal`、`isSubagentPayloadLive`、`SubagentTerminalStatus`、`normalizeSubagentTerminalStatus`、`resolveSubagentTerminalStatus`、`isSubagentEffectivelyActive`。

新增（本 change 的核心决策，全部纯函数）：

```ts
export function isSubagentBackgroundRunning(payload: SubagentTaskPayload | undefined): boolean;
// true ⟺ payload 存在、payload.status 非终态，且（status === "async_launched" || payload.is_async === true）

export type SubagentDisplayStatus =
  | { kind: "background" }                                              // spinner + 后台运行标签
  | { kind: "working" }                                                 // spinner + 既有 Working 标签（同步路径）
  | { kind: "terminal"; terminal: "complete" | "error" | "cancelled" }; // 既有终态图标

export function resolveSubagentDisplayStatus(
  metadata: ToolCallMetadata | undefined,
  isContainingTurnActive: boolean,
): SubagentDisplayStatus;
// 顺序即语义：background（不受 turn 门控，覆盖 active-turn 场景统一标签）→ working（isSubagentEffectivelyActive，语义不变）→ terminal（resolveSubagentTerminalStatus）
```

`isSubagentEffectivelyActive` **语义不变**：它继续驱动 `autoExpanded`、meta 行显隐、result summary 显隐（turn 域布局语义，见决策 D2）。变更不在谓词本身，而在图标消费的新解析层。外部消费者 `turn-group-message.tsx:78` 的 import 路径改为新模块。

### 组件接线（`tool-subagent-message.tsx`）

- `deriveSubagentDisplay` 增算 `display = resolveSubagentDisplayStatus(metadata, isContainingTurnActive)`，随返回值下传。
- `SubagentStatusIcon` props 从 `{ isActive, status, payloadStatus }` 改为 `{ display: SubagentDisplayStatus }`，三分支渲染：`background` → `GridSpinner` + 可见斜体文本 `t("task:statusBackgroundRunning")` + 同值 `title`（镜像既有 Working 分支结构）；`working` → 现状不变；`terminal` → 现状不变（IconCheck/IconX + 既有标签）。
- 布局行为零改动：`autoExpanded = isActive`（:386）、`!isActive && meta 行`（:417）维持原样。

### turn-group 聚合信号（`turn-group-message.tsx`）

`hasRunningTool`（:67-83）对 subagent 分支改为 `isSubagentEffectivelyActive(metadata, isContainingTurnActive) || isSubagentBackgroundRunning(metadata?.normalized?.subagent_task)`。已知且接受的副作用：组级 `autoExpanded = isGroupRunning || …`（:360）会让含后台子代理的 turn 组保持展开；组内卡片自身仍按卡片级规则折叠为紧凑行（11 卡 fan-out = 11 行折叠卡，可接受，见 D2'）。

## 关键决策

- **D1 判活谓词**（proposal 决策 1）：如上；`isSubagentPayloadTerminal` 守卫保证 `is_async + 终态` 正常结算（终态优先）。
- **D2 状态与布局解耦**（proposal 决策 2）：卡片级。turn 结束后 `isActive=false` → 折叠、meta 行（含 `subagent-meta-background` chip，testid 已存在）可见；活跃指示与标签由 `display.kind === "background"` 驱动。
- **D2' 组级扩展**（plan 阶段范围细化）：`hasRunningTool` 是 `isSubagentEffectivelyActive` 的唯一直接外部消费者；不纳入则折叠 turn 组头在上层复现同一 bug（组头显示空闲而组内后台卡在运行）。组保持展开的副作用见上，与卡片级 D2 不冲突（卡片仍折叠）。
- **D3 标签统一**（proposal 决策 3）：`background` 优先于 `working` 判定，active-turn 内的后台卡也显示后台标签（现有测试 :328 需同步改断言）。
- **D4 无完成信号**（proposal 决策 5）：spec 有专门 Scenario 钉死「计数归零不翻转」「无信号时永久停留」。

## i18n

新 key `task:statusBackgroundRunning`（单 key，可见文本与 title 同值；不需要 Working 那样的双 key）：

| locale | value |
|---|---|
| en | `Running in background` |
| pseudo | 由 `pnpm run i18n:pseudo` 从 en 生成 |
| pt-pt | `A correr em segundo plano` |
| zh-cn | `后台运行中` |
| zh-hk / zh-tw | `pnpm run i18n:zh-hant` 从 zh-cn 转换 |
| ja | `バックグラウンドで実行中` |
| ko | `백그라운드 실행 중` |

无 em dash、非复数、与英文不同值（不触发 verbatim 检查）。放置于各 catalog `statusWorking` 邻近。

## Research Log（plan 阶段 probe，补充 Propose 未知之外的实现级事实）

### 未知 1：状态 helper 的全部消费者（决定搬移是否会破坏 import）
- **验证方式**：`grep isSubagentEffectivelyActive|resolveSubagentTerminalStatus|deriveSubagentDisplay` 全 `apps/web`。
- **结论（试通证据）**：`isSubagentEffectivelyActive` 恰好两个外部引用：`turn-group-message.tsx:12,78`（`hasRunningTool`）与组件自身测试；`resolveSubagentTerminalStatus`/`deriveSubagentDisplay` 无外部引用。搬移只需同步这两处 import。
- **证据摘要**：grep 输出如上；`turn-group-message.tsx:354` `isGroupRunning = hasRunningTool(...)`，仅 feeding :121 spinner 与 :360 autoExpand。

### 未知 2：测试基建形状（决定 .test.ts 如何写、组件测试如何断言）
- **验证方式**：读 `tool-subagent-message.test.tsx` 头部与 factory、`subagent-meta-row.tsx`。
- **结论（试通证据）**：组件测试跑真实 en catalog（断言 `"Working..."` 等真实文案）；`subagentMessage` factory 已支持 `metadataStatus/payloadStatus` 等参数但不支持 `is_async`（测试任务需扩展）；背景 chip 有稳定 `data-testid="subagent-meta-background"`；纯函数用例现为 `it.each(EFFECTIVELY_ACTIVE_CASES)` 表驱动，可整体搬入新 `.test.ts`（用直接构造 `ToolCallMetadata` 的 builder 替代 Message factory，避免 JSX）。
- **证据摘要**：`tool-subagent-message.test.tsx:5,29-71,224-266`；`subagent-meta-row.tsx:22-37`。

### 未知 3：i18n 工具链与目录形状（决定 locale 步骤顺序）
- **验证方式**：读 `apps/web/package.json` scripts 与 `src/locales/` 列表。
- **结论（试通证据）**：`i18n:pseudo`（generate-pseudo-locale.mjs）从 en 再生成 pseudo；`i18n:zh-hant` 从 zh-cn 转换 zh-hk/zh-tw；`i18n:check` 六项子检查含 em-dash 与 nonjsx-copy。顺序：en → pseudo 再生成 → 4 个手写 locale → zh-hant 转换 → i18n:check。
- **证据摘要**：`apps/web/package.json:32-38`；`src/locales/` 含 `_verbatim.json, en, ja, ko, pseudo, pt-pt, zh-cn, zh-hk, zh-tw`。

### 未知 4：本机验证命令形态
- **验证方式**：项目记忆 + shell 环境约束。
- **结论（试通证据）**：本机 shell `cd` 不可用（gvm 报错中断），一律 `pnpm -C apps/web ...` / `pnpm -C apps --filter ...`；本机全量测试基线必红，门禁用聚焦 vitest 文件运行，禁止 all-worker 覆盖参数。
- **证据摘要**：会话记忆 `kandev-shell-cd-gvm-quirk`、`kandev-env-test-baseline`。

依赖准出：本 change 无仓库外依赖（无第三方 API/CLI/外部服务；agent 行为已由 Explore 阶段以代码级证据固定），全部依赖均为仓库内已验证事实。

## Verification Strategy

**验证类型：integration-test（组件级 vitest + @testing-library/jsdom，渲染真实组件与真实 en catalog）。**

理由：变更是「已固定的 message metadata 形状 → 显示」的纯前端函数；metadata 形状由后端既有规格钉死（Explore 已核实），无后端路径改动。Playwright E2E 需要 mock-agent 产出 async 帧的夹具（后端/夹具工作，明确 out of scope），故不以 e2e-script 作为验收入口；单测（`subagent-status.test.ts`）覆盖纯谓词，组件测试覆盖渲染与既有行为回归，二者共同构成验收。

验收命令（apply 阶段逐条执行，全部须真实通过）：

```bash
pnpm -C apps/web exec vitest run \
  components/task/chat/messages/subagent-status.test.ts \
  components/task/chat/messages/tool-subagent-message.test.tsx \
  components/task/chat/messages/turn-group-message.test.tsx
pnpm -C apps/web run typecheck
pnpm -C apps --filter @kandev/web lint
pnpm -C apps/web run i18n:check
pnpm -C apps/web run i18n:ratchet
openspec validate show-background-subagent-running --strict --no-interactive
```

注意：`i18n:check` 需 Node 24；本机聚焦 vitest 即门禁（勿跑全量、勿加 worker 覆盖）。

## E2E 测试图

核心用户路径（以组件测试覆盖映射）：

1. 控制器 turn 内派发后台子代理（tool status `complete` + payload `async_launched`，turn 活跃）→ 卡片显示 spinner + 「Running in background」→ `tool-subagent-message.test.tsx`（改 :328 断言）。
2. 父 turn 结束（`isContainingTurnActive=false`，session WAITING_FOR_INPUT）→ 卡片仍显示后台运行态、无完成勾、折叠为紧凑行、meta 行含 background chip → 同文件新增用例 + `subagent-status.test.ts` 纯解析用例。
3. Cursor 形状（`is_async:true`、无 status）→ 同样后台运行态 → 同上。
4. 折叠 turn 组头保持运行指示（`hasRunningTool`）→ `turn-group-message.test.tsx` 新增用例。
5. 长时运行后无信号 → 卡片永久停留后台运行态（无法用测试驱动「永远」，以「无翻转路径」的纯谓词测试 + spec 边界记录代替）。
6. 回归护栏：同步子代理（spinner → 终态勾/叉）、`is_async+终态` 正常结算、组内全终态无运行指示、缺 payload 不崩溃。

## 风险与回退

- 主要风险是「背景态误判为终态」回归（历史 bug 复活）：以 `subagent-status.test.ts` 的表驱动组合（async × turn 活跃/失活 × tool 状态）钉死。
- turn-group 组保持展开的副作用（D2'）若评审不接受，可单独回退 `turn-group-message.tsx` 的 1 行改动（卡片行为不受影响），spec scenario 随之移除。
- 回退面小：全部变更集中在 2 个新文件 + 4 个修改文件，无 schema/后端耦合。
