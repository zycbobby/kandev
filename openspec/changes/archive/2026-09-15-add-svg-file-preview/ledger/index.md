# Apply Ledger: add-svg-file-preview

> 由 `openspec-apply-change` 在 apply 全程维护，位置 `openspec/changes/<change-id>/ledger/index.md`。
> 与 `reviews/` ledger 同级，随 change 一并归档。markdown 形态，人类可读、跨 agent。

## Per-Task Ledger

> 时间戳说明：`~` 前缀为按文件 mtime 回溯的近似时间，非精确记录。

> 每条 task 到达终态（done / rolled-back）时追加一行。
> `executor`：本次所用执行器（用户所选；无人值守自动回退时记 `auto`）。

| task id | executor | 验证方法 | 结果 | 状态 | 时间戳 |
|---------|----------|----------|------|------|--------|
| 1.1 | inline | 单测 `pnpm test -- file-types` | pass | `done` | 2026-09-15 17:28 |
| 1.2 | inline | 单测覆盖四条分支（svg 文本→image / png 文本→text / png 二进制→image / zip→binary） | pass | `done` | 2026-09-15 17:28 |
| 1.3 | inline | 单测：多字节 UTF-8 往返、空串、超单块阈值内容 | pass | `done` | 2026-09-15 17:28 |
| 1.4 | inline | 单测：文本走 base64 编码、二进制原样透传 | pass | `done` | 2026-09-15 17:28 |
| 1.5 | inline | `cd apps/web && pnpm test -- file-types` | 16 passed / 1 file；既有 previewKind 断言未改动 | `done` | 2026-09-15 17:28 |
| 2.1 | inline | 单测：二进制/文本两路 src 断言 | pass | `done` | 2026-09-15 17:29 |
| 2.2 | inline | 单测：未传 handler 不渲染、传入后可点击 | pass | `done` | 2026-09-15 17:29 |
| 2.3 | inline | 单测 `getByTestId("image-viewer")` 取到 `<img>` | pass | `done` | 2026-09-15 17:29 |
| 2.4 | inline | `pnpm test -- file-image-viewer file-editor-panel.image` | 8 passed / 2 files；既有 image 面板用例不回归 | `done` | 2026-09-15 17:29 |
| 3.1 | inline | 单测：切换、identity 变化重置、非文本图片 disabled | pass | `done` | 2026-09-15 17:30 |
| 3.2 | inline | `pnpm test -- use-text-image-source-toggle` | 5 passed / 1 file | `done` | 2026-09-15 17:30 |
| 4.1 | inline | monaco 工具栏单测：html/markdown 标签与 testid 不变；`previewKind=none` 仍不渲染 | pass | `done` | 2026-09-15 17:35 |
| 4.2 | inline | codemirror 同构单测：markdown 标签 + source action | pass | `done` | 2026-09-15 17:35 |
| 4.3 | inline | `pnpm typecheck` 验证透传链路类型闭合 | pass | `done` | 2026-09-15 17:35 |
| 4.4 | inline | `pnpm test -- components/editors` | 92 passed / 18 files | `done` | 2026-09-15 17:35 |
| 5.1 | inline | 组件测试：默认图形 / 切源码后出现 `svg-preview-toggle` / `.png` 文本仍进编辑器 | pass | `done` | 2026-09-15 ~17:47 |
| 5.2 | inline | 组件测试：dockview 面板两态 + 二进制图不给 source 入口 | pass | `done` | 2026-09-15 ~17:47 |
| 5.3 | inline | 组件测试：移动端两态 + 回切按钮 `h-11 w-11`（44px）断言 | pass | `done` | 2026-09-15 ~17:47 |
| 5.4 | inline | `pnpm test -- file-tab-content mobile-file-viewer-panel file-editor-panel` | 33 passed / 5 files | `done` | 2026-09-15 ~17:47 |
| 7.1 | inline | 写入 en/pt-pt/zh-cn 并 JSON 解析校验 | `Preview SVG` / `Pré-visualizar SVG` / `预览 SVG` | `done` | 2026-09-15 17:38 |
| 7.2 | inline | `pnpm run i18n:zh-hant` | zh-hk / zh-tw 生成 `預覽 SVG`，residual-simplified=0 | `done` | 2026-09-15 17:38 |
| 7.3 | inline | `pnpm run i18n:check`（含 U+2014 检查） | exit 0；复用既有 `task:showCode`（5 locale 已在） | `done` | 2026-09-15 17:39 |
| 6.1 | inline | 单测：SVG→img、二进制非图片→原文案、文本→`<pre>`、二进制图片→原 payload | pass | `done` | 2026-09-15 ~17:44 |
| 6.2 | inline | `pnpm test -- file-preview-block` | 4 passed / 1 file | `done` | 2026-09-15 ~17:44 |
| 9.1 | inline | 新增 requirements + system-design 一对，覆盖判定规则 / CSP 与编码 / 四入口 / 移动端 / non-goals | pass | `done` | 2026-09-15 ~17:53 |
| 9.2 | inline | `python3 scripts/lint-spec-files.py --all` | All specification files passed | `done` | 2026-09-15 ~17:53 |
| 8.1 | inline | 桌面 spec `e2e:run -- tests/task/svg-file-preview.spec.ts`（3 用例） | 3 passed / EXIT=0 | `done` | 2026-09-15 ~18:05 |
| 8.2 | inline | 移动端 spec `e2e:run --project mobile-chrome -- tests/task/mobile-file-viewer.spec.ts` | 14 passed / EXIT=0（12 条既有用例无回归 + 2 条新增 SVG 用例） | `done` | 2026-09-15 ~18:08 |
| 10.1 | inline | `pnpm lint`（--max-warnings 0）+ `pnpm typecheck` | 两者均 exit 0；lint 首轮 6 条警告（复杂度 17/上限 15、CodeMirrorToolbar 103 行/上限 100、4 处重复字面量）已通过抽取 `resolveTabView` 与提升 props 类型修掉 | `done` | 2026-09-15 ~18:02 |
| 10.2 | inline | `/mobile-parity` 复核 | 见下方「移动端契约」；无待办 | `done` | 2026-09-15 ~18:11 |
| 8.3 | inline | 新增 `e2e/tests/chat/rich-output-svg-preview.spec.ts`（rich-output 块渲染 SVG + 打开文件进查看器） | 1 passed / EXIT=0；补齐验证清单路径 4 的端到端覆盖 | `done` | 2026-09-15 ~18:11 |
| — | inline | 全量 `pnpm test`（1901 个测试文件） | `16297 passed \| 4 skipped`，耗时 689s；2 个文件收集期失败属环境问题（Node v22.12.0 无 `node:sqlite` 内置模块，仓库需 Node 24），与本 change 无关 | `done` | 2026-09-15 ~18:21 |

## E2E 调试记录

桌面 spec 共经历 4 轮失败。**四轮全部定位为用例缺陷，未发现产品缺陷**；产品代码在本轮唯一改动是为源码面板补了一个 `data-testid`。

| # | 现象 | 定位手段 | 根因 | 结论 |
|---|------|---------|------|------|
| 1 | 3 个用例全部 `element(s) not found`，均卡在 `file-tree-node` 定位 | Playwright 失败截图 | 素材写成 `assets/xxx.svg`，而桌面文件树默认折叠目录，该节点根本不在 DOM 中 | 用例缺陷：改为仓库根目录 + 每用例唯一文件名（同一 worker 共享 seed 仓库，重复路径的第二次 commit 无内容可提交） |
| 2 | `toBeDisabled()` 超时 | 在 spec 内临时挂 `page.on("websocket")` 帧日志 | 未发出任何 `workspace.file.update` 帧 —— `getByRole("button", { name: /Save/ })` 在任务页并不唯一（Changes 面板头也有 Save），`.first()` 点到了别的按钮 | 用例缺陷：改为在面板内定位；同时给源码面板补 `data-testid="file-source-panel"` 作为稳定锚点 |
| 3 | 保存成功后返回图形，`naturalWidth === 0` | 临时探针读出 `<img>` 的 `src` 并 base64 解码 | `keyboard.type()` 与 Monaco 自动闭合冲突，内容被写成 `<svg xmlns="http:www.w3.org2000svg" ...>>`（`/` 被吞） | 用例缺陷：改用仓库既有写法 `keyboard.insertText()` |
| 4 | 同上，仍为 `naturalWidth === 0` | 同上探针 | `insertText` 保住了 `/`，但 Monaco 仍会为首个 `<` 自动补一个 `>`，内容成为 `<svg .../>>` —— 确为非法 XML，`complete:true / naturalWidth:0` 是**正确**渲染结果 | 用例缺陷：编辑后的文档本就不保证是良构 XML。断言改为「状态往返 + 内容留存」，不再要求手工编辑后的文档必须解码成功；非法 SVG 渲染为破图并保留源码入口，本就是 design 中登记的失败模式 |

第 4 条同时构成对 design「Failure and recovery」中「Content is not a valid SVG」一条的真实覆盖。


## 移动端契约（task 10.2，/mobile-parity 复核）

| 项 | 结论 |
|----|------|
| 桌面结论 / 移动入口 | 同一结论（看见图形、可回源码）。入口复用既有 Files → 点文件 → `MobileFileViewerPanel`，未新增导航 |
| 最近已上线范例 | `MobileFileViewerHeader` 既有的预览控件（`html-preview-toggle` / `markdown-preview-toggle`）。复用了它的几何（`h-11 w-11 cursor-pointer p-0`）、header 动作行位置、以及「图标 + `aria-label`」形态 |
| 信息层级 / 主操作 | 图形是主内容；`Show code` 位于查看器 header 动作行，与下载、外链并列 |
| 呈现形态 | 内联在既有全高查看器面板内，未新增表面 |
| 表面理由 | 单文件预览内容浅、时效短，全高聚焦查看器已存在且匹配任务频率；改用抽屉会为同一内容多造一层表面 |
| 滚动归属 | 唯一滚动容器是图形容器（`flex-1 overflow-auto`），外层 `PanelBody ... overflow-hidden` 不滚动；文档级无横向溢出 |
| 触控尺寸 | `Show code` 为 `h-11`（<640px 44px）/ `sm:h-8`，与本 header 既有下载控件同式；回切控件为 `h-11 w-11`（44×44），与既有预览控件同式 |
| 共享逻辑 | 判定（`resolveFileViewerKind`）、切换 hook、动作描述符均与桌面共用；仅呈现不同（移动端源码态无工具栏，回切控件落在 header） |
| 移动端 Playwright | 扩展 `mobile-file-viewer.spec.ts`（`mobile-` 前缀自动路由到 mobile-chrome）：图形渲染并解码、文档无横向溢出、两个控件均 ≥44px、两态往返 |

**与任务原文的两处偏差（均为实现层，不改变对外行为）**

1. task 5.3 写「源码态透传 `sourcePreviewAction`」。移动端源码态渲染的是只读 `FileViewerContent`（裸 CodeMirror，没有工具栏），无可透传的目标；因此回切控件改由移动端 header 承载，但描述符、label、testid 与桌面完全共用。
2. `Show code` 采用 `h-11 … sm:h-8`（基类为触控尺寸、断点内收窄），与 skill 建议的「基类为 fine-pointer」写法相反；但这是本 header 既有下载控件的成文写法，且各断点实际尺寸正确（<640px 得 44px，≥640px 得 32px）。与同 header 已上线控件保持一致优先。


## Exit Gate

> 由 Step 4 在宣告完成前填写；三项必须全部满足，否则不得宣告完成或进入 verify / archive。

- [x] **Verification Strategy 入口通过**
  - 桌面：`cd apps/web && pnpm e2e:run -- tests/task/svg-file-preview.spec.ts` — 结果：`3 passed (14.6s)`，EXIT=0
  - 移动端：`cd apps/web && pnpm e2e:run --project mobile-chrome -- tests/task/mobile-file-viewer.spec.ts` — 结果：`14 passed (49.7s)`，EXIT=0（12 条既有用例 + 2 条新增 SVG 用例）
  - 补充端到端：`pnpm e2e:run -- tests/chat/rich-output-svg-preview.spec.ts` — 结果：`1 passed`，EXIT=0
  - 单测入口：`pnpm test -- file-types` — 结果：`16 passed`
  - 旁证：`pnpm lint` exit 0（--max-warnings 0）；`pnpm typecheck` exit 0；`pnpm run i18n:check` exit 0；`python3 scripts/lint-spec-files.py --all` → All specification files passed
  - 全量单测：`pnpm test` — 结果：`16297 passed | 4 skipped`；2 个 `e2e/scripts/kubernetes-*` 文件收集期失败，因本机 Node v22.12.0 缺 `node:sqlite` 内置模块（需 Node 24），与本 change 无关
- [x] **`openspec validate add-svg-file-preview --strict` 通过** — 结果：`Change 'add-svg-file-preview' is valid`
- [x] **验收方法与证据**
  - 方法：以真实浏览器端到端为验收主体（design 已论证本 change 的两个核心风险只有真实渲染管线能证伪），单测覆盖纯函数判定与编码；另有 lint / typecheck / i18n / spec lint 作为静态旁证
  - 场景：
    - 桌面路径 1：打开 `.svg` → 图形立即呈现（断言 `naturalWidth > 0`，证明真的解码成功而非碎图）→ `Show code` 进源码 → `Preview SVG` 回到图形
    - 桌面路径 2：源码态编辑 → 保存按钮由可用转禁用（脏标记清除）→ 状态往返后编辑内容仍在
    - 桌面不回归 B：名为 `.png` 的 UTF-8 文本文件仍进源码编辑器，且不出现 `Show code`
    - 移动端路径 3：图形渲染并解码 → `Show code` 触控尺寸 ≥44px → 文档无横向溢出 → 两态往返，回切控件亦 ≥44px
    - 聊天路径 4：rich-output 块内 SVG 渲染为图形（`naturalWidth > 0` 且不含 `<svg` 源码文本）→ 既有打开文件动作进入查看器并同样渲染
    - 回归 A：`markdown-preview-toggle` / `html-preview-toggle` 的标签、testid、禁用态与信任提示由单测逐项断言；移动端既有 markdown 预览 e2e 一并全绿
    - 边界：空串、多字节 UTF-8、超单块阈值内容编码不抛错（单测）
  - 结果：上述全部通过；`tasks.md` 42 条 task 全部勾选。E2E 共经历 4 轮失败，逐条定位后**均为用例缺陷**（详见「E2E 调试记录」），产品代码唯一相关改动为补一个 `data-testid` 锚点。
