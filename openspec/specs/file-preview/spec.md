# file-preview Specification

## Purpose

文件查看器对文件类别的判定规则，以及图片类文件的呈现与源码回切行为。判定以「内容是否为合法 UTF-8」为基础，并额外承认**文本编码图片格式**（当前仅 SVG）：这类文件内容是合法文本，却应走图片渲染路径。图片一律经 `<img>` + `data:` URL 呈现，源码态由既有编辑器承载，并提供 `Show code` 与预览之间的往返。规则在桌面 tab、dockview 面板、移动端查看器与聊天 rich-output 文件块四个入口共享同一实现。

## Requirements
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

