# workflow-selector

新建任务弹窗中的工作流选择器（`WorkflowSelectorRow`）。本 change 固化其下拉列表在多选项场景下的高度与滚动约束：列表 MUST 在选项较多时约束自身高度并纵向滚动，保证桌面端与移动端都能完整浏览并选中全部工作流。

## ADDED Requirements

### Requirement: 工作流选择器列表在选项较多时必须可滚动

`WorkflowSelectorRow` 的下拉列表 SHALL 在选项数量超出可用垂直空间时约束自身高度并启用纵向滚动，使全部工作流选项均可被浏览与选中；该行为 MUST 在桌面端与移动端同时成立。

#### Scenario: 工作流较多时列表受高度约束并纵向滚动
- **WHEN** 已配置的工作流数量较多，列表高度超出 Popover 的可用高度（`--radix-popover-content-available-height` 或等价视口约束）
- **THEN** 列表容器出现纵向滚动（`overflow-y-auto`），用户可滚动查看并选中任意工作流

#### Scenario: 移动端同样可滚动浏览全部工作流
- **WHEN** 移动端视口下打开工作流选择器，且工作流数量较多
- **THEN** 列表仍受高度约束并可纵向滚动，不会因溢出视口而导致部分工作流不可见、不可选

#### Scenario: 工作流较少时不出现无谓滚动条
- **WHEN** 工作流数量较少、列表高度未超出可用高度
- **THEN** 列表按内容自适应高度，不强制出现滚动条
