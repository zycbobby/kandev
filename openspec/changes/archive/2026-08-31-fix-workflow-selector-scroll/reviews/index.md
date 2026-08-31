# Review Index — fix-workflow-selector-scroll

| Round | Checkpoint | Status | Trigger | Summary |
|-------|------------|--------|---------|---------|
| 1 | cp-001 | finalized | post-apply | 用户反馈移动端仍无法滚动 → 分类 A（code-only fix）：`WorkflowSelectorRow` 的 PopoverContent 需对齐 `combobox.tsx` 接线（dialog `portalContainer` + `pointer-events-auto` + `onWheel stopPropagation`），并增强移动端 E2E 用真实触摸滚动复现。见 `1.md`。 |
