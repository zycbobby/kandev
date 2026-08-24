<!-- Source: superpowers-bridge/templates/adopters/CLAUDE.md.fragment.md -->

## Workflow routing (read on session start)

This repo uses `superpowers-bridge` with the following rule:

**Keep OpenSpec as the front door.**  
Use Superpowers only as embedded capability inside OpenSpec stages.

### Entry routing

| Trigger | What to do |
|---|---|
| User starts a new feature / capability / architectural change | Use `/opsx:propose` so the change is created with `--schema superpowers-bridge` |
| User is already inside an active change | Use `/opsx:plan`, `/opsx:apply`, `/opsx:verify`, `/opsx:archive` |
| User is only discussing ideas narratively | You may use a proposal executor such as verbal `superpowers:brainstorming`, but write the outcome into `proposal.md` when the change is opened |
| User explicitly says bug fix / typo / tiny config tweak | Direct PR, no change |

### Hard rules

- Do not create `brainstorm.md`
- Do not create `plan.md`
- Do not create `retrospective.md`
- Do not write planning/design output to `docs/superpowers/specs/`
- Do not write planning output to `docs/superpowers/plans/`

### Archive policy

- Merge conservatively by capability
- Prefer existing `openspec/specs/<capability>/spec.md`
- Avoid creating new top-level capability folders unless truly necessary
