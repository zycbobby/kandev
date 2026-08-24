## Why

<!--
Explain the motivation for this change. What problem does this solve? Why now?

硬限制：50 ≤ 字元數 ≤ 1000（OpenSpec zod schema 會 validate）
- 太短：會收到 `Why section must be at least 50 characters` error
- 太長：會收到 `Why section should not exceed 1000 characters` error

建議結構：現況痛點 → 為什麼現在處理 → 預期收益（各 1-2 句）
-->

## What Changes

<!--
Describe what will change. Be specific about new capabilities, modifications, or removals.

對於有明確前後對比的行為變更，使用 From/To 格式（markdown 無 inline diff）：

**<Section or Behavior Name>**
- From: <current state / requirement>
- To: <future state / requirement>
- Reason: <why this change is needed>
- Impact: <breaking / non-breaking, who's affected>

多個變更可重複此 block；純新增或純刪除可用簡單列表描述。
-->

## Capabilities

### New Capabilities
<!--
Capabilities being introduced. Replace <name> with kebab-case identifier.
命名規則見 openspec/specs/README.md：使用複合名詞（至少 2 個 word），
例如 `user-auth`、`data-export`、`api-rate-limiting`，不用純單詞。
Each creates specs/<name>/spec.md
-->
- `<name>`: <brief description of what this capability covers>

### Modified Capabilities
<!--
Existing capabilities whose REQUIREMENTS are changing (not just implementation).
Only list here if spec-level behavior changes. Each needs a delta spec file.
Use existing spec names from openspec/specs/. Leave empty if no requirement changes.
-->
- `<existing-name>`: <what requirement is changing>

## Research Log

<!--
可選章節：僅當本次 propose 實際執行過至少一條 bounded research / probe 時才需要。
純 inline-direct、無爭議的機械變更可以省略本章節。

每條未知一個 `### 未知 N：<描述>` 小節，固定四個字段：
- 未知描述：這條未知會如何影響 Why / scope / Impact / capability 映射
- 驗證方式：讀代碼 / 跑最小命令 / 調用無副作用接口 / 復現現象 等
- 結論：三選一 —「試通證據」/「拒絕理由」/「blocker」
- 證據摘要：命令 + 退出碼、真實 input/output、文件位置或行號

範例：
### 未知 1：<簡短描述>
- **驗證方式**：<怎麼驗證的>
- **結論（試通證據 / 拒絕理由 / blocker）**：<結論內容>
- **證據摘要**：<具體證據>
-->

## Impact

<!-- Affected code, APIs, dependencies, systems -->
