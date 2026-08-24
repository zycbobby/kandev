---
name: openspec-plan
description: >
  Use when a proposal exists under `openspec/changes/<change-id>/` and
  you need to produce an OpenSpec-native implementation plan:
  `design.md` plus `tasks.md`. Embeds Superpowers planning quality
  without creating extra `plan.md`.
---

# OpenSpec Plan

目标：保留 OpenSpec 的 plan 阶段语义，只产出 `design.md` 和 `tasks.md`。

## 工作哲学：先试通，再拆任务

plan 阶段不是把 proposal 机械拆成待办，而是把未知路径验证成可执行路线。人类做计划前会先确认：
这条路是否通、是否值得走、边界是否足够窄、验收方式是否真的能证明成功。

规划前必须先做四件事：
- 跑最小闭环：能安全试通时，先跑一条成功路径或最小 demo，拿到真实输出
- 确认未知：列出所有会影响设计、任务拆分或验收的未知，并在定稿前解决、拒绝或标成硬 blocker
- 收紧边界：把本 change 的目标、non-goals、依赖边界和失败路径写清楚，不把相邻问题吞进来
- 记录事实：把调研输入/输出、命令、响应、失败模式、限制和验收启发写入 `design.md` 的 `## Research Log` 章节

## 执行流程

### Step 1：确认 change 与 schema

读取：

```bash
cat openspec/changes/<change-id>/.openspec.yaml
cat openspec/changes/<change-id>/proposal.md
cat openspec/changes/<change-id>/specs/*/spec.md 2>/dev/null
```

必须确认：
- 当前 change 的 schema 是 `superpowers-bridge`

### Step 1.5：选择规划引擎（内联 / superpowers:writing-plans）

规划引擎指本阶段用什么方式产出 `design.md` / `tasks.md`，只服务于 `openspec-plan` 本身，与 apply 阶段如何写代码无关。

在生成 `design.md` / `tasks.md` 前，必须向用户显式展示两个选项，并等待用户选择：

1. **内联**：由本 skill 直接用高推理强度推进规划。适用于默认路径、简单 change、或不需要专门 planning skill 的场景。
2. **`superpowers:writing-plans`**：调用该 skill 做方案权衡、任务拆分、风险识别和 Verification Strategy 规划；
   结论**只写入** `design.md` / `tasks.md`，不创建 `plan.md`，也不写 `docs/superpowers/plans/`。

执行规则：
- 如果用户已经显式指定规划引擎，按指定项执行。
- 如果用户选择 **内联**，使用高思考强度处理方案权衡、任务边界、依赖准出、Verification Strategy 与风险边界。
- 如果用户选择 **`superpowers:writing-plans`**，借用其规划纪律，但仍遵守 OpenSpec-native artifact 约束。
- **无人值守回退**：CI / headless / 无对话通道 / 用户无法回答时，自动使用内联；回退不报错、不阻塞，也不要求用户安装任何东西。

**无论选择内联还是 `superpowers:writing-plans`，以下 plan 质量门都适用**（不是开始实现）。当 change 涉及复杂既有行为、失败路径、日志 / transcript、外部 CLI 或运行时状态时，必须先做必要的 bounded debugging / probe，再定稿任务：

- 读取相关实现、错误输出、日志或状态文件，确认当前行为，而不是凭印象拆任务
- 能安全复现时，先用最小命令或测试复现 / 试通关键路径
- 若面对具体 bug、失败或异常行为，按 `superpowers:systematic-debugging` 的纪律先定位根因，再把调试结论反哺到 `design.md` / `tasks.md`
- 无法安全调试或复现时，在 `design.md` / `tasks.md` 记录 blocker、未验证假设与后续验证入口

这些动作服务于 plan 质量门，不是开始实现；不要写 `ledger/`，不要勾选 task，也不要创建额外 planning artifact。

### Step 1.6：前置调研 / 手动试通（bounded research / manual probe）

plan 阶段的调研不是附加说明，而是定稿任务前的准出门禁：先确认路线可行、关键未知已处理、边界不外溢、验收能证明成功。

在生成 `design.md` / `tasks.md` 前，先判断计划是否依赖代码库外事实。触发条件包括但不限于：

- 第三方 HTTP / RPC API、请求/响应契约、认证、分页、限流、错误码
- SDK / API 版本行为、平台文档、外部服务限制
- 会在实现或验证中调用的**外部 CLI**、命令输出、退出码、配置探测
- 其他 agent、编排器、模型、工具运行时的 **agent/runtime 响应**、transcript、日志或状态文件
- 任何不在当前代码库内、但会影响设计、任务拆分或验收标准的知识
- 当调研会占用大量上下文（例如第三方接口、外部 CLI、外部服务、agent/runtime 响应或长日志/transcript）时，可优先派发一个**专门 subagent** 承接 bounded research / manual probe；subagent 只回传本地事实、关键输出、失败模式、限制条件和验收建议，主 agent 负责把结论写入 `design.md` / `tasks.md`

若凭据、网络、沙箱、成本、速率限制和副作用边界都安全可控，必须先做 bounded research / manual probe：用最小、可复现、低风险的方式**真实跑通**关键路径（实际执行 demo / 最小调用，而不是仅凭文档推断），并在本地记录完整事实或必要摘要。

若无法安全试通，不要冒险调用；必须在 `design.md` / `tasks.md` 中记录 blocker 与未验证假设，并把后续可执行的验证入口设计出来。

记录规则：
- probe 结果只写入 `design.md` 的 `## Research Log` 章节，使用固定字段（未知描述 / 验证方式 / 结论 / 证据摘要）；
  该结论同时用于支撑 Verification Strategy、E2E 测试图和验收清单
- 记录请求/响应形状、CLI 命令和退出码、agent/runtime 响应摘要、失败模式、限制条件或未验证假设
- `## Research Log` 章节可选：未涉及仓库外知识、无需验证假设的简单 change 可以省略
- 不创建额外 artifact：不产 `research.md`、`debug.md`、`plan.md`，也不写 `docs/superpowers/plans/`

**依赖准出门禁（定稿 `design.md` / `tasks.md` 前必须满足）**

逐一列出计划涉及的**所有潜在依赖**（第三方 API、SDK、外部服务、外部 CLI、agent/runtime 行为、仓库外知识等）。对每一条，必须落到以下两种终态之一，**禁止停留在"我猜它应该能…"**：

1. **明确调研结论**：真实跑通（不止读文档），拿到具体 input/output——请求/响应形状、CLI 命令与退出码、版本、签名、错误形态——并写入 `design.md` 的 `## Research Log` 章节。
2. **明确拒绝理由**：决定不依赖它或换方案，并把拒绝理由（不可达、成本、副作用、不可控、license 等）同样记入 `## Research Log`，据此调整计划。

只记录"未验证假设"然后继续定稿**不满足**门禁：此时要么补调研拿到真实 I/O，要么给出明确拒绝理由并改计划。无法安全试通的依赖，按终态 ② 处理（给出拒绝理由或改设计绕开），或作为**硬 blocker** 明确阻塞定稿——不得作为已接受的假设悄悄进入验收。

### Step 2：生成 `design.md`

目标：
- 为实现阶段定义 HOW
- 不创建 `plan.md`
- 如变更简单，也保留精简版 `design.md`

若 Step 1.5 选用 `superpowers:writing-plans`，则按该 skill 的分析纪律推进，但**只把结果写入**
`design.md` 与 `tasks.md`；若选内联基线，则由本 skill 直接规划。

**可选增强：`grill-with-docs`**

当 change 较复杂、或其计划涉及既有领域术语 / spec / design 决策冲突时，可以选择性地嵌入
`grill-with-docs`，对照既有文档拷问计划、收敛术语。约束：

- 这是**可选**分支，不是默认；简单 change 选内联基线即可
- 澄清结论**只写入** `design.md` / `tasks.md`
- **不创建额外文件**：不产 `plan.md`，也不产 grill 专属文档

### Step 3：E2E 测试设计优先

在拆实现任务前，先设计：
- 核心用户路径
- 关键状态节点
- 验证方式

设计验证方式前，先判断本次变更的**测试对象类型**，因为可观测手段完全不同：

- **测试对象是具体程序代码**（API、CLI、UI 等）：相对容易设计，优先借助 API 调用、浏览器插件或脚本驱动核心路径，
  用日志、返回值、coverage 或界面状态做断言。
- **测试对象是 agent 本身或某个 skill**：不能只看代码，必须把以下三件事设计进 Verification Strategy：
  1. **沙箱创建**：用什么方式拉起一个隔离的 agent/skill 运行环境（worktree、临时项目目录、独立会话等），避免污染真实状态。
  2. **沙箱观测能力**：怎么拿到 agent/skill 运行期间的事实——transcript、日志、产物文件、状态文件——而不是只凭对话摘要判断成功。
  3. **agent 启动配置传递**：复现待验证场景需要哪些上下文、参数或配置，以及这些配置如何传入沙箱（环境变量、启动参数、初始 prompt 等）。

### Step 4：生成 `tasks.md`

必须包含：

1. `## Verification Strategy`
2. `## E2E 测试图`
3. `## 实现清单`
4. `## 验证清单`

**验证类型选择偏好**：`## Verification Strategy` 的验证类型应**优先选 `e2e-script` 或 `integration-test`**；
`unit-test` 仅在确无更高层验证手段时退而求其次，且**不得作为唯一验收依据**。TDD / 单测属于实现过程手段，
最终验收以选定的验证入口（优先 e2e / 集成）通过为准。

每条 task 必须：
- 使用 `- [ ]`
- 有明确文件路径
- 有清晰边界
- 有准出标准

### Step 5：为 apply 阶段预埋状态恢复要求

在 `tasks.md` 中明确：
- apply 阶段按 task 顺序推进实现
- 每条 task 都要有清晰边界、文件路径与准出标准
- 完成后再进入 verify / archive

### Step 6：校验

```bash
openspec validate <change-id> --strict --no-interactive
```

### Step 7：交接

告知用户：
- `design.md` 已生成
- `tasks.md` 已生成
- 下一步进入 `openspec-apply-change`
