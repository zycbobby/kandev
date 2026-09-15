## Why

Kandev 的文件查看器打不开 SVG 的图形预览。后端以「内容是否为合法 UTF-8」判定二进制，而 SVG 是 XML 文本，因此 `.svg` 永远 `is_binary=false`；前端四个文件查看入口又都先判 `is_binary`、再判是否图片，两个条件对 SVG 永远无法同时成立，`.svg` 只能落到源码编辑器。仓库里 `svg` 其实早已被写进图片扩展名与 MIME 表，但那行代码不可达，说明「把 SVG 当图片」的意图存在却从未接通。本次把它接通，并补上源码回切入口，四个入口一次修齐；改动全部在前端，零后端改动。

## What Changes

**文件类型判定：引入「文本编码图片格式」概念**

- From: 分类规则为「`is_binary` 为真时按扩展名判图片，否则一律文本」。SVG 是文本，故永远进不了图片通道。
- To: 在既有规则之上补一条分支——内容为文本、但扩展名属于**文本编码图片格式**（当前仅 SVG）时，同样判为图片。`is_binary` 为真的路径保持原样，其它文本扩展名保持原样。
- Reason: SVG 是唯一「合法文本同时是图片格式」的常见格式，需要与 PNG 等二进制图片走同一条呈现路径。
- Impact: 非破坏性。四个入口的分类判定需同步调整；`Blob`/`File` 语义不变。

**SVG 默认呈现为图形**

- From: 打开 `.svg` 看到 XML 源码，无任何图形预览入口。
- To: 打开 `.svg` 直接呈现渲染后的图形，与 PNG/JPG 的直觉一致。
- Reason: 用户已确认「默认渲染图 + Show code 切回源码」；这是本次交互定调。
- Impact: 用户可见的行为变化。SVG 的编辑能力不再默认可见，须经 `Show code` 进入。

**图形呈现形态：`<img>` + `data:` URL**

- From: 图片统一用 `data:${mime};base64,${content}`，且只接受后端已 base64 的内容。
- To: 保持同一形态，但对文本编码的 SVG 先在前端把 UTF-8 文本编码为 base64 再拼接。
- Reason: `<img>` 内的 SVG 走浏览器「安全静态模式」，脚本不执行、外部资源不加载，因此**不需要** HTML 预览那样的信任警告；同时 Tauri 外壳的 CSP 只允许 `img-src 'self' data:`，`blob:` 会被拦截，故**必须**用 `data:` URL。
- Impact: 需要一个 UTF-8 → base64 的小工具函数（仓库现有 `atob` 解码用法，无编码helper）。

**四个入口行为一致**

- From: 桌面 tab、dockview 面板、移动端查看器、聊天 rich-output 文件块各自复制了一份相同的判定逻辑。
- To: 四处按同一新规则判定；前三个入口提供 `Show code` ⇄ `Preview SVG` 的往返切换，默认停在图形态。
- Reason: 根因相同，分别修复会留下行为不一致。
- Impact: 前三个入口需各自持有切换状态；聊天块只做呈现替换，源码入口复用其既有的「打开文件」动作。

**不改变的部分**

- Changes 面板中 `.svg` 改动行的行为（当前导向文件查看器而非代码 diff）本次不动，另开 change。
- 不引入 agentctl 静态服务器、端口代理或 iframe。
- 不新增信任警告文案。
- 不做 Markdown 内嵌图片/相对路径渲染。

## Capabilities

### New Capabilities

- `file-preview`: 文件查看器对文件类别的判定规则，以及图片类文件的呈现与源码回切行为；覆盖桌面 tab、dockview 面板、移动端查看器与聊天 rich-output 文件块四个入口的一致性。

### Modified Capabilities

（无。`openspec/specs/` 下现有 `runtime-debugging` 与 `workflow-selector` 均不受影响；文件查看领域此前没有 capability spec。）

## Research Log

### 未知 1：桌面端 CSP 是否允许 `<img>` 使用 `blob:` URL

- **未知描述**：若采用 `URL.createObjectURL` 渲染文本 SVG，需确认在桌面（Tauri）外壳下不被 CSP 拦截；这直接决定实现形态是 blob 还是 data URL。
- **验证方式**：读取桌面包的 CSP 配置与其构建产物来源。
- **结论（試通證據）**：`blob:` **不被允许**，必须使用 `data:` URL。CSP 为 `img-src 'self' data:`，未列 `blob:`；构建走 `frontendDist: "../dist"`，即由 Tauri 提供并施加该 CSP。
- **證據摘要**：`apps/desktop/src-tauri/tauri.conf.json:30` → `"csp": "... img-src 'self' data:; ..."`，同文件 `build.frontendDist: "../dist"`、`build.devUrl: "http://127.0.0.1:1420"`。另查 `apps/web/index.html` 无 CSP meta，Go 后端服务 SPA 路径亦无 CSP 注入（命中项仅插件 webapp `policy.go` 与端口代理），故浏览器部署形态两者皆可，桌面形态以 `data:` 为准。

### 未知 2：文本编码图片的 `data:` URL 具体形态

- **未知描述**：`encodeURIComponent` 式（`data:image/svg+xml;charset=utf-8,<encoded>`）与 base64 式哪个更合适，影响是否需要新增工具函数。
- **验证方式**：读现有图片渲染实现与仓库内 base64 工具现状。
- **结论（試通證據）**：采用 base64 式，需新增一个 UTF-8 → base64 工具函数。理由：与既有 `data:${mime};base64,${content}` 形态一致，`FileImageViewer` 只需一处分支；体积膨胀为固定 1.33x，而 `encodeURIComponent` 对 XML 标记字符（`<` `>` `"` 空格换行均转 `%XX`）膨胀不可控。
- **證據摘要**：`apps/web/components/task/file-image-viewer.tsx:20-21`（现有 `data:${mime};base64,${content}` 写法）；仓库仅见 `atob` 解码用法 `apps/web/lib/utils/file-download.ts:14`，无 UTF-8 编码 helper。

### 未知 3：无差别「扩展名优先」是否会造成既有行为回归

- **未知描述**：若把「扩展名判图片」直接提到「`is_binary` 判文本」之前，则一个名为 `.png` 但内容为合法 UTF-8 文本的文件会从「文本编辑器」变成「破图」，属回归。
- **验证方式**：读四个入口的分类实现并推演各分支。
- **结论（試通證據）**：存在回归，故**不采用**无差别重排。改为只对「文本编码图片格式」（白名单，当前仅 SVG）放行，`.png` 等二进制图片格式的文本文件维持今日的文本行为。
- **證據摘要**：四处判定为同一份复制逻辑——`apps/web/components/task/file-tab-content.tsx:13-16`、`file-editor-panel.tsx:26-29`、`apps/web/components/task/mobile/mobile-file-viewer-panel.tsx:35-38`、`apps/web/components/task/chat/messages/kandev/rich-output/file-preview-block.tsx:38`，均以 `if (!isBinary) return "text"` 开头。

## Impact

**受影响的代码**

- `apps/web/lib/utils/file-types.ts`：新增文本编码图片格式的判定；新增 UTF-8 → base64 工具函数。
- 四个分类判定点：`components/task/file-tab-content.tsx`、`components/task/file-editor-panel.tsx`、`components/task/mobile/mobile-file-viewer-panel.tsx`、`components/task/chat/messages/kandev/rich-output/file-preview-block.tsx`。
- `apps/web/components/task/file-image-viewer.tsx`：接受 `isBinary`（或其等价信息），文本 SVG 走新增编码分支；新增 `Show code` 动作。
- 编辑器工具栏（`components/editors/monaco/monaco-editor-toolbar.tsx`、`components/editors/codemirror/codemirror-code-editor.tsx`）与 `file-editor-content.tsx`：为源码态提供 `Preview SVG` 回切入口。工具栏现有 `isHtml ? ... : ...` 二元分支需改为查找表（仓库 lint 禁止嵌套三元）。
- 三个查看器宿主（`file-tab-content.tsx`、`file-editor-panel.tsx`、`mobile-file-viewer-panel.tsx`）：持有「显示源码」状态。按已定决策**不持久化**。

**接口 / 数据 / 依赖**

- 无后端改动，无 API/协议变更，无新增依赖。
- 无持久化 schema 变更（切换状态不落盘）。

**规格**

- 新增 `openspec/specs/file-preview/`（本 change 以 ADDED delta 引入）。
- 后续应在 `docs/specs/ui/` 下补充与 `native-html-preview` 并列的 requirements + system-design 一对。

**本地化**

- 复用既有 `task:showCode`（五个 locale 均已存在，已核实）。
- 新增 `editors:previewSvg` 一个 key，需补齐 en / pt-pt / zh-cn / zh-hk / zh-tw；繁体两项走 `pnpm run i18n:zh-hant`，文案不得含 U+2014。

**验证**

- 触及前端 UI，按仓库约定需走 `/mobile-parity`；E2E 需补文件查看器预览覆盖（含移动端）。
