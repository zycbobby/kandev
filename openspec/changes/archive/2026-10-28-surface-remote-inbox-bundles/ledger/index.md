# surface-remote-inbox-bundles — Apply Ledger

Executor: inline-baseline（用户在会话重置指令中显式指定；任务链线性依赖 T1→T9，tasks.md 要求按序推进）。

环境备注（影响全部后端验证命令）：本 worktree 的 Go 工具链需 `GOTOOLCHAIN=go1.26.0 GOPROXY=off GONOSUMDB='*'`（本机 /usr/local/go 为 1.25.4，1.26.0 工具链已在 module cache，直连 proxy.golang.org 会挂起）；直接 go 命令统一加 `-trimpath` 共享构建缓存；`cd` 会触发 gvm chpwd 钩子返回非零，命令用 `;` 而非 `&&` 串联；golangci-lint 在 `~/go/bin`。

## Per-Task Ledger

| task | executor | 验证方法 | 结果 | 状态 | 时间戳 |
|---|---|---|---|---|---|
| T1 remoteinbox 客户端与聚合器 | inline-baseline | `go test -trimpath -count=1 ./internal/instances/remoteinbox/...`（12 用例：聚合排序/cap/has_more/降级四理由/respond 透传/sidecar 透传/PAT 注入/deadline/URL 处理）+ `lint-go-changed` 0 issues | PASS | done | 2026-10-28T12:55+08:00 |
| T2 backendapp 实例 inbox 路由 | inline-baseline | `go test -trimpath -count=1 -run TestInstanceInbox ./internal/backendapp/` 全绿 + 全包回归（9 个既有环境性失败经基线 stash 验证与本改动无关：macOS /private/var 符号链接解析与本地 git 材料化）+ `lint-go-changed` 0 issues | PASS | done | 2026-10-28T12:55+08:00 |
| T3 respondTarget 覆盖 | inline-baseline | `pnpm vitest run hooks/domains/session/use-clarification-group components/task/chat/clarification-{panel-section,input-overlay}.test`（110 用例含 4 新增：hook 覆盖 URL/batch body/skip/loss+409、PanelSection 透传、Overlay 透传、默认 URL 逐字节）+ `tsc --noEmit` exit 0（需 NODE_OPTIONS=--max-old-space-size=8192，本机内存压力下默认堆会段错误）+ eslint 0 警告 | PASS | done | 2026-10-28T12:47+08:00 |
| T4 API client 与类型 | inline-baseline | `pnpm vitest run lib/api/domains/remote-inbox-api.test.ts`（9 用例：local-base 解析/显式 baseUrl 优先/路径与标识符编码/respond body 透传/sidecar 三写/respondTarget builder）+ tsc exit 0 + eslint 0 | PASS | done | 2026-10-28T12:55+08:00 |
| T5 slice 与组合角标 | inline-baseline | `pnpm vitest run lib/state/slices/needs-you-inbox`（18 用例含 8 新增：远端页落地/跨实例 generation 隔离/degraded 清行同更新/tick 三通道/composed count-known/composed hasMore/实例枚举）+ tsc exit 0 + eslint 0 | PASS | done | 2026-10-28T12:58+08:00 |
| T6 远端 inbox controller | inline-baseline | `pnpm vitest run hooks/domains/needs-you-inbox`（26 用例含 8 新增：三重 gating/每实例一读/降级落态/relay 单实例 bump/非澄清事件不 bump/retry 单实例/慢响应只写本实例）+ tsc 0 + eslint 0 | PASS | done | 2026-10-28T13:10+08:00 |
| T7 UI section/行/角标 + i18n | inline-baseline | `pnpm vitest run components/needs-you-inbox app/needs-you-inbox`（106）+ sidebar/navigation 域（75）+ `pnpm run i18n:check` exit 0（7 语言 + pseudo，无 em dash）+ tsc 0 + eslint 0 | PASS | done | 2026-10-28T13:15+08:00 |
| T8 E2E | inline-baseline | Managed host E2E, 3 tests passed, covering list, count, jump, answer, remote snooze and restore, degraded/recovery, disabled inbox, 502 retaining selection and retry, and competing answer convergence. Commits `0b8b8dbe7` and `a9e001a396`. | PASS | done | 2026-10-28T14:31+08:00 |
| T9 decision and specs | inline-baseline | Commit `f0a141d57`; list-docs validate, lint-spec-files, i18n:check and strict OpenSpec validation passed. OpenSpec telemetry failed to reach its network endpoint after successful validation. | PASS | done | 2026-10-28T14:28+08:00 |
| Drift remediation (3 findings) | inline-baseline | Affected vitest suites green (slices/controller/settings 63; inbox row + clarification chain 189; inbox components 79); `tsc` exit 0; changed-file eslint 0 warnings after extracting `rowSidecarActions` (cognitive complexity back under 20); e2e `remote-inbox.spec.ts` re-run 3/3 green (44.2s) including the new degrade → restart → retry → draft-restored assertions | PASS | done | 2026-10-28T15:40+08:00 |
| Workflow-move doc alignment | inline-baseline | ADR Decision 3 unified-notifications Revision + master-spec 提醒方式限定 annotation + proposal decision hand-off; `list-docs.py validate` (395/1679), `lint-spec-files.py --all`, `openspec validate --strict` all pass | PASS | done | 2026-10-28T15:40+08:00 |
| Verify drift remediation (hidden-route double fan-out) | inline-baseline | `pageRoute` selects one read by the `hidden` flag instead of `ReadInbox` + conditional `ReadHidden` overwrite (design bound: 1+W dials per request); fake forwarder gained read counters, new `TestInstanceInboxPageRouteIssuesSingleFanOut` asserts inbox/hidden routes each issue exactly one read. `go test -trimpath -count=1 ./internal/backendapp/` green (56.6s, TMPDIR=/private/tmp) + `lint-go-changed` 0 issues | PASS | done | 2026-10-28T17:12+08:00 |

### Drift remediation notes (2026-10-28 afternoon session, resumed after reset)

- **Badge keeps a landed page during refresh**: composed count/hasMore selectors now key on `lastAppliedOk` instead of `status === "ready"`, so a refresh (status regresses to loading) keeps the previously landed page contributing until a replacement lands; a degrade still drops it (page cleared, count follows).
- **Registry changes reconcile shell state**: InstancesSettings writes registry mutations through to the shared instances slice (`upsertInstance`/`removeInstance`, enabled edits bump the instance's remote refresh tick); `useRemoteInboxController` calls `retainRemoteInboxInstances` on every registry effect, disposing removed/disabled instance pages, fencing their in-flight reads via a generation bump, and dropping their answer drafts.
- **Answer drafts survive degradation**: remote rows persist entered answers per (instance, pendingId) in `remoteDraftsByInstanceId` via `setRemoteInboxDraft`; `ClarificationPanelSection`/`ClarificationInputOverlay` gained additive `initialAnswers`/`onAnswersChange` pass-through; drafts clear on resolved/no-longer-active outcomes; local rows are unaffected (the draft callback no-ops without an instanceId). e2e asserts selection survives degrade → restart → retry.

### Workflow-move alignment (one-time instructions, 2026-10-28)

Terminal-state revision applied: 「远端安静、仅侧边栏」no longer written as a terminal contract anywhere this change authorizes. ADR Decision 3 Amendment rewritten + Revision added (one notification logic for local and relayed events, origin-tagged occurrences into `internal/notifications`, sidebar module removed at end state, relay store = dedup/recovery only, realized by follow-up needs-you-inbox-waiting-rows REQ-UI-NEEDS-YOU-INBOX-002~004); `openspec/specs/cross-instance-notifications` 「提醒方式限定」annotated as current-behavior-not-terminal; proposal.md 不触碰 wording reframed and a 「后续叠加变更预留（决策交接）」 section records the three settled decisions (backend fan-out vs SPA composition; remote rows count toward the actionable badge; instance-wide workspace aggregation) plus the kind-discriminated extension of the remoteinbox read path onto the follow-up's aggregate endpoint.

### T1/T2 实现备注（设计微调，均不构成契约变更）

- **hidden 聚合 total 语义**：design.md「hidden 聚合 total=min」按求和实现（Σ 各 ws 的 workspace-wide total）。min 无可成立的语义（会少报单 ws 已知的隐藏数）；spec delta 未约束该字段。`next_snooze_expiry` 按设计取 MIN。
- **任一 ws 读失败 = 实例页整体降级**（首失败理由胜出），不渲染部分页——与「降级 = 实例 section 级」一致。
- **credential 读取失败**（本地 secret store 不可用）映射为 `unauthorized`：与「PAT 失效」同属降级文案指向登记 PAT 的路径。
- **T2 文件集偏差**：除列出的文件外，`instance_proxies.go` 新增 `ProxyManager.Credential(inst)`（routeParams 无法触达 secretadapter，ProxyManager 是 Services 上唯一的持有者）；`instance_routes.go` 的 404 文案改用共享常量（goconst）。均在该 task 意图范围内。
- **路由端为每行 bundle 强制盖 `instance_id` 章**，不信任 forwarder 传入值（spec「远端行不落本地表」场景的标识不变量）。
- **workspaces 404 ≠ inbox_disabled**：workspaces 端点早于 inbox 面存在，404 映射 `error`；仅 inbox 组 404 映射 `inbox_disabled`。

## Exit Gate

- Verification Strategy (main entry, e2e-script): `pnpm e2e:run -- tests/multi-instance/remote-inbox.spec.ts` green after drift remediation — 3 tests / 44.2s covering 图1-7 (list/jump/answer/snooze-restore/degrade-recovery/needs-you-inbox-off), answer-retryable-after-disconnect now also asserts the draft survives degradation (degraded section, overlay unmounted), and after remote restart + retry the re-expanded row restores the entered selection; competing-answer convergence unchanged. The host backend build needs Go on PATH (`PATH=/usr/local/go/bin GOTOOLCHAIN=go1.26.0 GOPROXY=off`); a bare PATH fails with `go: Command not found` and `| tail` can mask the failure — check the ELIFECYCLE line, not the pipe exit.
- Verification Strategy (second entry, integration-test): backend `remoteinbox` + backendapp suites green at T1/T2 and no backend code changed since; after remediation, affected frontend suites green — needs-you-inbox + instances slices/controller/settings 63, inbox row + clarification chain 189, inbox components 79; `tsc --noEmit` exit 0; changed-file eslint 0 warnings (cognitive complexity back under 20 via `rowSidecarActions` extraction); no new copy, so i18n catalogs unchanged.
- Strict OpenSpec validation: `openspec validate surface-remote-inbox-bundles --strict --no-interactive` → valid. `list-docs.py validate` (395 decisions / 1679 specifications) and `lint-spec-files.py --all` pass after the alignment edits.
- Evidence & acceptance method: six-requirement mapping audit recorded in tasks.md (组合读→T1/T2/T7 图1; 就地回答→T3/T8 图2 + competing-answer; sidecar 转发→T1/T2/T8 图6; 计数→T5/T7/T8 图3; 降级→T1/T2/T7/T8 图5/7; 跳转→T7/T8 图4; 双 flag 惰性→T2/T6 + 既有 flag-off e2e); data-isolation final grep clean — `remoteinbox` imports only stdlib HTTP (no sql/store), forward routes have no repository writes, remote rows exist only in `remoteByInstanceId`/`remoteDraftsByInstanceId` view state, and the local sidecar APIs appear only in the local-row branch of `rowSidecarActions`.
- Earlier exit-gate history (superseded but retained): E2E + backend + frontend suites green; workflow-service sweep leak fixed by `7c20bda615` fixture cleanup; full `internal/instances/...`/`internal/backendapp/` passed with `TMPDIR=/private/tmp`. E2E commit-hook receipt: parent `0b8b8dbe764`, commit `a9e001a396`.
- Exit: **closed**. All implementation tasks and verification checklist items checked; drift findings remediated and re-verified; workflow-move doc alignment applied. Ready for review; do not archive before review convergence.
