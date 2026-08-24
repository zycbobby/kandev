---
name: openspec-archive-change
description: >
  Use when a change has been implemented and verified, ready to be
  archived. Merges spec deltas conservatively by capability and keeps
  OpenSpec specs lean.
---

# OpenSpec Archive

目标：更克制地 archive，只按 capability 合并，不随意扩张 `openspec/specs/`。

## 执行流程

### Step 1：确认前置条件

```bash
openspec validate <change-id> --strict --no-interactive
test -f openspec/changes/<change-id>/verify.md
```

### Step 2：做 capability-first 路由判断

优先判断这是：
- 产品能力变更
- 规范/约定变更
- 混合变更

原则：
- 产品能力 → 合并到 `openspec/specs/<capability>/spec.md`
- 规范约定 → AGENTS.md / docs

### Step 3：archive

```bash
openspec archive <change-id> --yes
```

### Step 4：克制地合并 specs

要求：
- 优先合并到已有 capability
- 不轻易创建新的顶级 spec 目录
- 只有真正新增独立核心 capability 才新建

### Step 5：Post-Archive Knowledge Checklist

archive 完成后，**必须逐项确认以下 4 项**，不可整体跳过。每项确认后直接执行，不留 pending 行动项。

向用户逐一询问：

1. **Known Failure Modes**：本次变更中是否有踩坑 / `What Didn't Work` 值得提炼？
   - 若是 → 在对应 `openspec/specs/<capability>/spec.md` 末尾的 `## Known Failure Modes` 表格追加行：
     ```
     | Symptom | Cause | Fix |
     ```
   - 若否 → 跳过，继续下一项

2. **AGENTS.md / CLAUDE.md**：本次变更是否引入了新的 SHALL/MUST 约束、工作流变化或影响 agent 行为的架构决策？
   - 若是 → 直接更新 `AGENTS.md` 和/或 `CLAUDE.md` 对应章节
   - 若否 → 跳过，继续下一项

3. **README.md**：本次变更是否影响了对外使用方式（CLI 命令、安装步骤、配置项）？
   - 若是 → 直接更新 `README.md` 对应章节
   - 若否 → 跳过，继续下一项

4. **ADR（可选）**：本次变更是否涉及重大架构决定，需要保留"为什么"的决策日志？
   - 若否 → 跳过
   - 若是 → 先读取 `openspec/config.yaml` 中的 `archive.decisions_dir` 偏好
     - 若不存在、值无效，或用户明确要求改目录：让用户在 `dev-docs/decisions`、`docs/decisions`、`doc/decisions` 中选择一个，并立即写回 `openspec/config.yaml`
     - 若已存在有效偏好：向用户展示当前值，并允许本次覆盖；若覆盖，同步更新 `openspec/config.yaml`
   - 然后 → 在选定目录创建 `YYYY-MM-DD-<slug>.md`，记录决策背景、备选方案与选择理由

全部确认完成后，向用户报告：
- archive 路径
- 合并到了哪些 capability
- Knowledge Checklist 各项执行结果（已更新 / 已跳过）
