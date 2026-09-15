# Verification Report

> 此文件在 apply 完成 + Verify 步骤漂移检测通过后产生，确认实现与 proposal / design / specs / tasks 的一致性，作为 archive 前置。

**Change**: `add-svg-file-preview`
**Verified at**: `2026-09-15`
**Verifier**: Verify 步骤漂移检测 subagent（逐条 delta 对照）+ 主会话对四条高风险断言独立复核

---

## 1. Structural Validation (`openspec validate --strict --no-interactive`)

- [x] `Change 'add-svg-file-preview' is valid`（exit 0）

## 2. Task Completion (`tasks.md`)

- [x] 实现清单 10 组共 42 条全部勾选；验证清单 6 条路径 + 4 条工具链全部勾选
- [x] ledger Exit Gate 三项（Verification Strategy 入口通过 / `openspec validate --strict` 通过 / 验收方法与证据）全部满足

## 3. Delta Spec Sync State

| Capability | Sync 状态 | 备注 |
|---|---|---|
| file-preview | N/A（新增 capability） | `openspec/specs/` 原仅有 `runtime-debugging` 与 `workflow-selector`，均不受本次影响；delta 位于 `openspec/changes/add-svg-file-preview/specs/file-preview/spec.md`，archive 时 materialize 到 `openspec/specs/file-preview/` |

## 4. Requirements ↔ Implementation 逐条对照

| Requirement | 实现位置 | 差距 |
|---|---|---|
| 文本编码图片格式的分类判定（3 场景） | `apps/web/lib/utils/file-types.ts` 的 `TEXT_IMAGE_EXTENSIONS` / `isTextEncodedImage` / `resolveFileViewerKind`；四入口统一调用 | 无 |
| SVG 默认以图形呈现（2 场景） | `file-tab-content.tsx`、`file-editor-panel.tsx`、`mobile/mobile-file-viewer-panel.tsx` 默认 `showImage`；`file-image-viewer.tsx` 的 `object-contain` 容器约束未改动 | 无（桌面「超宽 SVG 被容器约束」依赖未改动的既有 CSS，移动端有专项 e2e 断言） |
| 图片渲染必须使用 CSP 允许的 `data:` URL（3 场景） | `utf8ToBase64`（分块编码）+ `buildImageSrc`，全仓无 `createObjectURL` / `blob:` | 无 |
| 图形呈现提供回到源码的入口（3 场景） | `onShowSource` / `show-source-toggle`；源码态经 `sourcePreviewAction` 回切；`useTextImageSourceToggle` 为 `useState`，未落盘 | 无 |
| 四个入口行为一致（3 场景） | `file-preview-block.tsx` 图片判定提前 + `buildImageSrc`；源码入口复用既有打开文件动作 | 无 |
| 移动端等价且可触控（2 场景） | `h-11`（<640px 44px）控件；`assertNoDocumentHorizontalOverflow` 断言 | 无 |
| 本次变更不得改变相邻行为（2 场景） | `changes-panel-file-row.tsx`、`markdown-preview-content.tsx` 未出现在 diff；后端零改动 | 无 |

**Out-of-scope 检查**：工作区改动全部落在 `apps/web/**` 与 `docs/specs/ui/**`；`git status --porcelain -- apps/backend` 为空；`apps/package.json` / `apps/web/package.json` / lockfile 无 diff，零新依赖。

## 5. Design Decisions 核对（D1-D6）

| 决策 | 核对结论 |
|---|---|
| D1 白名单分支收敛为单一 `resolveFileViewerKind` | ✅ 四条分支逐一推演成立；旧的四份复制判定已清零，`resolveFileViewerKind` 调用点恰为四个入口 |
| D2 `data:image/svg+xml;base64` + 分块 `utf8ToBase64` | ✅ 逐字匹配；无 blob / `encodeURIComponent` 方案残留 |
| D3 单一共享 hook、不持久化 | ✅ 三宿主共用 `useTextImageSourceToggle`；未进 dockview store |
| D4 工具栏改动作描述符查找表 | ✅ `preview-action.tsx` 的 `resolvePreviewAction` / `PreviewActionButton`；markdown / html 两路标签、testid、禁用态与信任提示等价（单测覆盖） |
| D5 聊天块只替换呈现，不加第三态 | ✅ 未引入源码态 |
| D6 E2E 桌面新建 + 移动端扩展 | ✅ `svg-file-preview.spec.ts`（3 用例）+ `mobile-file-viewer.spec.ts` 扩展（2 用例）；另有 `rich-output-svg-preview.spec.ts` 补齐路径 4 |

**已在 ledger / design 登记的偏差（非新漂移）**：移动端源码态回切控件落在 header 而非经 `sourcePreviewAction` 透传（移动端源码态为无工具栏的只读 `FileViewerContent`）；`Show code` 采用 `h-11 ... sm:h-8`，与同 header 既有下载控件同式。

## 6. 抽检事实（主会话独立复核，非 subagent 结论转述）

- `git status --porcelain -- apps/backend` 无输出 ✓
- `git diff HEAD -- apps/web` 中 `createObjectURL` / `blob:` 零命中；仅 `file-types.ts:163` 注释提及「`blob:` 会被拦截」✓
- `resolveFileViewerKind` 调用点：`file-tab-content.tsx`、`file-editor-panel.tsx`、`mobile/mobile-file-viewer-panel.tsx`、`rich-output/file-preview-block.tsx` 及其单测，无扩散 ✓
- 依赖清单与 lockfile 无 diff ✓

## Overall Decision

- [x] ✅ PASS — 可进入 archive（`openspec validate --strict` exit 0；tasks 全勾；漂移零发现；四条高风险断言独立复核通过）
