# Reference Doc Standard

The standard for docs under `docs/specs/`. These are **evergreen reference**
docs: each describes one subsystem or topic of Toil *as it works now*. The code
is the canonical source of truth; a reference doc is a navigable, verified
summary of it.

This is the contract every doc in this folder follows, and the brief any author
(human or agent) writing one must satisfy.

## What a reference doc is — and isn't

- **Is:** an accurate description of current behavior, structure, and surfaces of
  one topic, written for a developer or agent working in or against it.
- **Is not:** a design proposal, a plan, a changelog, or a dated snapshot. Those
  are point-in-time artifacts and belong in `docs/plans/` or
  `docs/superpowers/specs/`, never here. No "V1 / future / proposed" framing, no
  "we will…" — only what exists.

One topic per doc. Organize by topic and name files for the topic
(`runners.md`, `approvals.md`), not by a sequence number.

## Structure

Scale each section to the subsystem; omit sections that don't apply.

1. **Title + one-paragraph overview** — what this subsystem is and where it lives
   (the `internal/<pkg>` path).
2. **Concepts / data model** — the key types and what they represent, each tied
   to its package/type so a reader can jump to the code.
3. **Behavior / lifecycle** — how it works: the flow, the states, the rules.
4. **Surfaces** (where applicable) — the CLI commands, HTTP routes, event types,
   and config/env vars this subsystem exposes. Point at the authoritative
   source (`schemas.md`, `cli.md`, `api.md`, `logging-and-state.md`)
   rather than duplicating it.
5. **Cross-references** — related reference docs.

## Conventions

- **Cite the code.** For any load-bearing claim, name the package, type, or
  function so the claim is checkable (e.g. "approval records live under
  `runs/<id>/approvals/` — `internal/approvals`"). Citations are how the doc
  stays honest.
- **Cite symbols, not line numbers.** `file` or `file:Symbol` — never
  `file.go:120`, which drifts on every edit.
- **No fabricated counts or inventories.** If a number isn't determinate, omit it
  or describe the set instead.
- **Describe what's there, not what's missing or planned.** A gap is not a
  finding for a reference doc.
- **Be concise.** Favor the shortest accurate description.

## Verification & provenance

A reference doc is only correct if it survives an adversarial check against the
code — extract its claims, verify each against the source, fix what's wrong (the
`auditing-documentation` skill's Phase 3). After verification, the doc carries a
provenance marker at EOF:

```
---
<!-- doc-audit:last-reviewed -->
_Last reviewed: YYYY-MM-DD · commit `<sha>` · verified against code._
```

When the code changes, update the doc and re-stamp. The marker records when the
doc was last checked against reality, not when it was written.
