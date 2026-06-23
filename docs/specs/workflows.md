# Workflow Catalog

An index of the workflows defined in `definitions/workflows/*.yaml`. For how the
software-factory pipeline composes them, see `software-factory.md`. For the
field-by-field workflow schema, see `schemas.md`.

## Software-factory pipeline

The build pipeline, top to bottom (see `software-factory.md` for the wiring):

- **brainstorm** — Front-door interview that turns an idea into a spec and story
  cards. Pauses for human input through the approvals API.
- **implement_spec** — Top-level orchestrator. Decomposes a spec into components,
  builds each concurrently, integrates them in dependency order, then
  pushes/finalizes.
- **build_component** — Plans and builds one component in its own worktree
  (`plan_tasks` ↔ `review_plan` loop, then a ForEach of `implement_task`).
- **implement_task** — Implements one task with mechanically enforced TDD in a
  per-task worktree (write_tests → write_code → acceptance/quality review →
  dispute judge).
- **integrate_component** — Merges one component into the integration branch
  (mechanical merge; LLM conflict resolution only on conflict), then verifies.
- **verify_integration** — Integration + E2E tests against story acceptance
  criteria, then the release action.
- **debug** — Systematic root-cause debugging. Subworkflow of `implement_task`
  and `integrate_component`; also runnable standalone.
- **debug_merge** — Boundary-focused debugging for post-merge integration/E2E
  failures.
- **finish_branch** — Single-node release action (merge/pr/keep/discard).

## Utility

- **interview** — Structured Q&A between an interviewer and interviewee node.
- **learn** — ForEach of `interview` subworkflows then a synthesis node;
  post-run learning, outside the build pipeline.

## Porting / eval

- **initial_port** — One-shot port of a source repo's core abstractions plus
  tests into a target language.
- **semantic_port** — Looping workflow that tracks upstream commits and ports
  semantic changes incrementally, one commit per iteration.

## Test fixtures (not product workflows)

These exist only to exercise engine behavior in tests:
`handoff_smoke`, `handoff_smoke_serf`, `forced_debug_test`, and the
`forced_review_test` family (`forced_review_test`, `_dry`, `_downstream`,
`_old`).

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code._
