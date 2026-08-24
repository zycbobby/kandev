# Task 2 brief: Makefile `sync-workflow` target

## 2.1 修改变量区

在 `Makefile` 变量区加 `URL ?= http://localhost:38429`。

## 2.2 加 target（`deploy` 目标附近）

```make
.PHONY: sync-workflow
sync-workflow:
	$(call phase,Syncing workflows from runtime)
	@python3 scripts/sync-workflow.py "$(URL)" "$(CURDIR)/workflows"
	$(call success,Workflows synced to $(CURDIR)/workflows)
```

## 2.3 加 help 条目（"Service Commands" 段）

```
sync-workflow                Export all runtime workflows into workflows/ (one file per workflow)
sync-workflow URL=http://localhost:38429  Backend base URL override
```

准出：`make help` 列出 `sync-workflow`；`make -n sync-workflow`（默认 URL）打印 `python3 scripts/sync-workflow.py "http://localhost:38429" "..."`。

## 绑定约束

- 变量用 `?=`（可被命令行 `URL=` 覆盖）。
- target 必须 `.PHONY`，放在 `deploy` 目标附近。
- help 条目加入 "Service Commands" 段（`@echo "Service Commands:"` 之后，`Build Commands:` 之前）。
- 缩进与既有 target/help 条目一致：recipe 行用 TAB；help 用两个空格 + 目标名 + 对齐空格。
- 不改后端、不改其它 target 行为。
