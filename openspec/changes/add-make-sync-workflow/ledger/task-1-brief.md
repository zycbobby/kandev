# Task 1 brief: `scripts/sync-workflow.py`

## 1.1 新建 `scripts/sync-workflow.py`

仅用 Python3 stdlib（`json`/`re`/`os`/`sys`/`urllib.request`/`urllib.error`）。接口 `python3 scripts/sync-workflow.py <url> <out_dir>`。核心逻辑（伪码即契约）：

- `slug(name)`：`re.sub(r"[^a-z0-9]+", "-", name.lower()).strip("-")`，空则 `"workflow"`。
- `get(url, path) -> bytes`：`urlopen(url.rstrip("/")+path, timeout=10)`；`HTTPError` → `sys.exit("sync-workflow: backend at <url> returned HTTP <code> for <path>")`；其他异常 → `sys.exit("sync-workflow: backend unreachable at <url>: <e>")`。
- `main`：`os.makedirs(out_dir, exist_ok=True)`；GET `/api/v1/workspaces` → `["workspaces"]` 按 `id` 升序；对每个 workspace GET `/api/v1/workflows?workspace_id=<id>&include_hidden=true&exclude_office=false` → `["workflows"]` 按 `id` 升序；对每个 workflow GET `/api/v1/workflows/<id>/export`（`bytes` 原样写文件）。文件名：`base=slug(name)`，`used` 集合记录已用名，首次用 `base`，冲突用 `base--slug(workspace.name)`，再冲突追加 `--2/--3/...`；写到 `<out_dir>/<fn>.yml`。结束打印 `synced <count> workflow(s) to <out_dir>`。

准出：`python3 scripts/sync-workflow.py http://localhost:38429 /tmp/sync-smoke` 退出 0 且生成若干 `.yml`。

## 1.2 失败路径

`python3 scripts/sync-workflow.py http://localhost:1 /tmp/x` 退出非 0，stderr 含 `localhost:1`。

准出：上面命令 `echo $?` 非 0。

## 绑定约束（来自 spec delta 与 design）

- 列表请求必须带 `include_hidden=true&exclude_office=false`。
- 导出 body 原样落盘（YAML，不解析不改写）。
- 文件名唯一且可预测；跨 workspace 同名以 workspace 身份消歧（`base--slug(workspace.name)`）。
- 后端不可达 / 非 2xx：打印错误到 stderr 并退出非零，错误信息须含 base URL。
- 只写/覆盖，不删除 `out_dir` 下其它文件（backup 语义）。
