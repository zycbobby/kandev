# superpowers-bridge Schema

[English](./README.md) · [繁體中文](./README.zh-TW.md)

> Vendored bridge schema for projects that want to **keep OpenSpec as the
> workflow front door** while **embedding Superpowers capabilities** inside
> selected stages.

## Design intent

This vendored variant follows four rules:

1. **OpenSpec remains primary**
   - Change creation: `openspec new change`
   - Artifact progression: `openspec instructions ...`
   - Validation: `openspec validate`
   - Archive: `openspec archive`

2. **Superpowers is embedded, not a replacement**
   - Each OpenSpec stage may choose a stage-specific executor
   - `proposal` may use proposal executors: `superpowers:brainstorming`, `grill-with-docs`, or inline direct/clarify
   - `tasks` / `design` may use planning executors such as `superpowers:writing-plans`
   - `apply` may use implementation executors such as worktrees / subagents / TDD / code review
   - But all outputs still land in OpenSpec-native locations

3. **Artifact set stays close to stock OpenSpec**
   - `proposal.md`
   - `design.md`
   - `specs/**/*.md`
   - `tasks.md`
   - `verify.md`

   This vendored schema intentionally does **not** use extra bridge artifacts
   such as `brainstorm.md`, `plan.md`, or `retrospective.md`.

4. **Apply remains OpenSpec-native**
   - Implementation progress stays driven by `tasks.md`
   - Superpowers capabilities enhance execution quality without replacing the lifecycle

## Stage-specific executors

A stage-specific executor is the method chosen to complete one OpenSpec stage while keeping that stage's native artifact as the source of truth. Proposal executors shape intent and scope; planning executors produce `design.md` / `tasks.md`; apply executors drive implementation from `tasks.md`.

## Runtime model

### Proposal

- OpenSpec artifact: `proposal.md`
- Proposal executors: `superpowers:brainstorming`, `grill-with-docs`, `inline-clarify`, or `inline-direct`
- Rule: executor output is written directly into `proposal.md`

### Design + Tasks

- OpenSpec artifacts: `design.md`, `tasks.md`
- Optional embedded capability: `superpowers:writing-plans`
- Rule: no extra `plan.md`; planning output is written directly into
  `design.md` / `tasks.md`

### Apply

- OpenSpec remains the controlling stage
- Recommended embedded capabilities:
  - `superpowers:using-git-worktrees`
  - `superpowers:subagent-driven-development`
  - transitive TDD / code review support when available
- Progress remains driven by `tasks.md`

### Verify

- OpenSpec artifact: `verify.md`
- Checks:
  - `openspec validate --all --json`
  - task completion
  - delta spec sync state
  - design / spec / implementation coherence
  - OpenSpec-native artifact coherence

### Archive

- OpenSpec mechanism: `openspec archive -y`
- Policy: merge deltas conservatively by capability
- Prefer updating existing `openspec/specs/<capability>/spec.md`
- Avoid creating new top-level capability folders unless truly necessary

## Installed files

This vendored schema ships:

- `schema.yaml`
- templates for:
  - `proposal`
  - `design`
  - `specs`
  - `tasks`
  - `verify`
- adopter CLAUDE fragments

It does **not** ship templates for removed extra artifacts.

## Adopter guidance

If you use the adopter CLAUDE fragment, the repo should route work like this:

- narrative idea discussion → optional proposal executor (`superpowers:brainstorming`, docs-aware grilling, inline clarification/direct) → `/opsx:propose`
- active change → `/opsx:plan` / `/opsx:apply` / `/opsx:verify` / `/opsx:archive`
- bug fix / typo / tiny config tweak → direct PR, no change

## Vendored-source note

This copy is intentionally adapted for `openspec-my`. It is not a verbatim
mirror of upstream `openspec-schemas`. The shipped behavior in this repository
is defined by:

- `schema.yaml`
- `templates/*`
- bundled opsx skills / commands

If those files disagree with older upstream docs, **follow the vendored files in
this repository**.
