## Context
<!-- Background, current state, constraints, stakeholders. -->

## Goals / Non-Goals

**Goals:**
<!-- What this design aims to achieve -->

**Non-Goals:**
<!-- What is explicitly out of scope -->

## Research Log

<!--
可選章節：僅當 plan 階段實際執行過 bounded research / manual probe / debugging 時才需要。
未涉及倉庫外知識、無需驗證假設的簡單 change 可以省略本章節。

每條未知一個 `### 未知 N：<描述>` 小節，固定四個字段：
- 未知描述：這條未知會如何影響 design / task 拆分 / 驗收標準
- 驗證方式：讀代碼 / 跑最小命令 / 復現現象 / 調用外部 CLI 等
- 結論：三選一 —「試通證據」/「拒絕理由」/「blocker」
- 證據摘要：命令 + 退出碼、真實 input/output、文件位置或行號

範例：
### 未知 1：<簡短描述>
- **驗證方式**：<怎麼驗證的>
- **結論（試通證據 / 拒絕理由 / blocker）**：<結論內容>
- **證據摘要**：<具體證據>

依賴準出門禁要求：計劃涉及的每個潛在依賴都必須在這裡落到「試通證據」或「拒絕理由」其中一種終態，
不得只留「未驗證假設」就定稿。
-->

## Decisions

<!--
所有技術決策的唯一來源（single source of truth）。
本段記錄「技術路線上每個岔口怎麼選的」。

每個決策建議結構：
### D1：<決策標題>
- **選擇**：<採用的做法>
- **理由**：<為何這樣選>
- **已考慮 alternative**：<被拒方案 + 拒絕原因>
-->

## Risks / Trade-offs

<!--
Known risks and trade-offs.
Format: [Risk] <描述> → Mitigation: <緩解措施>
[Trade-off] <取捨描述> → 接受理由
-->

## Migration Plan

<!--
部署順序、rollback 策略、驗收條件。
若本 change 不涉及部署變更（純加套件、無 endpoint / DB 變更），
可寫「N/A — 本 change 不涉及部署變更」。
-->

## Open Questions

<!-- Outstanding decisions or unknowns to resolve -->
