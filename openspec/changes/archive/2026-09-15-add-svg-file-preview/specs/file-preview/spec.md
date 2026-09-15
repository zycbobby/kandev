# file-preview

## 背景

Kandev 的文件查看器无法预览 SVG。后端以「内容是否为合法 UTF-8」判定二进制（`apps/backend/internal/agentctl/server/process/workspace_files.go:502`），SVG 是 XML 文本因而永远 `is_binary=false`；四个前端入口又都先判 `is_binary`、再判扩展名是否图片，两个条件对 SVG 永远无法同时成立，`.svg` 只能落到源码编辑器。

仓库中 `svg` 早已被列入图片扩展名表与 MIME 表（`apps/web/lib/utils/file-types.ts`），但那行代码不可达，说明「把 SVG 当图片」的意图一直存在却从未接通。

本 capability 定义文件类别的判定规则，以及图片类文件的呈现与源码回切行为，覆盖桌面 tab、dockview 面板、移动端查看器与聊天 rich-output 文件块四个入口。

## 方案与决策

- **判定规则**：保留既有「`is_binary` 为真时按扩展名判图片、否则文本」的规则，在其上新增一条分支——内容为文本、但扩展名属于**文本编码图片格式**白名单（当前仅 SVG）时，同样判为图片。**明确拒绝**把「扩展名判图片」无差别提到 `is_binary` 之前，因为那会让名为 `.png` 的文本文件从编辑器变成破图，属回归。
- **呈现形态**：复用既有图片通道（`FileImageViewer`）与 `<img>` 标签。`<img>` 内的 SVG 走浏览器「安全静态模式」，脚本不执行、外部资源不加载，因此**不需要** `native-html-preview` 那样的信任警告，也**不引入** agentctl 静态服务器、端口代理或 iframe。
- **URL 形态**：统一用 `data:` URL。**必须**如此：桌面（Tauri）外壳的 CSP 为 `img-src 'self' data:`，`blob:` 会被拦截。文本 SVG 先在前端把 UTF-8 字节编码为 base64，再拼成 `data:image/svg+xml;base64,...`，与既有二进制图片的形态一致，且体积膨胀固定为 1.33x。
- **交互定调**：默认呈现图形，提供 `Show code` 切回源码；源码态保留完整编辑能力。切换状态**不持久化**，按会话内局部状态处理。
- **一致性**：四个入口共享同一判定与渲染路径。聊天 rich-output 块只做呈现替换，源码入口复用其既有的「打开文件」动作，不额外增加第三态。

## ADDED Requirements

### Requirement: 文本编码图片格式的分类判定

A file whose content is valid UTF-8 text SHALL be classified as an image when its extension belongs to the text-encoded image formats (currently SVG only). Classification for every other extension MUST remain unchanged.

#### Scenario: SVG 文本被判定为图片
- **WHEN** a `.svg` file whose content is valid UTF-8 text is opened in any file viewer entry point
- **THEN** it is classified as an image and routed to the image renderer instead of the source editor

#### Scenario: 非文本图片格式的文本文件不改变行为
- **WHEN** a file named with a binary image extension (for example `.png`) contains valid UTF-8 text
- **THEN** it is classified as text and opens in the source editor, exactly as before this change

#### Scenario: 二进制图片行为不变
- **WHEN** a `.png` file whose content is not valid UTF-8 is opened
- **THEN** it is classified as an image and rendered from its base64 payload, exactly as before this change

### Requirement: SVG 默认以图形呈现

Opening a `.svg` file SHALL render the image immediately, without requiring an extra user action, in every file viewer entry point.

#### Scenario: 打开即呈现图形
- **WHEN** a `.svg` file is opened in the desktop file tab, the dockview file panel, or the mobile file viewer
- **THEN** the rendered image is displayed immediately

#### Scenario: 图形按容器约束呈现
- **WHEN** the SVG's intrinsic size exceeds the available viewer area
- **THEN** the image is contained within the viewer without distorting its aspect ratio

### Requirement: 图片渲染必须使用 CSP 允许的 data: URL

Image rendering SHALL produce a `data:` URL. It MUST NOT depend on `blob:` URLs, because the desktop shell's content security policy permits only `'self'` and `data:` for `img-src`.

#### Scenario: 桌面外壳下渲染成功
- **WHEN** an SVG is rendered inside the desktop shell whose `img-src` is `'self' data:`
- **THEN** the image displays without a content security policy violation

#### Scenario: 文本内容按 UTF-8 编码为 base64
- **WHEN** the file content is valid UTF-8 text
- **THEN** the renderer encodes the UTF-8 bytes to base64 and builds `data:image/svg+xml;base64,...`

#### Scenario: 既有二进制图片路径不回归
- **WHEN** a binary image is rendered
- **THEN** its existing `data:${mime};base64,${content}` source is used unchanged

### Requirement: 图形呈现提供回到源码的入口

Every entry point that renders an SVG SHALL provide a `Show code` action, and the source state SHALL retain the existing editing, saving, downloading, and deleting behavior.

#### Scenario: 从图形切到源码
- **WHEN** the user activates `Show code` on a rendered SVG
- **THEN** the source editor opens with the file content, and a preview action is available to return to the image

#### Scenario: 源码态可编辑并保存
- **WHEN** the user edits and saves the SVG while in the source state
- **THEN** the save goes through the existing editor save path with no behavior change

#### Scenario: 切换状态不持久化
- **WHEN** the user switches an SVG to source and later reopens the same file
- **THEN** the file renders as an image again, because the toggle is session-local

### Requirement: 四个入口行为一致

All four file viewer entry points SHALL apply the same classification rule and the same image rendering path.

#### Scenario: 聊天 rich-output 块按同一规则呈现
- **WHEN** a chat rich-output file block references a `.svg`
- **THEN** the block renders the image instead of the truncated text preview

#### Scenario: 聊天块的源码入口
- **WHEN** the user needs the SVG source from a chat rich-output block
- **THEN** the existing open-file action leads to the full viewer, where `Show code` is available

#### Scenario: 非图片二进制文件保持既有提示
- **WHEN** a chat rich-output file block references a binary non-image file
- **THEN** the existing preview-unavailable copy is shown, unchanged

### Requirement: 移动端等价且可触控

On phone and coarse-pointer surfaces the SVG viewer SHALL provide the same capabilities with a touch target of at least 44 pixels, and MUST NOT introduce document-level horizontal overflow.

#### Scenario: 移动端触控尺寸
- **WHEN** the SVG viewer renders on a coarse-pointer surface
- **THEN** the `Show code` control has an active touch dimension of at least 44 pixels

#### Scenario: 移动端无横向溢出
- **WHEN** an SVG wider than the viewport is rendered on a phone
- **THEN** the image is contained within the viewer and the document does not scroll horizontally

### Requirement: 本次变更不得改变相邻行为

This change MUST NOT alter Changes-panel routing for `.svg` entries, Markdown embedded-image rendering, or any backend file-content response.

#### Scenario: Changes 面板行为不变
- **WHEN** a `.svg` entry is clicked in the Changes panel
- **THEN** it behaves exactly as before this change, and the diff-routing question is handled by a separate change

#### Scenario: 无后端改动
- **WHEN** this change is applied
- **THEN** no backend file-content response, API, or protocol changes

## Known Failure Modes

| Symptom | Cause | Fix |
| --- | --- | --- |
| 桌面端 SVG 预览空白，控制台报 CSP 违规 | Tauri 的 `img-src` 只允许 `'self' data:`，`URL.createObjectURL` 产生的 `blob:` URL 被拦截 | 改用 `data:image/svg+xml;base64,...`（见 `apps/desktop/src-tauri/tauri.conf.json:30`） |
| 名为 `.png` 的文本文件无法再在编辑器打开 | 把「扩展名判图片」无差别提到 `is_binary` 判定之前 | 只对文本编码图片格式白名单（当前仅 SVG）放行，不重排整体判定顺序 |
