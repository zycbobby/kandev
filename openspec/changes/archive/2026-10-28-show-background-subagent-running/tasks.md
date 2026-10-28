# show-background-subagent-running — Tasks

> **For agentic workers:** REQUIRED SKILL: 用 `openspec-apply-change` 按任务逐条推进（含 per-task ledger 与勾选）。步骤用 `- [ ]` 跟踪。断点恢复：重读本文件勾选状态与 `ledger/`，从首个未完成任务继续；不得跳步合并提交。
> Spec: `openspec/changes/show-background-subagent-running/specs/subagent-card-status/spec.md`
> Design: `openspec/changes/show-background-subagent-running/design.md`（接口签名、决策 D1–D4、i18n 值表、验证命令均以 design.md 为准）

**Goal:** 后台异步子代理卡片在 turn 结束后仍渲染「后台运行中」（spinner + 显式标签），不显示完成勾；折叠 turn 组头保持运行指示。

**Architecture:** 提取纯状态模块 `subagent-status.ts`（新谓词 `isSubagentBackgroundRunning` + 显示状态解析 `resolveSubagentDisplayStatus`），组件图标消费解析结果；turn-group 聚合判定补同一谓词。纯前端，无后端改动。

**Tech Stack:** React 19 + TypeScript + vitest + @testing-library/jsdom + i18next。

## Global Constraints

- 仅改 `apps/web`；不得触碰 `apps/backend` 任何文件（proposal Impact 钉死）。
- 新文案只经 `t()`；key `task:statusBackgroundRunning` 须进 en/pseudo/pt-pt/zh-cn/zh-hk/zh-tw/ja/ko；zh-hk、zh-tw 只能用 `pnpm run i18n:zh-hant` 生成；pseudo 用 `pnpm run i18n:pseudo` 再生成；值不得含 em dash（en 值：`Running in background`）。
- 本机 shell 禁用 `cd`（gvm 报错）：一律 `pnpm -C apps/web …` / `pnpm -C apps --filter @kandev/web lint`。
- 本机全量测试基线必红：vitest 只跑本文件列出的聚焦文件，禁止 all-worker 覆盖参数。
- Conventional Commits，header ≤100 字符，message 尾行 `Co-Authored-By: Claude Code <noreply@anthropic.com>`；若 prettier 钩子改写了暂存文件，重新暂存并新建提交（不 `--amend`）。
- `i18n:check` 需 Node 24；若当前 node 版本不符，先切换到机器上的 Node 24 再跑。

## Review Focus（本计划外最可能咬人的输入类，均已有归属测试）

1. `is_async` payload 后来出现终态 agent 状态 → 必须正常结算为勾，不得卡在后台态 → Task 2 纯解析用例。
2. `metadata.status` 为 `error`/`failed` 而 payload 仍 `async_launched` → 后台态优先（spec SHALL NOT 渲染失败叉）→ Task 2 纯解析用例。
3. turn 活跃期间的后台卡 → 必须显示后台标签而非通用 Working → Task 3 改写 `:328` 用例。
4. 只含终态子代理的 turn 组 → 不得出现运行指示（不过度提示）→ Task 4 守卫用例。
5. 无 `subagent_task` payload 的降级 agent 消息 → 不崩溃、不进后台态（走终态兜底）→ Task 2 纯解析用例。

---

## Verification Strategy

**integration-test**（组件级 vitest，渲染真实组件 + 真实 en catalog），辅以纯函数 `*.test.ts`。选择理由见 design.md「Verification Strategy」：变更是已固定 metadata 形状的纯显示函数，无后端路径；Playwright 需要后端夹具（out of scope）。最终验收以以下命令全部真实通过为准：

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

## E2E 测试图

用户路径 → 覆盖用例（详见 design.md「E2E 测试图」）：派发后台卡（turn 活跃）→ `:328` 改写；turn 结束后后台态+折叠+meta chip → Task 3 新用例；Cursor `is_async` 形状 → Task 3 新用例；折叠组头运行指示 → Task 4 用例；无信号永久停留（以「无翻转路径」纯谓词测试 + spec 边界记录代替）；同步回归护栏 → 既有用例不动须保持绿。

---

## 实现清单

### Task 1: 提取纯状态模块 `subagent-status.ts`（行为不变重构）

**Files:**
- Create: `apps/web/components/task/chat/messages/subagent-status.ts`
- Create: `apps/web/components/task/chat/messages/subagent-status.test.ts`
- Modify: `apps/web/components/task/chat/messages/tool-subagent-message.tsx:37-105`（删除被搬走的 helper 块）
- Modify: `apps/web/components/task/chat/messages/tool-subagent-message.test.tsx:5,210-266`（移除搬走的表与 describe、改 import）
- Modify: `apps/web/components/task/chat/messages/turn-group-message.tsx:12`（import 路径）

**Interfaces:**
- Produces（供 Task 2/3/4 与 turn-group 消费）: `isSubagentEffectivelyActive(metadata: ToolCallMetadata | undefined, isContainingTurnActive: boolean): boolean`（语义不变）、`resolveSubagentTerminalStatus(metadataStatus: ToolCallMetadata["status"] | undefined, payloadStatus: string | undefined): "complete" | "error" | "cancelled"`、`type SubagentTerminalStatus`。

- [x] **Step 1: 写 `subagent-status.test.ts`（搬移既有表驱动用例）**

把 `tool-subagent-message.test.tsx` 的 `EFFECTIVELY_ACTIVE_CASES` 表与 `describe("isSubagentEffectivelyActive")` 整体迁入新 `.test.ts`，用直接构造 metadata 的 builder 替代 Message factory（纯 `.ts`，无 JSX）：

```ts
import { describe, expect, it } from "vitest";
import type { ToolCallMetadata } from "@/components/task/chat/types";
import { isSubagentEffectivelyActive } from "./subagent-status";

function subagentMetadata(
  status: ToolCallMetadata["status"] | undefined,
  payloadStatus: string | undefined,
): ToolCallMetadata {
  const metadata: ToolCallMetadata = {
    status,
    normalized: { kind: "subagent_task", subagent_task: { status: payloadStatus } },
  };
  if (status === undefined) delete metadata.status;
  return metadata;
}
// EFFECTIVELY_ACTIVE_CASES 表原样迁入；it.each 断言改为
// isSubagentEffectivelyActive(subagentMetadata(metadataStatus, payloadStatus), isContainingTurnActive)
```

- [x] **Step 2: 运行确认失败（模块不存在）**

Run: `pnpm -C apps/web exec vitest run components/task/chat/messages/subagent-status.test.ts`
Expected: FAIL（`Cannot find module .../subagent-status`）

- [x] **Step 3: 创建 `subagent-status.ts`，组件与消费者改 import**

把 `tool-subagent-message.tsx:37-105` 的全部状态 helper 原样搬入新模块并 export（`isSubagentPayloadTerminal`/`isSubagentPayloadLive`/两个 Set 保持模块内私有可不导出）；组件删除该块并 `import { isSubagentEffectivelyActive, resolveSubagentTerminalStatus } from "./subagent-status"`；`turn-group-message.tsx:12` 与组件测试 `:5` 的 import 同步改源。**不改任何逻辑**。

- [x] **Step 4: 聚焦测试通过**

Run: `pnpm -C apps/web exec vitest run components/task/chat/messages/subagent-status.test.ts components/task/chat/messages/tool-subagent-message.test.tsx components/task/chat/messages/turn-group-message.test.tsx`
Expected: PASS（行为不变，全部既有用例绿）

- [x] **Step 5: 提交**

```bash
git add apps/web/components/task/chat/messages/subagent-status.ts apps/web/components/task/chat/messages/subagent-status.test.ts apps/web/components/task/chat/messages/tool-subagent-message.tsx apps/web/components/task/chat/messages/tool-subagent-message.test.tsx apps/web/components/task/chat/messages/turn-group-message.tsx
git commit -m "refactor: extract subagent status helpers into a pure module"
```

### Task 2: 新谓词与显示状态解析（纯函数 TDD）

**Files:**
- Modify: `apps/web/components/task/chat/messages/subagent-status.ts`
- Test: `apps/web/components/task/chat/messages/subagent-status.test.ts`

**Interfaces:**
- Consumes: Task 1 的 `isSubagentPayloadTerminal`/`isSubagentEffectivelyActive`/`resolveSubagentTerminalStatus`。
- Produces（Task 3/4 消费，签名见 design.md）: `isSubagentBackgroundRunning(payload: SubagentTaskPayload | undefined): boolean`；`type SubagentDisplayStatus = { kind: "background" } | { kind: "working" } | { kind: "terminal"; terminal: "complete" | "error" | "cancelled" }`；`resolveSubagentDisplayStatus(metadata: ToolCallMetadata | undefined, isContainingTurnActive: boolean): SubagentDisplayStatus`。

- [x] **Step 1: 写失败测试（新 describe，覆盖 Review Focus 1/2/5）**

```ts
describe("isSubagentBackgroundRunning", () => {
  it.each([
    { name: "async_launched", payload: { status: "async_launched" }, expected: true },
    { name: "is_async without status (Cursor)", payload: { is_async: true }, expected: true },
    { name: "async_launched regardless of is_async flag", payload: { status: "async_launched", is_async: false }, expected: true },
    { name: "is_async with terminal completed settles", payload: { is_async: true, status: "completed" }, expected: false },
    { name: "is_async with terminal failed settles", payload: { is_async: true, status: "failed" }, expected: false },
    { name: "sync started is not background", payload: { status: "started" }, expected: false },
    { name: "undefined payload is not background", payload: undefined, expected: false },
  ])("$name", ({ payload, expected }) => {
    expect(isSubagentBackgroundRunning(payload)).toBe(expected);
  });
});

describe("resolveSubagentDisplayStatus", () => {
  it.each([
    { name: "async + tool complete + turn inactive → background", metadataStatus: "complete", payload: { status: "async_launched" }, turnActive: false, expected: { kind: "background" } },
    { name: "async + turn active → background (统一标签)", metadataStatus: "complete", payload: { status: "async_launched" }, turnActive: true, expected: { kind: "background" } },
    { name: "is_async Cursor shape + turn inactive → background", metadataStatus: "complete", payload: { is_async: true }, turnActive: false, expected: { kind: "background" } },
    { name: "tool error while payload async_launched → background 优先", metadataStatus: "error", payload: { status: "async_launched" }, turnActive: false, expected: { kind: "background" } },
    { name: "is_async + terminal completed → terminal complete", metadataStatus: "complete", payload: { is_async: true, status: "completed" }, turnActive: false, expected: { kind: "terminal", terminal: "complete" } },
    { name: "sync in active turn → working", metadataStatus: "in_progress", payload: { status: "started" }, turnActive: true, expected: { kind: "working" } },
    { name: "settled sync → terminal complete", metadataStatus: "complete", payload: { status: "completed" }, turnActive: false, expected: { kind: "terminal", terminal: "complete" } },
    { name: "terminal failed payload → terminal error", metadataStatus: "complete", payload: { status: "failed" }, turnActive: true, expected: { kind: "terminal", terminal: "error" } },
    { name: "undefined metadata → terminal complete 兜底不崩溃", metadataStatus: undefined, payload: undefined, turnActive: false, expected: { kind: "terminal", terminal: "complete" } },
  ])("$name", ({ metadataStatus, payload, turnActive, expected }) => {
    const metadata = payload === undefined ? undefined : subagentMetadataWith(metadataStatus, payload);
    expect(resolveSubagentDisplayStatus(metadata, turnActive)).toEqual(expected);
  });
});
```

`subagentMetadataWith`：Task 1 builder 的扩展版，接受完整 `SubagentTaskPayload`（status 与 is_async 都落进 `normalized.subagent_task`）。

- [x] **Step 2: 运行确认失败**

Run: `pnpm -C apps/web exec vitest run components/task/chat/messages/subagent-status.test.ts`
Expected: FAIL（`isSubagentBackgroundRunning`/`resolveSubagentDisplayStatus` 未导出）

- [x] **Step 3: 实现两个导出**

`isSubagentBackgroundRunning`：`payload` 存在、`!isSubagentPayloadTerminal(payload.status)`、且 `payload.status === "async_launched" || payload.is_async === true`。
`resolveSubagentDisplayStatus`：顺序判定 background → working（`isSubagentEffectivelyActive`）→ terminal（`resolveSubagentTerminalStatus(metadata?.status, payload?.status)`）。

- [x] **Step 4: 运行确认通过**

Run: `pnpm -C apps/web exec vitest run components/task/chat/messages/subagent-status.test.ts`
Expected: PASS

- [x] **Step 5: 提交**

```bash
git add apps/web/components/task/chat/messages/subagent-status.ts apps/web/components/task/chat/messages/subagent-status.test.ts
git commit -m "feat: resolve background-running subagent display status"
```

### Task 3: 卡片接线 + i18n + 组件测试（用户可见变更）

**Files:**
- Modify: `apps/web/components/task/chat/messages/tool-subagent-message.tsx:131-146,174-184,186-220,265,358-427`
- Modify: `apps/web/components/task/chat/messages/tool-subagent-message.test.tsx`
- Modify: `apps/web/src/locales/{en,pt-pt,zh-cn,ja,ko}/task.json`（+ 脚本生成 pseudo/zh-hk/zh-tw）

**Interfaces:**
- Consumes: Task 2 的 `resolveSubagentDisplayStatus`/`SubagentDisplayStatus`。
- Produces: 用户可见行为；无下游代码接口。

- [x] **Step 1: 改写/新增组件测试（先红）**

`tool-subagent-message.test.tsx`：新增常量 `const BACKGROUND_RUNNING = "Running in background";`；factory `subagentMessage` 增 `isAsync?: boolean` 选项写入 `subagent_task.is_async`。

改写 `:328`（断言从 WORKING 改为后台标签）：
```
it("shows the background-running label after spawn completes while the nested Claude task is still async_launched", ...)
  renderSubagent(subagentMessage({ metadataStatus: COMPLETE, payloadStatus: "async_launched" }), { isContainingTurnActive: true });
  断言 getByText(BACKGROUND_RUNNING)、getByTitle(BACKGROUND_RUNNING)、getByRole("status", { name: "Loading" })；
  断言 queryByText(WORKING) 为 null。
```

新增两用例：
```
it("keeps the background-running state after the containing turn ends", ...)
  renderSubagent(subagentMessage({ metadataStatus: COMPLETE, payloadStatus: "async_launched" }), { isContainingTurnActive: false });
  断言 getByText(BACKGROUND_RUNNING)、getByRole("status", { name: "Loading" })；
  断言 queryByTitle(COMPLETED) 为 null（无完成勾）；
  断言 getByTestId("subagent-meta-background") 存在（meta 行可见，D2）。

it("shows the background-running state for an is_async payload without status", ...)
  renderSubagent(subagentMessage({ metadataStatus: COMPLETE, payloadStatus: undefined, isAsync: true }), { isContainingTurnActive: false });
  断言 getByText(BACKGROUND_RUNNING)；断言 queryByTitle(COMPLETED) 为 null。
```

既有 `:297`（Codex stale 失活）与其余用例**不动**（回归护栏）。

- [x] **Step 2: 运行确认失败**

Run: `pnpm -C apps/web exec vitest run components/task/chat/messages/tool-subagent-message.test.tsx`
Expected: FAIL（新用例找不到 BACKGROUND_RUNNING 文本）

- [x] **Step 3: 接线组件 + en key**

`tool-subagent-message.tsx`：`deriveSubagentDisplay` 增返回 `display = resolveSubagentDisplayStatus(metadata, isContainingTurnActive)`；`SubagentStatusIcon` props 改为 `{ display: SubagentDisplayStatus }`，`background` 分支镜像 working 分支结构（`GridSpinner` + 斜体可见文本 `t("task:statusBackgroundRunning")` + 同值 `title` 的 span）；`working`/`terminal` 分支行为不变；`SubagentHeader` 改传 `display`。布局行（:386 autoExpand、:417 meta 行）零改动。
`src/locales/en/task.json`：在 `statusWorking` 邻近加 `"statusBackgroundRunning": "Running in background"`。

- [x] **Step 4: 运行确认通过**

Run: `pnpm -C apps/web exec vitest run components/task/chat/messages/tool-subagent-message.test.tsx components/task/chat/messages/subagent-status.test.ts`
Expected: PASS

- [x] **Step 5: 补齐 locale catalog**

`pnpm -C apps/web run i18n:pseudo`（从 en 再生成 pseudo）；按 design.md 值表手写 pt-pt `A correr em segundo plano`、zh-cn `后台运行中`、ja `バックグラウンドで実行中`、ko `백그라운드 실행 중`（各 `src/locales/<locale>/task.json` 的 `statusWorking` 邻近）；`pnpm -C apps/web run i18n:zh-hant`（生成 zh-hk/zh-tw）。

- [x] **Step 6: i18n 门禁 + 静态检查**

Run: `pnpm -C apps/web run i18n:check && pnpm -C apps/web run i18n:ratchet && pnpm -C apps/web run typecheck`
Expected: 全部 PASS（无缺 key、无 em dash、无新硬编码）

- [x] **Step 7: 提交**

```bash
git add apps/web/components/task/chat/messages/tool-subagent-message.tsx apps/web/components/task/chat/messages/tool-subagent-message.test.tsx apps/web/src/locales
git commit -m "feat: show background-running state on async subagent cards"
```

### Task 4: turn-group 聚合运行指示

**Files:**
- Modify: `apps/web/components/task/chat/messages/turn-group-message.tsx:76-79`
- Test: `apps/web/components/task/chat/messages/turn-group-message.test.tsx`

**Interfaces:**
- Consumes: Task 2 的 `isSubagentBackgroundRunning`（与 Task 1 迁入的 `isSubagentEffectivelyActive` 同模块）。
- Produces: 组头运行指示覆盖 spec 新 Scenario「折叠 turn 组的头保持运行指示」（spec delta 已在 plan 阶段补好，本任务不改 spec）。

- [x] **Step 1: 写失败测试**

复用该测试文件既有的组/消息构造方式；若无构造 `subagent_task` metadata 的 helper，仿 `tool-subagent-message.test.tsx` 的 factory 加最小 builder。两用例：
```
it("keeps the group running indicator while a background subagent runs after the turn ends", ...)
  组内消息含 subagent_task（metadataStatus complete + payload async_launched），isContainingTurnActive=false
  断言组头运行指示存在（GridSpinner，按既有用例的断言方式，如 getByRole("status", { name: "Loading" })）。

it("does not show a running indicator for a group of settled subagents", ...)
  组内消息 payload 均为 completed，turn 失活
  断言无运行指示。
```

- [x] **Step 2: 运行确认失败**

Run: `pnpm -C apps/web exec vitest run components/task/chat/messages/turn-group-message.test.tsx`
Expected: FAIL（后台用例无运行指示）

- [x] **Step 3: 实现（1 行谓词）**

`turn-group-message.tsx:78` 的 subagent 分支改为：
`if (isSubagentEffectivelyActive(metadata, isContainingTurnActive) || isSubagentBackgroundRunning(metadata?.normalized?.subagent_task)) return true;`

- [x] **Step 4: 运行确认通过**

Run: `pnpm -C apps/web exec vitest run components/task/chat/messages/turn-group-message.test.tsx`
Expected: PASS（含守卫用例）

- [x] **Step 5: 提交**

```bash
git add apps/web/components/task/chat/messages/turn-group-message.tsx apps/web/components/task/chat/messages/turn-group-message.test.tsx
git commit -m "feat: keep turn-group running cue for background subagents"
```

### Task 5: 全量验证收尾

**Files:** 无新改动（只跑门禁 + 自检；发现问题时回到对应 Task 修复后重跑）。

- [x] **Step 1: 聚焦 vitest 全绿**

Run: `pnpm -C apps/web exec vitest run components/task/chat/messages/subagent-status.test.ts components/task/chat/messages/tool-subagent-message.test.tsx components/task/chat/messages/turn-group-message.test.tsx`
Expected: PASS

- [x] **Step 2: 静态与 i18n 门禁**

Run: `pnpm -C apps/web run typecheck && pnpm -C apps --filter @kandev/web lint && pnpm -C apps/web run i18n:check && pnpm -C apps/web run i18n:ratchet`
Expected: 全部 PASS

- [x] **Step 3: 范围自检**

Run: `git diff --name-only main...HEAD`（或对基线分支）
Expected: 变更文件仅为本计划 Files 列表所列 `apps/web` 路径；`apps/backend` 零文件。

- [x] **Step 4: OpenSpec 校验**

Run: `openspec validate show-background-subagent-running --strict --no-interactive`
Expected: valid

## 验证清单

| # | 验证项 | 命令 | 期望 |
|---|---|---|---|
| 1 | 纯谓词/解析 | 聚焦 vitest `subagent-status.test.ts` | PASS（含 Review Focus 1/2/5） |
| 2 | 卡片渲染 | 聚焦 vitest `tool-subagent-message.test.tsx` | PASS（async 失活用例 + 同步回归） |
| 3 | 组头指示 | 聚焦 vitest `turn-group-message.test.tsx` | PASS（含守卫用例） |
| 4 | 类型 | `pnpm -C apps/web run typecheck` | PASS |
| 5 | Lint | `pnpm -C apps --filter @kandev/web lint` | PASS |
| 6 | i18n | `pnpm -C apps/web run i18n:check && pnpm -C apps/web run i18n:ratchet` | PASS |
| 7 | 范围 | `git diff --name-only main...HEAD` | 仅 `apps/web` 计划内文件 |
| 8 | OpenSpec | `openspec validate show-background-subagent-running --strict --no-interactive` | valid |

**Apply 阶段状态恢复要求**：严格按 Task 1→5 顺序推进；每 Task 完成即勾选并按 openspec-apply-change 记录 ledger；中断后从首个未勾选 Task 的 Step 1 继续（重读本文件 + `ledger/`）；全部勾选后进入 verify（`openspec-verify-change`）再 archive，不得跳过 verify 直接 archive。
