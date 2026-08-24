<!-- Source: superpowers-bridge/templates/adopters/CLAUDE.md.fragment.zh-TW.md -->

## 變更工作流（Claude Code 啟動先讀）

本 repo 採用 `superpowers-bridge`，核心規則是：

**讓 OpenSpec 保持工作流正門。**  
Superpowers 只作為 OpenSpec 各階段中的內嵌能力。

### 入口分流

| 觸發 | 該怎麼做 |
|---|---|
| 使用者要做新功能 / 新 capability / 架構變更 | 用 `/opsx:propose`，確保 change 以 `--schema superpowers-bridge` 建立 |
| 使用者已在某個 active change 中 | 用 `/opsx:plan` / `/opsx:apply` / `/opsx:verify` / `/opsx:archive` |
| 使用者只是先用自然語言討論想法 | 可以使用 proposal 執行器，例如 verbal `superpowers:brainstorming`，但正式開 change 後要把結論寫進 `proposal.md` |
| 使用者明確是 bug fix / typo / 很小的 config tweak | 直接 PR，不建 change |

### 硬規則

- 不建立 `brainstorm.md`
- 不建立 `plan.md`
- 不建立 `retrospective.md`
- 不把設計/規劃內容寫到 `docs/superpowers/specs/`
- 不把規劃內容寫到 `docs/superpowers/plans/`

### Archive 策略

- 依 capability 保守合併
- 優先更新既有 `openspec/specs/<capability>/spec.md`
- 非必要不要新增新的頂層 capability 目錄
