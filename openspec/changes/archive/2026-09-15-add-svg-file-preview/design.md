## Context

Kandev 的文件查看器无法预览 SVG。根因是两条判定互相排斥：后端以「内容是否为合法 UTF-8」判定二进制（`apps/backend/internal/agentctl/server/process/workspace_files.go:502`），SVG 是 XML 文本因而永远 `is_binary=false`；四个前端入口又都先判 `is_binary`、再判扩展名是否图片。`.svg` 因此只能落到源码编辑器。

仓库里 `svg` 早已被列入图片扩展名表与 MIME 表（`apps/web/lib/utils/file-types.ts`），但那行代码不可达，说明「把 SVG 当图片」的意图存在却从未接通。

四个入口的分类判定是同一份逻辑的四次复制：`components/task/file-tab-content.tsx:13-16`、`components/task/file-editor-panel.tsx:26-29`、`components/task/mobile/mobile-file-viewer-panel.tsx:35-38`、`components/task/chat/messages/kandev/rich-output/file-preview-block.tsx:38`。

本 change 只改前端。已定的交互是「默认渲染图形 + `Show code` 切回源码」，因此 SVG 在语义上等同图片类文件，而不是 HTML/Markdown 那种「默认源码 + 预览切换」。

## Goals / Non-Goals

**Goals:**

- 打开 `.svg` 默认呈现渲染后的图形，桌面 tab、dockview 面板、移动端查看器三处一致。
- 提供 `Show code` 回到源码，源码态保留完整编辑、保存、下载、删除能力。
- 聊天 rich-output 文件块按同一规则呈现 SVG 图形。
- 把四处复制的分类判定收敛为单一实现，消除既有重复。
- 渲染路径在桌面（Tauri）外壳的 CSP 下可用。

**Non-Goals:**

- 不改 Changes 面板中 `.svg` 改动行的路由行为（另开 change）。
- 不引入 agentctl 静态服务器、端口代理或 iframe。
- 不做 Markdown 内嵌图片 / 相对路径渲染。
- 不新增信任警告文案（`<img>` 内 SVG 不执行脚本）。
- 不支持 `<foreignObject>` 内 HTML 与 SVG 外部引用的完整浏览器保真。
- 零后端改动、无 API/协议/持久化 schema 变更、无新增依赖。

## Research Log

### 未知 1：E2E 是否具备「把一个 .svg 放进任务工作区并打开」的既有能力

- **未知描述**：若 E2E 需要新建 fixture 基础设施，Verification Strategy 的成本与形态会显著变化，任务拆分也要多出一组。
- **验证方式**：读 e2e fixture 与 helper 的现有能力，不改动任何文件。
- **结论（試通證據）**：既有 helper 已支持，无需新建基础设施。`setupMobileFileViewerTest` 接受 `options.extension` 与 `options.content`（`string | Buffer`），内部用 `GitHelper.createFile` 写入真实 git 仓库、`stageAll`、`commit`，再创建任务并导航。
- **證據摘要**：`apps/web/e2e/tests/task/mobile-file-viewer.spec.ts:20-70`（`setupMobileFileViewerTest` 签名与实现）；`apps/web/e2e/helpers/git-helper.ts:32` → `createFile(name: string, content: string | Buffer)`。传入 `{ extension: "svg", content: "<svg .../>" }` 即可覆盖本次场景。

### 未知 2：桌面与移动端 E2E 的落位约定

- **未知描述**：决定新写 spec 还是扩展既有 spec，影响任务的文件路径与移动端覆盖是否达标。
- **验证方式**：读 e2e 目录结构与既有 HTML 预览 spec 的命名/断言方式。
- **结论（試通證據）**：以文件名 `mobile-` 前缀选择 mobile-chrome project。HTML 预览的桌面与移动端是两个文件：`apps/web/e2e/tests/chat/html-preview.spec.ts`（桌面，用 `html-preview-toggle` / `html-preview` testid）与 `apps/web/e2e/tests/task/mobile-html-preview.spec.ts`（移动端）。本次同样拆两个：桌面新 spec + 扩展既有 `mobile-file-viewer.spec.ts`（它已持有可直接复用的 setup helper）。另有 `assertNoDocumentHorizontalOverflow` 可直接支撑「移动端无横向溢出」验收。
- **證據摘要**：`apps/web/e2e/tests/task/mobile-file-viewer.spec.ts:1-4` 文件头注释明示 `mobile-` 前缀路由规则；`apps/web/e2e/tests/chat/html-preview.spec.ts:144-146`；`apps/web/e2e/helpers/layout-assertions.ts`（`assertNoDocumentHorizontalOverflow`）。

### 未知 3：`FileImageViewer` 是否已有可供 E2E 断言的锚点

- **未知描述**：无 testid 则 E2E 无法稳定断言「图形已渲染」，会影响验收可行性。
- **验证方式**：grep 组件源码中的 `data-testid`。
- **結論（試通證據）**：`FileImageViewer` 与 `file-viewer-header.tsx` 均**没有** `data-testid`，必须新增。这是 E2E 验收的前置任务，不能留到测试阶段才发现。
- **證據摘要**：`grep -n "data-testid" apps/web/components/task/file-image-viewer.tsx apps/web/components/task/file-viewer-header.tsx` → 无输出。

### 未知 4：源码态回切入口在工具栏的可挂载性

- **未知描述**：SVG 的 `previewKind` 为 `"none"`，若工具栏按钮在该值下直接返回 null，则「从源码切回图形」无处挂载，属最易低估的一处。
- **验证方式**：读工具栏组件源码。
- **結論（試通證據）**：确认存在硬门禁。`PreviewButton` 在 `previewKind === "none"` 时 `return null`，且标签与行为由 `isHtml ? onPreviewHtml : onTogglePreview` 二元选择。需要为它引入一个独立的动作描述符，而不是继续扩展二元分支（仓库 lint 禁止嵌套三元）。
- **證據摘要**：`apps/web/components/editors/monaco/monaco-editor-toolbar.tsx:237-252`（`if (previewKind === "none") return null`；`const action = isHtml ? onPreviewHtml : onTogglePreview`）；CodeMirror 侧同构于 `apps/web/components/editors/codemirror/codemirror-code-editor.tsx:234-252`。

### 未知 5：SVG 经 `<img>` 渲染在该技术栈下是否真的可行

- **未知描述**：proposal 已从 CSP 角度确认必须用 `data:` URL，但「`<img>` + SVG」在本仓库实际运行时是否渲染成功仍需证据，否则整个方案不成立。这是一条仓库外知识（浏览器行为）。
- **验证方式**：查找仓库内既有的、对 SVG 作为 `<img>` 渲染结果的运行时断言。
- **結論（試通證據）**：仓库已有运行时断言证明可行。两个 HTML 预览 spec 都把一个 `logo.svg` 资产放进工作区、由预览页以 `<img>` 引用，并在页面脚本中断言 `image.complete && image.naturalWidth > 0`，失败即测试红。
- **證據摘要**：`apps/web/e2e/tests/chat/html-preview.spec.ts:11-12`（`SVG_ASSET` 常量）与同文件页面脚本中的 `image.naturalWidth > 0`；`apps/web/e2e/tests/task/mobile-html-preview.spec.ts` 同构。这说明 SVG 在本栈的 `<img>` 渲染路径已被 CI 覆盖。

**依赖准出门禁结论**：本 change 不引入任何第三方 API、外部服务、外部 CLI 或 agent/runtime 行为依赖。唯一涉及仓库外知识的是「浏览器对 `<img>` 内 SVG 的处理」，已由未知 5 落为試通證據（仓库内既有 CI 断言）。**无 blocker，无未验证假设。**

## Decisions

### D1：判定规则采用白名单分支，并把四处复制收敛为一个函数

- **选择**：在 `apps/web/lib/utils/file-types.ts` 新增 `TEXT_IMAGE_EXTENSIONS`（当前仅 `svg`）与 `isTextEncodedImage(path)`，再新增单一导出 `resolveFileViewerKind(path, isBinary)`，由四个入口统一调用：
  1. 扩展名属图片格式且（内容为二进制 或 属文本编码图片格式）→ `image`
  2. 内容为二进制 → `binary`
  3. 其余 → `text`
- **理由**：既接通 SVG，又顺带消除四处重复；四条分支逐一推演过：二进制 PNG → image（不变）；文本 SVG → image（修复）；名为 `.png` 的文本文件 → text（不回归）；`.zip` 二进制 → binary（不变）。仓库约定也要求遇到重复时抽 helper。
- **已考慮 alternative**：把「扩展名判图片」无差别提到 `is_binary` 之前——**拒绝**，会让名为 `.png` 的文本文件从编辑器变成破图，属回归。新增 `FilePreviewKind: "svg"`——**拒绝**，`previewKind` 机制的语义是「默认源码 + 预览切换」，与「默认渲染」相反，且会牵连 `use-file-editors.ts` 等 5 处 `=== "markdown"` 的持久化判断。

### D2：渲染 URL 用 `data:image/svg+xml;base64,...`

- **选择**：新增 `utf8ToBase64(text)`（`TextEncoder` + 按 `0x8000` 分块 `String.fromCharCode` + `btoa`），以及 `buildImageSrc(path, content, isBinary)`：二进制内容直接用后端已给的 base64，文本内容先编码再拼。两个分支最终同为 base64 形态。
- **理由**：桌面 Tauri 的 CSP 为 `img-src 'self' data:`，`blob:` 被拦截，故必须 `data:`；base64 与既有 `file-image-viewer.tsx:20-21` 的写法同形，`FileImageViewer` 只需一处分支，体积膨胀固定 1.33x。分块编码避免 `String.fromCharCode(...bytes)` 在大输入上触发调用栈溢出。
- **已考慮 alternative**：`URL.createObjectURL` blob URL——**拒绝**，桌面端 CSP 拦截（`apps/desktop/src-tauri/tauri.conf.json:30`）。`data:image/svg+xml;charset=utf-8,${encodeURIComponent(text)}`——**拒绝**，XML 的 `<` `>` `"` 空格换行均转 `%XX`，膨胀不可控。

### D3：切换状态用单一共享 hook，保持组件局部、不持久化

- **选择**：新增 `apps/web/hooks/use-text-image-source-toggle.ts`，以 `fileIdentity`（session + repo + path）为依赖重置，暴露 `enabled` / `showSource` / `showSourceCode` / `showImage`。三个宿主各调用一次。
- **理由**：用户已明确未选「记住上次选择」，故不落盘、不进 store；但三个宿主若各写一份状态会复制三遍，抽 hook 与仓库「hooks 领域化」约定一致。
- **已考慮 alternative**：进 dockview store 并持久化（同 `renderedPreview`）——**拒绝**，与已定决策相悖。

### D4：工具栏回切入口改为动作描述符查找表

- **选择**：把 `PreviewButton`（monaco 与 codemirror 两处）从 `isHtml ? A : B` 改为接收一个动作描述符（label key / testid / onClick / disabled / 可选信任警告），由一个小 helper 以查找表形式解析；新增可选 prop `sourcePreviewAction` 自 `FileEditorContent` 透传至编辑器与工具栏。
- **理由**：SVG 的 `previewKind` 是 `"none"`，按钮会 `return null`，必须另开入口；描述符形式避免出现第三个嵌套三元（lint 禁止），也让 markdown / html 两条既有路径保持等价。
- **已考慮 alternative**：给 SVG 伪造 `previewKind: "html"` 复用现有分支——**拒绝**，会谎报文件类型并触发 HTML 发布流程。在编辑器外单独摆一个按钮——**拒绝**，破坏工具栏一致性且移动端无处安放。

### D5：聊天 rich-output 块只替换呈现，不加第三态

- **选择**：`file-preview-block.tsx` 把「是否图片」判定提到 `is_binary` 之前，用 `buildImageSrc` 渲染；非图片二进制仍显示既有的「预览不可用」文案；文本仍走截断 `<pre>`。源码入口复用其既有的打开文件动作，跳到完整查看器再用 `Show code`。
- **理由**：该块本就有展开/折叠交互，再加源码态会让它变成第三个小查看器；其定位是预览，不是编辑器。
- **已考慮 alternative**：块内直接加源码切换——**拒绝**，复杂度与收益不成比例。

### D6：E2E 落位为「桌面新建 + 移动端扩展现有」

- **选择**：桌面新建 `apps/web/e2e/tests/task/svg-file-preview.spec.ts`；移动端扩展现有 `apps/web/e2e/tests/task/mobile-file-viewer.spec.ts`（复用其 `setupMobileFileViewerTest`）。同时为 `FileImageViewer` 与 `Show code` 动作补 `data-testid`。
- **理由**：与 HTML 预览的既有双文件结构一致；移动端 spec 已持有可直接传参的 setup helper，避免重复基建。
- **已考慮 alternative**：把 setup helper 抽成公共 helper 供两端共用——**拒绝**（本次），属对既有测试文件的重构，超出本 change 边界；若后续第三个 spec 需要再抽。

## Risks / Trade-offs

- [Risk] dockview 面板若不随关闭卸载，「重新打开仍是源码态」会与 spec 的场景冲突 → Mitigation：hook 以 `fileIdentity` 为依赖重置；实现后用真实交互确认一次，必要时把重置挂到面板激活事件。
- [Risk] 工具栏重构可能回归既有的 markdown / html 预览按钮 → Mitigation：`markdown-preview-toggle` 与 `html-preview-toggle` 已被多个既有 spec 覆盖，作为回归网；改动后必须全绿。
- [Risk] 大体积 SVG 的 base64 编码开销与内存 → Mitigation：分块编码；沿用后端既有的 10MB 文件上限；无需额外限制。
- [Risk] 无 `data-testid` 导致 E2E 断言不稳 → Mitigation：已列为独立前置任务（见 Research Log 未知 3）。
- [Trade-off] `<foreignObject>` 内 HTML 与 SVG 外部引用不显示 → 接受理由：这是 `<img>` 安全静态模式的直接代价，也正是「脚本不执行、无需信任警告」这一收益的来源；用户已明确选择该方案。
- [Trade-off] SVG 默认不再直接呈现源码，编辑需多一次点击 → 接受理由：这是用户选定的交互（默认渲染 + `Show code`）。

## Migration Plan

N/A — 本 change 不涉及部署变更：无 endpoint、无 DB、无持久化 schema、无配置项、无新增依赖。前端资源随常规构建发布，无 rollback 特殊要求（回滚即回退前端构建）。

## Open Questions

1. dockview 面板复用场景下，以 `fileIdentity` 为依赖的重置是否已覆盖「关闭再打开」的全部路径，需在 apply 阶段以真实交互确认；若不足，改为在面板激活时显式重置。
2. 聊天 rich-output 块未来是否需要内联源码入口，留待后续观察；本次明确不做。
