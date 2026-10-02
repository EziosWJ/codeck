# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Before exploring, read these

- **`CONTEXT.md`** at the repo root.
- **ADRs**: published as GitHub issues in this repo (ADR-0001 is issue #1). Read the ADRs that touch the area you are about to work in.

If any of these don't exist, **proceed silently**. Don't flag their absence; don't suggest creating them upfront. The `/domain-modeling` skill (reached via `/grill-with-docs`) creates them lazily when terms or decisions actually get resolved.

## File structure

Single-context repo:

```
/
├── CONTEXT.md          ← glossary: domain terms + the Chinese/API naming pairs
├── docs/agents/        ← this configuration
└── docs/deploy.md      ← deployment runbook
```

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `CONTEXT.md`. The glossary's `_Avoid_` lines matter here: the UI is Chinese while the API and database are English, and the glossary is what pins those pairs together. Don't drift to synonyms the glossary explicitly avoids.

If the concept you need isn't in the glossary yet, that's a signal: either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/domain-modeling`).

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than silently overriding:

> _Contradicts ADR-0001 (C1: HTTP 暴露面), but worth reopening because…_
