---
name: openspec-propose
description: >
  Use when starting a new feature or change in a project that uses
  OpenSpec. Ensures the change is created with
  `--schema superpowers-bridge`, then produces a Chinese `proposal.md`
  and capability-oriented spec deltas under
  `openspec/changes/<change-id>/`.
---

# OpenSpec Propose

目标：**保留 OpenSpec propose 入口**，并把 Superpowers brainstorming 作为可选 proposal 引擎嵌入进来。

**分流（与 post-apply 修复）**：

- **pre-apply** 的既有 spec 行为偏离 → 本 skill 的 **Step 2.4 bugfix triage**（仍产出 `proposal.md`，后续可 plan）
- **post-apply / archived** 上的快速修复 → **`openspec-bugfix` / `/opsx:bugfix`**，不要默认重走完整 plan
- **新能力 / 行为扩展** → 本 skill 的 feature proposal 流程

## 工作哲学：先调研，再定范围

propose 阶段不是先写愿望清单，而是先面对未知：这条路通不通、是否值得做、边界在哪里。任何 proposal
在成稿前都要先把关键未知显性化，并在安全、低成本、可逆的前提下做最小验证。

调研方式包括：
- 跑最小闭环：用最小 demo、命令、接口调用、现有代码路径或手工流程试通一条成功路径
- 枚举未知：列出会影响 Why / Scope / Impact / Capability 映射的未知，并尽量在 propose 阶段解决
- 收紧边界：明确本 change 解决什么、不解决什么，避免把所有相邻问题都纳入范围
- 沉淀事实：把调研结论、失败模式、未解决 blocker 和关键假设写入 `proposal.md` / spec delta，不创建额外 artifact

## 执行流程

### Step 1：确认 / 创建 change

如果当前还没有 change：

```bash
openspec new change <change-id> --schema superpowers-bridge
```

要求：
- `change-id` 用 kebab-case，建议动词开头
- 创建后检查 `openspec/changes/<change-id>/.openspec.yaml`
- 必须确认其中包含：`schema: superpowers-bridge`

如果当前已经在某个 change 中：
- 读取 `.openspec.yaml`
- 若不是 `schema: superpowers-bridge`，暂停并告知用户

### Step 2：读取上下文

```bash
openspec list
openspec list --specs
cat openspec/changes/<change-id>/proposal.md 2>/dev/null
cat openspec/changes/<change-id>/specs/*/spec.md 2>/dev/null
```

### Step 2.3：问题空间调研 / 最小可行验证

在选择 proposal 引擎或写 `proposal.md` 前，先做一次 bounded research：

1. **列出未知**：哪些事实会改变 proposal 的 Why、What Changes、Capabilities、Impact 或 non-goals？
2. **选择最小验证**：能否用一个低风险动作试通成功路径，例如读取现有实现、运行最小命令、调用无副作用接口、
   复现当前流程、查看真实输出或用现有 fixture 跑一条闭环？
3. **确认边界**：哪些相邻问题暂不解决？哪些依赖、平台、接口或场景明确不纳入本 change？
4. **记录结论**：把调研事实写入 `proposal.md` 的 `## Research Log` 章节，使用固定字段（未知描述 / 验证方式 /
   结论 / 证据摘要）；若本次 propose 未执行任何调研（例如纯 `inline-direct` 的机械变更），可以省略该章节；
   不要创建 `research.md`、`brainstorm.md` 或外部设计文档。

安全规则：
- 只做低风险、可逆、范围内的验证；涉及生产副作用、凭据、成本或权限不明时不要调用
- 如果无法试通，把 blocker、拒绝理由或后续验证入口写进 proposal，而不是把未知伪装成已知
- 调研服务于 proposal 定义，不进入实现阶段，不勾选任务，不创建 ledger

### Step 2.4：bugfix 分支判断与 triage

Step 2.3 之后、选择 proposal 引擎之前，先判断本次变更是否属于 bugfix 场景。bugfix 指**在既有 spec 上发现并修复缺陷**，而非新增/扩展能力。

**若缺陷已锚定 post-apply 或 archived change**：不要用本步硬套完整 proposal/plan；改走 **`openspec-bugfix` / `/opsx:bugfix`**。

**触发判断**：

- 若用户描述指向既有 capability spec 的**现有行为偏离**（"应该 X，实际却 Y"），而非新增行为 → 走 bugfix 分支
- 若是新增/扩展能力 → 跳过本步，走现有 feature proposal 流程（Step 2.5 及之后）

用户大概率会明说"这是 bug / 修复 / 不符合预期"，但不强求显式声明——由本步根据"是否指向既有 spec 的现有行为偏离"自动判断。无法判断时按 feature 流程走，不强行套 bugfix。

**bugfix 分支的 triage 纪律**（复用 Step 2.3 的安全规则与"不创建额外 artifact"约束）：

1. **定位既有 spec**：`openspec list --specs`，指向 `openspec/specs/<capability>/spec.md` 的具体 Requirement / Scenario
2. **澄清现象**：预期行为 vs 实际行为 + 触发条件
3. **性质判定三选一**（核心纪律，必须落到其一）：
   - **A 实现 bug**：spec 描述了正确行为，实现偏离了 → 后续走"修代码"方向，spec delta 可能不需要（或仅补 Known Failure Modes）
   - **B spec 缺陷**：spec 本身描述错了或漏了 → 后续走"改 spec + 修代码"方向，需要 spec delta 描述语义变更
   - **C 非 bug**：澄清后发现是误解或预期行为 → 不开 change，直接关闭，不产出 `proposal.md`
4. **尽力复现**（不强求）：能安全复现时，用最小命令/最小调用拿到真实输出，记录复现步骤与实际输出；不能复现时，把"未复现"作为 blocker 原因显式写入 `## Triage` 段，不阻塞流程，留给 plan 阶段的 bounded research 处理，且不得把未复现伪装成已确认事实。复现证据也可以交叉引用 `## Research Log` 中的对应记录，但 `## Triage` 段本身始终是 bugfix proposal 的必填特征段
5. **沉淀进 `## Triage` 段**：把 triage 结论写进 `proposal.md` 的 `## Triage` 段（见 Step 4）

`## Triage` 段固定字段（位置不强制，建议在 `## Why` 之前）：

```markdown
## Triage

- **现象**：<预期 vs 实际>
- **受影响 spec**：`openspec/specs/<capability>/spec.md` → <Requirement / Scenario 名>
- **性质判定**：A 实现 bug | B spec 缺陷 | C 非 bug
- **复现**：<最小复现步骤 + 实际输出> 或 <未复现，blocker 原因>
- **根因初判**：<若已定位> 或 <待 plan 阶段 bounded research>
```

**proposal 引擎选择**（衔接 Step 2.5）：bugfix 分支默认走 `inline-direct`（bug 现象通常已明确，不需要发散）。但性质判定 Step 3 可选 `grill-with-docs`——因为"对照既有 spec 拷问性质"本质就是 grill-with-docs 的用途。bugfix 不使用 `superpowers:brainstorming`（无需发散构思）。

**约束**：
- 不创建 `triage.md`、`research.md` 或其他额外 artifact；triage 结论只沉淀进 `proposal.md` 的 `## Triage` 段
- 性质判定 C 不开 change，不产出 `proposal.md`
- 非 bugfix 时跳过本步，不得让 bugfix 分支污染 feature proposal 流程

### Step 2.5：选择 proposal 引擎（agent-agnostic）

阶段引擎是每个 OpenSpec 阶段用来完成本阶段工作的方式。propose 阶段的 proposal 引擎指**需求澄清 / 构思收敛引擎**，不是 plan/apply 阶段的实现编排器。

**禁止**在 propose 阶段把以下实现编排器作为 proposal 引擎候选：
- `/goal`
- `ultragoal`
- `subagent-driven`
- `executing-plans`

这些能力属于 plan/apply 的任务编排或实现推进，不负责定义 proposal 的意图、范围与能力映射。

在开始生成 proposal 前，根据变更清晰度选择一种 proposal 引擎：

| Proposal 引擎 | 可用条件 | 适用场景 | 规则 |
|----------------|---------|---------|------|
| `superpowers:brainstorming` | skill 可见 | 需求仍需要发散、比较方案、确认成功标准 | 一次一个问题；收敛后把结论直接写入 `proposal.md` |
| `grill-with-docs` | skill 可见 | 需要对照既有 spec / docs / 领域术语拷问 proposal 边界 | 只借用文档拷问与术语收敛方法；结论直接写入 `proposal.md` / spec delta |
| `inline-clarify` | 永远可用 | 需求有方向但边界、约束、取舍还不够清楚，且不需要专门 skill | agent 内联追问；只问会改变 proposal 的问题 |
| `inline-direct` | 永远可用 | 用户意图已清楚，或只是小型/机械变更 | 不额外访谈，直接写 proposal，并在文本中显式记录关键假设 |

选择规则：
- 如果用户明确要求某种 proposal 引擎，按用户指定执行。
- 如果需求仍有会影响 scope / capability / non-goal 的关键未知，优先 `superpowers:brainstorming`（可用时），否则用 `inline-clarify`。
- 如果 proposal 必须对照既有 specs、docs、术语或历史决策来拷问边界，优先 `grill-with-docs`（可用时），否则用 `inline-clarify`。
- 如果需求已经足够明确，且 Step 2.3 的关键未知已有结论或明确 blocker，使用 `inline-direct`，不要为了流程而强行提问。
- CI / headless / 无对话通道时，使用 `inline-direct`，并在 `proposal.md` 中记录可验证假设。

### Step 3：运行 proposal 引擎（不产额外文件）

按 Step 2.5 选定的 proposal 引擎推进：
- `superpowers:brainstorming`：若可用，调用它做前置澄清；一次一个问题，优先问范围、约束、成功标准、方案取舍
- `grill-with-docs`：若可用，调用它对照既有 spec / docs / 领域术语拷问 proposal；本阶段只沉淀 OpenSpec artifact，不创建或更新 `CONTEXT.md` / ADR / 其他外部文档
- `inline-clarify`：由当前 agent 内联追问；保持一问一答，直到 proposal 的 Why / What Changes / Capabilities / Impact 足够稳定
- `inline-direct`：跳过访谈；直接生成 proposal，并把关键假设写入 `Why` 或 `Impact`

**禁止**：
- 不创建 `brainstorm.md`
- 不创建 `research.md`
- 不写 `docs/superpowers/specs/`

proposal 引擎输出和 Step 2.3 调研结论必须**直接沉淀到 `proposal.md`**。

### Step 4：生成中文 `proposal.md`

按 OpenSpec 原生 proposal 结构写：

- `## Why`
- `## What Changes`
- `## Capabilities`
  - `### New Capabilities`
  - `### Modified Capabilities`
- `## Impact`

要求：
- 中文写作
- 有明确前后对比时优先用 From / To
- `Capabilities` 必须为后续 spec delta 提供映射
- 若已执行 Step 2.3 的 bounded research，把调研结论写入 `## Research Log` 章节（固定字段：未知描述 / 验证方式 / 结论 / 证据摘要）；该章节可选，未执行任何调研时可省略
- 不把未验证猜测写成确定事实；未知要么被试通、要么被拒绝/移出范围、要么作为 blocker 明示

**bugfix 分支的 proposal 写法**（Step 2.4 判定为 bugfix 时适用）：

- proposal 含 `## Triage` 段（字段见 Step 2.4），这是 bugfix proposal 的特征段；feature proposal 不出现该段
- `## Why` 从 triage 的"现象 + 性质判定"推导而来，讲清为什么现在修；仍须满足 schema 的 50–1000 字符硬约束（bugfix 的 Why 通常偏短，注意不要低于 50 字符）
- `## What Changes`：性质判定 A（实现 bug）时只描述代码修复方向；性质判定 B（spec 缺陷）时用 From/To 描述 spec 语义变更
- `## Capabilities`：
  - 性质判定 A：`### Modified Capabilities` 可能为空（仅修代码、不改 spec 语义）或仅列受影响 capability。**注意**：是否允许为空取决于 OpenSpec 原生 zod schema 校验，若 `openspec validate --strict` 报错则至少列出受影响 capability
  - 性质判定 B：在 `### Modified Capabilities` 明确列出 spec 语义变更的 capability
- `## Impact`：受影响的代码文件与受影响的 spec

### Step 5：生成 capability-oriented spec deltas

在：

```text
openspec/changes/<change-id>/specs/<capability>/spec.md
```

写入 delta。

要求：
- 中文叙事优先：背景 / 问题 / 方案 / 决策
- 对复杂行为再补 Requirements / Scenarios
- 与既有 capability 命名对齐

### Step 6：校验

```bash
openspec validate <change-id> --strict --no-interactive
```

### Step 7：交接

告知用户：
- proposal 已写入 `openspec/changes/<change-id>/proposal.md`
- specs delta 已写入 `openspec/changes/<change-id>/specs/`
- 下一步进入 `openspec-plan`
- 若后续在 apply 之后或 archive 之后才发现缺陷：用 `openspec-bugfix` / `/opsx:bugfix`，不要默认再开一轮完整 plan
