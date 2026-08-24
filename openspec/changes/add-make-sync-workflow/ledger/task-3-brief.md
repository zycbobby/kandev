# Task 3 brief: `scripts/sync-workflow.test.sh` + `test-scripts` wiring

## 3.1 新建 `scripts/sync-workflow.test.sh`

bash 脚本，仿 `scripts/make-deploy.test.sh` 的 `pass`/`fail` + 末尾 `exit $status` 风格（`set -euo pipefail`、`ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"`、`status=0`、`pass(){...}`、`fail(){...}`、末尾 `exit "$status"`）。覆盖三块：

(a) `make help` 输出 grep `^[[:space:]]+sync-workflow[[:space:]]` 命中；`make -n sync-workflow` 输出 grep `python3 scripts/sync-workflow.py` 命中。

(b) 起一个 Python3 桩服务器（heredoc，`http.server`，随机高位端口），路由：
- `/api/v1/workspaces` → `{"workspaces":[{"id":"ws-1","name":"Alpha","owner_id":"","task_prefix":"KAN"},{"id":"ws-2","name":"Beta","owner_id":"","task_prefix":"KAN"},{"id":"ws-3","name":"Empty","owner_id":"","task_prefix":"KAN"}],"total":3}`
- `/api/v1/workflows?workspace_id=ws-1&...` → `{"workflows":[{"id":"wf-1","name":"Kanban"},{"id":"wf-2","name":"PR Review"}],"total":2}`
- `/api/v1/workflows?workspace_id=ws-2&...` → `{"workflows":[{"id":"wf-3","name":"Kanban"}],"total":1}`
- `/api/v1/workflows?workspace_id=ws-3&...` → `{"workflows":[],"total":0}`
- `/api/v1/workflows/wf-1/export`、`wf-2`、`wf-3` → `version: 1\ntype: kandev_workflow\nworkflows:\n    - name: <name>\n      steps: []`（`Content-Type: application/x-yaml`）。

运行 `python3 scripts/sync-workflow.py <stub_url> <tmpdir>` 后断言：
- `<tmpdir>` 下恰好 `kanban.yml`、`pr-review.yml`、`kanban--beta.yml` 三个文件；
- 每个文件 grep `type: kandev_workflow` 且 `workflows:` 下只含一个 `- name:`；
- `ws-3`（空）不产出文件。

(c) 指向已关闭端口（如先起再杀）运行脚本 → 断言退出非 0 且 stderr 含 URL。

准出：`bash scripts/sync-workflow.test.sh` 全绿退出 0。

## 3.2 修改 Makefile `test-scripts` 目标

加一行 `@bash scripts/sync-workflow.test.sh`（与 `make-deploy.test.sh` 并列）。

准出：`make test-scripts` 中该行通过（或单独 `bash scripts/sync-workflow.test.sh` 通过）。

## 绑定约束

- 测试脚本只依赖 bash + python3（repo 既有依赖），不引入 curl/jq。
- 桩服务器用 Python3 `http.server`（heredoc 内嵌 handler），绑定随机高位端口，测试结束清理（kill + wait）。
- 断言文件名与排序：脚本按 workspace `id` 升序、workflow `id` 升序遍历；ws-1 Alpha(Kanban→`kanban.yml`, PR Review→`pr-review.yml`)、ws-2 Beta(Kanban→`kanban--beta.yml`)、ws-3 空。
- 失败路径(c) 用「先起一个 server 拿到端口、再杀、复用该端口跑脚本」或等价方式，确保端口确实关闭。
- 测试脚本须可被 `bash scripts/sync-workflow.test.sh` 直接运行，退出码 0 表示全过。
