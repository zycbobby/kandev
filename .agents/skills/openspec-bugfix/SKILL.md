---
name: openspec-bugfix
description: >
  Use when fixing a defect anchored to a change that has already entered
  apply or has been archived. Fast path: systematic debugging, hang fixes
  on the original change (or patch master specs when archived), skip full
  plan, keep OpenSpec validate/acceptance gates. Triggers: "/opsx:bugfix",
  "post-apply bug", "archived change bug", after apply/verify/archive.
---

# OpenSpec Bugfix

目标：为 **已进入 apply 之后（含已 archive）** 的缺陷提供快速修复入口，在不重走完整 propose → plan 的前提下，保持对 OpenSpec specs 的控制。

**与 `openspec-propose` triage 的边界**：

| 阶段 | 入口 |
|------|------|
| pre-apply（开 change / 定义范围前）的既有 spec 偏离 | `openspec-propose` 的 bugfix triage（Step 2.4） |
| post-apply 或 archived change 上的修复 | **本 skill** `/opsx:bugfix` |
| 新能力 / 行为扩展（非既有 change 锚定缺陷） | `openspec-propose` feature 流程 |

## 调试引擎

优先使用 `superpowers:systematic-debugging`（skill 可见时调用）。

若不可见：内联等价根因优先纪律，**禁止在未完成根因调查前改代码**：

1. 读清错误 / 现象与触发条件  
2. 能安全复现则最小复现  
3. 查最近变更与相关实现  
4. 多组件时在边界取证，定位失败层  
5. 再提出修复并验证  

**落盘只写 OpenSpec 约定位置**。禁止创建 `debug.md`、`triage.md`、`research.md`、`brainstorm.md`、`docs/superpowers/**` 等额外 artifact。

## 执行流程

### Step 1：锚定 change

```bash
openspec list
ls openspec/changes/
ls openspec/changes/archive/ 2>/dev/null
```

确定目标：

- active：`openspec/changes/<change-id>/`
- archived：`openspec/changes/archive/<change-id>/`

读取至少：

```bash
cat openspec/changes/<path>/proposal.md 2>/dev/null
cat openspec/changes/<path>/tasks.md 2>/dev/null
cat openspec/changes/<path>/design.md 2>/dev/null
cat openspec/changes/<path>/specs/*/spec.md 2>/dev/null
cat openspec/changes/<path>/reviews/state.json 2>/dev/null
cat openspec/specs/<capability>/spec.md 2>/dev/null
```

若用户未指定 change：根据现象、最近 archive、或 `openspec list` 交互确认；无法锚定则 **拒绝** 并指引 `/opsx:propose`。

### Step 2：入口守卫（任一命中则停止）

1. **无法锚定** post-apply / archived change → 拒绝 → `/opsx:propose`  
2. **feature 形请求**：对 master capability 的新需求 / 行为扩展，而非既有 change 的实现或描述缺陷 → 拒绝 → `/opsx:propose`  
3. **pre-apply**：目标仍在 active 且 **未进入 apply**（见 Step 2a）→ 拒绝 → `/opsx:propose` 的 bugfix triage  
4. **review 阻塞**（仅 active）：存在 `reviews/state.json` 且（`status === "collecting"` **或** `applyBlocked === true`）→ 拒绝改产品代码 → 先 `/opsx:review-change`  

用户可显式声明“按 post-apply 处理”；须在修复记录中注明判定依据。

### Step 2a：是否已进入 apply（启发式）

按序判定：

1. 路径在 `openspec/changes/archive/` → **archived**（本 skill 适用）  
2. active 且满足任一：  
   - 存在 `ledger/`  
   - `tasks.md` 含至少一个 `- [x]`  
   - 存在 `verify.md`  
   → **已 enter apply**（本 skill 适用）  
3. 否则 → **pre-apply**（拒绝）

注意：仅有全未勾选的 `tasks.md` **不等于** 已 enter apply。`openspec status` 可作辅助；change-id 以数字开头时 status 可能失败，以文件系统信号为准。

### Step 3：性质判定 + 根因

澄清：预期 vs 实际 + 触发条件。

**性质判定三选一**（必须落到其一）：

| 判定 | 含义 | 后续 |
|------|------|------|
| **A 实现 bug** | spec/delta 正确，实现偏离 | 修代码；通常不改 spec 语义（可补 Known Failure Modes） |
| **B spec 缺陷** | spec 错了或漏了 | 改 spec（及必要代码） |
| **C 非 bug** | 误解 / 预期行为 / 超范围 | 关闭，不改产品代码 |

按调试引擎完成根因调查后再改代码。尽力复现：能安全复现则记录步骤与输出；不能则把“未复现”与原因写入修复记录，不得伪装成已确认事实。

### Step 4A：未 archive — 挂回原 change

- **不** `openspec new change`  
- **跳过** 完整 `openspec-plan` / 重写 design  
- 可在原 `tasks.md` 追加精简 follow-up 勾选项；在 `ledger/` 追加本次修复记录（若尚无 ledger 可创建简表）  
- **A**：修产品代码；delta 可不改  
- **B**：先（或同步）更新 `openspec/changes/<id>/specs/**` delta，再对齐代码  
- **C**：只解释关闭原因  

**轻量 verify 门禁（完成前必须）**：

1. `openspec validate <change-id> --strict --no-interactive` 通过  
2. 记录验收证据：复现步骤或命令 + 修复后结果  

任一项失败 → 不得宣告本次 bugfix 完成。

### Step 4B：已 archive — 直接改 master + 轻量记录

- **不** 默认 reopen 完整 propose/plan/apply/archive  
- **A**：直接改产品代码  
- **B**：直接改 `openspec/specs/<capability>/spec.md`（MODIFIED header 必须与现有 header 逐字符一致）；并改必要代码  
- **C**：不改代码/spec  

写记录：

```text
openspec/changes/archive/<change-id>/bugfixes/<timestamp>-summary.md
```

至少包含：

- 现象（预期 vs 实际）  
- 性质判定 A/B/C  
- 根因  
- 触及的文件 / specs  
- 验收证据（步骤或命令 + 结果）  

**完成前**必须具备验收证据；无证据不得宣告完成。  
（archive 项不在 `openspec validate` 列表中；仍应对相关代码跑最小验收命令。）

### Step 5：交接

告知用户：

- 锚定的 change 与路径（active / archive）  
- 性质判定与根因摘要  
- 改动的文件 / specs  
- 验证命令与结果  
- 未 archive：是否还需 `/opsx:verify` 全量漂移检测  
- 已 archive：`bugfixes/` 记录路径；必要时建议在 master Known Failure Modes 补一行  

## 约束

- 不创建 OpenSpec 外额外 artifact  
- 不把 bugfix 当完整 propose/plan 替代  
- 不在 pre-apply 强行使用本 skill  
- 不在 collecting / applyBlocked review 下改产品代码  
- 不跳过根因调查  
- 未 archive 完成前必须 `openspec validate --strict` + 验收证据  
