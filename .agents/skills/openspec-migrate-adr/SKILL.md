---
name: openspec-migrate-adr
description: >
  One-shot in-place upgrade of the project ADR directory declared by
  archive.decisions_dir (default openspec/adr). No active change required.
  Renames files in place (strips YYYY-MM-DD- prefixes for stable slugs),
  rewrites bodies into 背景/决策/后果 with per-file user confirmation,
  and builds/updates the sibling index. Use when adopting an existing ADR
  directory as the living layer.
---

# OpenSpec Migrate ADR

目标：把项目 adr 目录**就地升级**成当前活层格式：原地改名、逐篇重排正文、建立/更新索引。

## 定位

- **不是流水线阶段**：不要求已存在 active change，不检查 `.openspec.yaml` 的 schema
- **一次性**：正文已含「背景/决策/后果」结构标题的文件跳过重写，无日期前缀的文件跳过改名；slug 冲突报告不覆盖
- **破坏性动作，安全阀先行**：git 工作区必须干净；先出 dry-run 清单；逐篇重写经用户确认
- **这是「无 change 不得改 adr」的唯一例外**：本 skill 只允许初始化空索引，或就地升级声明目录里的既有正文。之后对 adr 正文的实质修改仍须经某个 change 的 apply

## 执行流程

### Step 1：确定目标目录

1. 若 `openspec/config.yaml` 存在，读取 `archive.decisions_dir`（相对项目根的路径）
2. 已配置且目录存在 → 升级该目录
3. 已配置但目录不存在 → 报告错误并停止，不静默回退默认，不自动扫描其他候选
4. 未配置 → 目标为默认 `openspec/adr`；若该目录也不存在，经用户确认后初始化空索引与目录（表头见 Step 3），结束

不把任何值写回 `archive.decisions_dir`。

### Step 2：安全阀与 dry-run 清单

1. `git status --porcelain` 非空 → 拒绝执行，要求先提交或暂存
2. 对目录内每个 `.md`（忽略索引文件与 `.gitkeep`）生成计划：
   - **改名**：文件名去掉 `YYYY-MM-DD-` 前缀得稳定 kebab-case slug；去掉后为空则跳过并报告；slug 已被其他文件占用则报告冲突、不覆盖
   - **重写**：正文将重排为「背景/决策/后果」结构
   - **跳过**：无日期前缀或已含目标结构标题
3. 向用户展示完整清单（将改名 / 将重写 / 跳过 / 冲突四组），确认后进入 Step 3

### Step 3：就地升级

1. 建立或更新索引：索引是**与目录同级同名的 `<目录名>.md`**（`docs/decisions/` → `docs/decisions.md`；默认 `openspec/adr/` → `openspec/adr.md`）。空索引至少含表头：

```markdown
# ADR 索引

人读活层目录。按序号阅读。正文在 `<adr 目录>/<slug>.md`。

| 序号 | 文档 | 定位 | depends-on | supersedes |
|------|------|------|------------|------------|
```

2. 对清单中每个文件，逐篇执行：
   - 原地改名（`git mv <旧名> <新名>`，同名则跳过）
   - 把正文重排为「背景/决策/后果」结构，向用户展示改写结果，**经确认后**写盘；用户拒绝则保留原文并记录
3. 升级产物不写到目录之外，不保留旧文件名副本（改名不是删除，历史在 git）

### Step 4：汇报

告知用户：

- 升级了哪个目录、索引在哪
- 改名 / 重写 / 跳过 / 冲突各多少，以及用户拒绝保留原文的清单
- 之后改正文走 propose → plan → apply，不要再跑本 skill 当编辑器

## 约束

- 不要求 active change
- 不勾选任何 `tasks.md`，不创建 `ledger/`
- 不把升级产物写到声明目录之外
- 不写回 `archive.decisions_dir`
- 不使用 `YYYY-MM-DD-` 作为目标文件名
- 不静默覆盖 slug 冲突文件
- 除本 skill 的引导写入外，不在没有 change 的情况下改已有 adr 正文
