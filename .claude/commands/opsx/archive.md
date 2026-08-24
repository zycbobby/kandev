---
name: "OPSX: Archive"
description: Archive a completed change — validates, archives, and merges spec deltas conservatively by capability
---

Use the openspec-archive-change skill to archive the current change after implementation is verified. It should merge into existing `openspec/specs/<capability>/spec.md` first and avoid creating new top-level capability folders unless truly necessary. If an ADR is needed, the skill should let the user choose the decision-log directory (`dev-docs/decisions`, `docs/decisions`, or `doc/decisions`) and persist that repo preference in `openspec/config.yaml`.
