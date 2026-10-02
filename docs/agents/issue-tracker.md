# Issue tracker: GitHub

Issues and specs for this repo live as GitHub issues in `EziosWJ/codeck`. Use the `gh` CLI for all operations.

## Conventions

- **Create an issue**: `gh issue create --title "..." --body "..."`. Use a heredoc for multi-line bodies.
- **Read an issue**: `gh issue view <number> --comments`, filtering comments by `jq` and also fetching labels.
- **List issues**: `gh issue list --state open --json number,title,body,labels --jq '[.[] | {number, title, labels: [.labels[].name]}]'` with appropriate `--label` and `--state` filters.
- **Comment on an issue**: `gh issue comment <number> --body "..."`
- **Apply / remove labels**: `gh issue edit <number> --add-label "..."` / `--remove-label "..."`
- **Close**: `gh issue close <number> --comment "..."`

## Blocking edges

Follow the precedent set by ADR-0001 (#1) → C1–C8 (#2–#9): each ticket lists its blockers in a `## 依赖` (or `## Blocked by`) section referencing the blocking issue numbers. Native GitHub issue dependencies are used when the API accepts them; otherwise the text reference is authoritative.

## ADRs

Architecture Decision Records in this repo are published as GitHub issues (ADR-0001 is #1), not as files under `docs/adr/`. A ticket tracing back to an ADR names it in its body.

## Pull requests as a triage surface

**PRs as a request surface: no.**

## When a skill says "publish to the issue tracker"

Create a GitHub issue.

## When a skill says "fetch the relevant ticket"

Run `gh issue view <number> --comments`.
