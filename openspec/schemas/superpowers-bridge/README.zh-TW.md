# superpowers-bridge Schema

[English](./README.md) · [繁體中文](./README.zh-TW.md)

> 這個 vendored schema 的目標是：**保留 OpenSpec 作為工作流正門**，
> 同時把 **Superpowers 能力內嵌到部分階段**。

## 設計原則

這個版本遵守四條規則：

1. **OpenSpec 是主流程**
   - 建 change：`openspec new change`
   - 推進 artifact：`openspec instructions ...`
   - 驗證：`openspec validate`
   - 封存：`openspec archive`

2. **Superpowers 是能力，不是替代流程**
   - 每個 OpenSpec 階段都可以選擇該階段自己的執行器
   - `proposal` 可選 proposal 執行器：`superpowers:brainstorming`、`grill-with-docs` 或內聯 direct/clarify
   - `design` / `tasks` 可借 planning 執行器，例如 `superpowers:writing-plans`
   - `apply` 可借 implementation 執行器，例如 worktree / subagent / TDD / code review
   - 但正式產物仍落在 OpenSpec 原生路徑

3. **artifact 集合盡量貼近 stock OpenSpec**
   - `proposal.md`
   - `design.md`
   - `specs/**/*.md`
   - `tasks.md`
   - `verify.md`

   這個 vendored schema **不使用** `brainstorm.md`、`plan.md`、
   `retrospective.md` 這類額外 bridge artifact。

4. **apply 保持 OpenSpec-native**
   - 實作進度以 `tasks.md` 為準
   - Superpowers 只增強執行品質，不替代 lifecycle

## 各階段執行器

階段執行器是用來完成某個 OpenSpec 階段的方法，同時保留該階段的原生 artifact 作為事實來源。Proposal 執行器負責收斂意圖與範圍；planning 執行器產出 `design.md` / `tasks.md`；apply 執行器依 `tasks.md` 推進實作。

## 執行模型

### Proposal

- OpenSpec artifact：`proposal.md`
- Proposal 執行器：`superpowers:brainstorming`、`grill-with-docs`、`inline-clarify` 或 `inline-direct`
- 規則：執行器輸出直接寫進 `proposal.md`

### Design + Tasks

- OpenSpec artifacts：`design.md`、`tasks.md`
- 可選內嵌能力：`superpowers:writing-plans`
- 規則：**不產生 `plan.md`**；規劃結果直接寫進 `design.md` / `tasks.md`

### Apply

- OpenSpec remains the controlling stage
- Recommended embedded capabilities:
  - `superpowers:using-git-worktrees`
  - `superpowers:subagent-driven-development`
  - transitive TDD / code review support when available
- Progress remains driven by `tasks.md`

### Verify

- OpenSpec artifact：`verify.md`
- 至少檢查：
  - `openspec validate --all --json`
  - task 完成度
  - delta spec sync 狀態
  - design / spec / implementation 一致性

### Archive

- OpenSpec 機制：`openspec archive -y`
- 策略：依 capability 保守合併
- 優先更新既有 `openspec/specs/<capability>/spec.md`
- 非必要不要新增新的頂層 capability 目錄
- 若需要 ADR，先看 `openspec/config.yaml` 的 `archive.decisions_dir`；若未設定，於 `dev-docs/decisions`、`docs/decisions`、`doc/decisions` 中選一個並記錄偏好

## 交付內容

這個 vendored schema 會提供：

- `schema.yaml`
- 以下 templates：
  - `proposal`
  - `design`
  - `specs`
  - `tasks`
  - `verify`
- adopter CLAUDE fragments

不再提供已移除 extra artifact 的 templates。

## 使用者路由建議

若專案採用本 schema，建議這樣分流：

- narrative 想法討論 → 可先選 proposal 執行器（`superpowers:brainstorming` / docs-aware grilling / 內聯澄清或 direct）→ `/opsx:propose`
- 已有 active change → `/opsx:plan` / `/opsx:apply` / `/opsx:verify` / `/opsx:archive`
- bug fix / typo / 小 config tweak → 直接 PR，不建 change

## Vendored 說明

這份內容是為 `openspec-my` **特別調整過的 vendored 版本**，
不是 upstream `openspec-schemas` 的逐字鏡像。

本 repo 真正出貨的行為，以這些檔案為準：

- `schema.yaml`
- `templates/*`
- bundled opsx skills / commands

如果你看到舊文件或上游敘述與這裡不同，**以本 repository 內 vendored 檔案為準**。
