# Software Factory Architecture

This document describes Toil's software-factory pipeline as it actually ships:
the workflow hierarchy that turns a spec into merged, integration-tested code.
It supersedes the previous workflow catalog in workflows.md.

Everything here is defined in `definitions/workflows/*.yaml` and
`definitions/runners/*.yaml`. The pipeline has no special engine support beyond
the generic primitives (`kind: role`, `kind: subworkflow`, `kind: emit`,
`for_each`, decision edges, meta-decision edges). Workflows are data; the engine
is the same for the software factory as for the porting and utility workflows.

## Design Principles

Drawn from Brooks' Surgical Team model and refined through practice.

**The doer never decides they're done.** Reviewer and verifier nodes validate
completion. Agents do not self-assess. The workflow graph mechanically enforces
quality gates — e.g. in `implement_task` the test-writer and code-writer never
emit "approved"; `verify_code_meets_acceptance_criteria` and
`review_code_quality` do.

**Nodes are smart, edges are dumb.** Agents make decisions using LLM judgment
and emit a decision string. Edges route on that string. There is no complex
logic in the workflow engine — routing is `from`/`to`/`when` triples plus a
small set of synthetic meta-decisions (`_loop_exhausted`, `_timeout`,
`_retry_exhausted`).

**Judgment out of the agents.** Where possible, process control comes from
workflow structure and mechanical limits (loop caps via `max_loop_iterations`,
decision edges, `_loop_exhausted` escalation) instead of relying on agent
judgment. Agents decide _what_ to build; the graph decides _what happens next_.

**Roles are inline, not a separate registry.** There are no role definition
files. A `kind: role` node carries its full prompt inline as `prompt:` and names
its model harness via `runner:` (plus optional per-node `runner_env:`). The same
agent behavior can be reused by copying the prompt; identity and process both
live in the workflow YAML.

**Self-contained prompts.** Agents running in Toil do not have access to
Superpowers skills or other external prompt libraries. Every instruction an agent
needs is in its node prompt. Prompts use `<!-- LOCAL -->` / `<!-- /LOCAL -->`
markers to split the attempt-specific instructions (the part the dashboard shows
inline) from the reusable role boilerplate (suppressed behind a "show role
prompt" strip — see `internal/document/prompt.go`). The markers are a
presentation aid; the whole prompt is sent to the runner.

**Star topology communication.** Within a subworkflow, agents do not talk to
each other directly. State flows through node outputs and `${node.X...}`
references; the workflow graph is the communication topology. Cross-node handoff
data is carried on edges via `passes:` overlays and on `kind: emit` aggregator
nodes.

## Architecture Overview

Three levels of hierarchy, with `implement_spec` at the top:

```
Spec level:    implement_spec (orchestrator)
                 scope_components  (shell)   decompose spec -> components
                 ensure_repo       (shell)   bare repo / worktree substrate
                 ForEach component -> build_component        (concurrent)
                 ForEach component -> integrate_component     (sequential by depends_on)
                 push / finalize / cleanup   (shell)

Component level: build_component (per component, in its own worktree)
                   create_worktree (shell)
                   plan_tasks  <-> review_plan      (serf, plan/review loop)
                   ForEach task -> implement_task    (concurrent, depends_on DAG)
                   commit_component (shell)

Task level:    implement_task (per task, version 2 — own worktree)
                 create_task_worktree (shell)
                 write_tests (RED, serf)
                 write_code  (GREEN, serf)
                 verify_code_meets_acceptance_criteria  (serf, context: fresh)
                 review_code_quality                    (serf, context: fresh)
                 resolve_review_dispute (judge, serf, context: full)
                 debug_task_failure -> debug subworkflow
                 merge_task_worktree / cleanup_task_worktree (shell)
```

Supporting subworkflows: `verify_integration` (called by `integrate_component`),
`debug` and `debug_merge` (systematic debugging), `finish_branch` (release
action). Standalone front-door workflows: `brainstorm` (idea -> spec + stories).
Utility workflows: `interview` / `learn` (post-run learning). Eval workflows:
`initial_port`, `semantic_port` (porting subsystem, not part of this pipeline).

### Workspace Strategy

Parallelism requires workspace isolation, implemented with git worktrees created
by **shell** nodes, never by LLM agents. Branch and worktree names are derived
from component and task IDs.

- `implement_spec` establishes the repo substrate with `ensure_repo`
  (`ensure_repo.sh`), exposing a bare repo path consumed downstream.
- `build_component` creates one worktree per component
  (`create_worktree.sh`), branched off the integration base.
- `implement_task` (version 2) gives **each task its own worktree**, not a shared
  one. `build_component` computes the task worktree path
  (`${node.create_worktree.message}-${workflow_input.task.id}`) and passes it in
  as `task_worktree`; `implement_task`'s `create_task_worktree`
  (`create_task_worktree.sh`) materializes it via `tgwm` with an explicit
  `--repo`. Each concurrent task gets its own worktree on its own branch — no
  shared `.git`, no shared build cache. (Design: `implement_task.yaml:1-25`,
  `docs/plans/2026-04-24-per-task-worktrees-right-shape.md`.)
- Each task's commits land on the component branch via `merge_task_worktree`
  (`merge_task_worktree.sh`); `cleanup_task_worktree` removes the per-task
  worktree. `commit_component` (`commit_component.sh`) finalizes the component
  branch.
- Integration merges components into an integration branch one at a time in
  `integrate_component`; conflicts route to an LLM resolver (see below). Final
  push/finalize/cleanup happen in `implement_spec` shell nodes
  (`push_to_remote.sh`, `finalize_remote_branch.sh`, `cleanup_branch.sh`).

Because the merge is mechanical (`merge_branch.sh`), the happy path through
`integrate_component` touches no LLM at all.

### Loop Exhaustion

When a looping node hits the workflow's `max_loop_iterations`, the engine emits
the synthetic `_loop_exhausted` meta-decision and preserves the node's last real
envelope for downstream consumers — see `runtime.md` for that mechanism. The
factory uses two authoring patterns on top of it:

- **Graceful escalation** — add an edge `when: _loop_exhausted` to a `kind: emit`
  "declare stuck" node (e.g. `declare_stuck`, `declare_merge_stuck`,
  `declare_verify_stuck`, `declare_integration_stuck`) which packages the last
  messages into a structured `escalate` envelope the parent can route on. The
  edge may carry `failed: true` (mark the node failed) or `failed: false`
  (treat exhaustion as a non-fatal forward route).
- **Fatal opt-out** — set `loop_exhaustion: fatal` on the node. The run fails
  when the loop is exhausted, and the loader's lint stops warning about a missing
  `_loop_exhausted` edge. Used for top-level loops with no useful parent handler
  (`brainstorm` nodes, `build_component`'s `implement_tasks`,
  `implement_task`'s `resolve_review_dispute`).

### ForEach and `depends_on`

The pipeline fans out work with `for_each` at two levels. `implement_spec` uses
`depends_on` on its `integrate_components` ForEach so components integrate in
dependency order; `build_component` uses it on `implement_tasks` so tasks with
inter-dependencies build in order while independent tasks parallelize. The
`build_components` ForEach has no `depends_on`, so components build concurrently.

Parents key off the ForEach aggregate decisions and per-item failure context:
`some_failed` / `all_failed` route `implement_tasks` back to `plan_tasks` to
re-plan only what failed. See `runtime.md` for ForEach expansion, scheduling, and
aggregate-output mechanics.

### Interview Mode

Every pipeline workflow sets `interview: on_issue`
(`internal/definitions/types.go` `InterviewMode`). This lets the engine surface a
human interview when a node hits trouble, without the workflow hardcoding human
gates everywhere.

## Workflows

### implement_spec

`definitions/workflows/implement_spec.yaml` (version 1). Main entry point. Takes
a spec and optional stories, decomposes into components, builds each concurrently
in worktrees, integrates them, then pushes/finalizes.

```
inputs:  spec, stories?, stories_context?, project_dir, repo_url?,
         merge_mode?, secret_keys?, product_slug?

nodes:
  scope_components       role/shell   (scope_components.sh) -> components, stories
  ensure_repo            role/shell   (ensure_repo.sh)      -> bare_repo
  build_one_component    subworkflow  build_component
  build_components       for_each(component) -> build_one_component   [concurrent]
  integrate_one_component subworkflow integrate_component
  integrate_components   for_each(component, depends_on) -> integrate_one_component  [ordered]
  check_remote_configured role/shell  (check_remote_configured.sh)  -> push|skip
  push_to_remote         role/shell   (push_to_remote.sh)
  finalize_remote_branch role/shell   (finalize_remote_branch.sh)
  cleanup_branch         role/shell   (cleanup_branch.sh)

edges:
  scope_components  -> ensure_repo            when: scoped
  ensure_repo       -> build_components       when: ready
  build_components  -> integrate_components   when: all_succeeded
  integrate_components -> check_remote_configured when: all_succeeded
  check_remote_configured -> push_to_remote   when: push
  (check_remote_configured "skip" is terminal — no push when no repo_url)
  push_to_remote    -> finalize_remote_branch when: default
  push_to_remote    -> cleanup_branch         when: status == 'failed'
  finalize_remote_branch -> cleanup_branch    when: status == 'failed'

limits: max_loop_iterations: 5
```

Note the decomposition step (`scope_components`) is a **shell** node, not an LLM
architect. The remote push path is conditional on `check_remote_configured`; with
no `repo_url`, the run completes locally at that node (it has no `skip` edge).

### build_component

`definitions/workflows/build_component.yaml` (version 1). Plans and builds one
component in its own worktree. (Merged from the former `plan_component` +
`build_component`, which is why its loop budget is 8.)

```
inputs:  component, project_dir, spec, stories, parent_run_id?, product_slug?, repo?

nodes:
  create_worktree   role/shell   (create_worktree.sh)
  plan_tasks        role/serf                       -> ready_for_review
  review_plan       role/serf   context: fresh      -> approved | changes_requested
  implement_one_task subworkflow implement_task
  implement_tasks   for_each(task, depends_on) -> implement_one_task   loop_exhaustion: fatal
                    -> all_succeeded | some_failed | all_failed
  commit_component  role/shell   (commit_component.sh)

edges:
  create_worktree -> plan_tasks         when: default
  plan_tasks      -> review_plan        when: ready_for_review
  review_plan     -> plan_tasks         when: changes_requested
  review_plan     -> implement_tasks    when: approved
  plan_tasks/review_plan -> implement_tasks  when: _loop_exhausted  (failed: false)
  implement_one_task -> implement_tasks when: status == 'failed'   (per-item; siblings continue)
  implement_tasks -> plan_tasks         when: some_failed
  implement_tasks -> plan_tasks         when: all_failed
  implement_tasks -> commit_component   when: all_succeeded

limits: max_loop_iterations: 8
```

The surgeon/PM split from the old design is collapsed: `plan_tasks` is a single
serf node that emits a structured task plan (`node.plan_tasks.data.plan.tasks`)
with `depends_on` edges, and `review_plan` (a fresh-context reviewer) gates it.
On task failure, control loops back to `plan_tasks` to re-plan only the failed
tasks. There is deliberately **no** post-build verifier gate — test verification
happens inside each `implement_task`.

### implement_task

`definitions/workflows/implement_task.yaml` (**version 2**). The workhorse.
Implements one task with mechanically enforced TDD in its own per-task worktree.

```
inputs:  task, task_worktree, component, parent_run_id, repo, plan_tasks_session_id

nodes:
  create_task_worktree                  role/shell  (create_task_worktree.sh; workspace: shared)
  write_tests                           role/serf                 (RED)
  write_code                            role/serf  context: fresh (GREEN)
  verify_code_meets_acceptance_criteria role/serf  context: fresh
  review_code_quality                   role/serf  context: fresh
  resolve_review_dispute                role/serf  context: full   loop_exhaustion: fatal (judge)
  debug_task_failure                    subworkflow debug
  merge_task_worktree                   role/shell  (merge_task_worktree.sh)
  cleanup_task_worktree                 role/shell  (cleanup_task_worktree.sh)

edges (happy path + loops):
  create_task_worktree -> write_tests                          when: default
  write_tests -> write_code                                    when: correct_failure
  write_tests -> write_tests                                   when: wrong_failure
  write_tests -> verify_code_meets_acceptance_criteria         when: tests_already_passing
  write_code  -> verify_code_meets_acceptance_criteria         when: tests_pass
  write_code  -> write_code                                    when: worktree_dirty
  write_code  -> debug_task_failure                            when: tests_fail
  write_code  -> verify_code_meets_acceptance_criteria         when: spec_issue
  debug_task_failure -> write_code                             when: default
  debug_task_failure -> resolve_review_dispute                 when: escalate
  verify_code_meets_acceptance_criteria -> write_code          when: code_changes_requested
  verify_code_meets_acceptance_criteria -> write_tests         when: test_changes_requested
  verify_code_meets_acceptance_criteria -> review_code_quality when: approved
  review_code_quality -> write_code                            when: changes_requested
  review_code_quality -> write_tests                           when: test_changes_requested
  review_code_quality -> merge_task_worktree                   when: approved
  resolve_review_dispute -> write_tests | write_code | merge_task_worktree
                            when: send_back | code_changes_requested | (force_approve|skip_task)
  merge_task_worktree -> cleanup_task_worktree                 when: merged
  (all five SCC nodes) -> resolve_review_dispute               when: _loop_exhausted (failed: false)

limits: max_loop_iterations: 4
```

**RED phase.** `write_tests` (the test engineer) makes the acceptance criteria
testable: a failing test for every unsatisfied criterion. It treats
`acceptance_criteria` and `interfaces` as binding, `guidance`/`steps`/`files` as
advisory. It emits `correct_failure` (tests fail for the right reason),
`wrong_failure` (self-loop to fix bad tests), or `tests_already_passing`.

**GREEN phase.** `write_code` writes the minimal *correct* code (not a diff that
papers over the spec), never modifies tests, and emits `tests_pass`,
`worktree_dirty` (self-loop to clean up), `tests_fail` (route to `debug`), or
`spec_issue` (route to the acceptance-criteria reviewer for triage). It runs with
`context: fresh` so each attempt re-reads state without carrying prior reasoning.

**On persistent GREEN failure.** `tests_fail` routes to the `debug` subworkflow,
which investigates root cause and applies a fix, then returns to `write_code`. If
`debug` itself exhausts, it emits `escalate`, which routes to the judge.

**Acceptance-criteria review.** `verify_code_meets_acceptance_criteria` (fresh
context) compares the combined test+code against the task's acceptance criteria.
Gaps route to `write_code` (`code_changes_requested`) or back to `write_tests`
(`test_changes_requested`); a clean pass advances to quality review.

**Code-quality review.** `review_code_quality` (fresh context) checks
readability/maintainability. Issues route to `write_code` or, for test problems,
`write_tests`.

**Dispute resolution.** When any of the five looping nodes exhausts the loop
budget, `_loop_exhausted` routes to `resolve_review_dispute` — a `context: full`
judge node that reads all upstreams and decides `force_approve`, `send_back`,
`code_changes_requested`, or `skip_task`. The judge is `loop_exhaustion: fatal`
itself (last line of defense). There are **no** failure edges inside
`implement_task`: a hard node failure fails the task, surfacing to
`build_component`'s `implement_tasks -> plan_tasks` (some_failed) re-plan edge.

### integrate_component

`definitions/workflows/integrate_component.yaml` (version 1). Merges one
component's branch into the integration branch, resolving conflicts with an LLM
only when needed, then verifies the merged tree.

```
inputs:  component, project_dir, spec, stories, parent_run_id, repo?

nodes:
  merge_branch              role/shell  (merge_branch.sh)   -> merged | conflict
  resolve_conflict          role/serf   context: fresh      -> resolved   (LLM conflict resolver)
  finalize_merge            role/shell  (finalize_merge.sh)
  cleanup_component_worktree role/shell (cleanup_component_worktree.sh)
  export_tree               role/shell  (export_tree.sh)
  verify_integration        subworkflow verify_integration
  debug_integration_failure subworkflow debug
  declare_integration_stuck emit        -> escalate

edges:
  merge_branch -> cleanup_component_worktree   when: merged          (mechanical happy path)
  merge_branch -> resolve_conflict             when: conflict
  resolve_conflict -> finalize_merge           when: resolved
  finalize_merge -> cleanup_component_worktree when: default
  cleanup_component_worktree -> export_tree    when: default
  export_tree -> verify_integration            when: default
  verify_integration -> debug_integration_failure when: fail
  verify_integration -> declare_integration_stuck when: escalate
  debug_integration_failure -> verify_integration when: default
  verify_integration / debug_integration_failure -> declare_integration_stuck
                            when: _loop_exhausted  (failed: true)

limits: max_loop_iterations: 5
```

The `resolve_conflict` node is the only LLM in the merge path: it reads the
conflicting files in the temp merge worktree, uses the spec as source of truth,
commits the resolution, and hands back to shell (`finalize_merge`). It is
explicitly constrained ("do NOT run tgwm/push/export"). After merge, `export_tree`
materializes the merged tree and `verify_integration` runs the test suites.

### verify_integration

`definitions/workflows/verify_integration.yaml` (version 1). Integration tests,
E2E tests against story acceptance criteria, then the release action.

```
inputs:  spec, stories, project_dir

nodes:
  run_integration_tests       role/serf  prompt_on_resume: true  -> pass | fail
  run_e2e_tests               role/serf  prompt_on_resume: true  -> pass | fail
  debug_integration_test_failure subworkflow debug_merge
  debug_e2e_test_failure         subworkflow debug_merge
  finish                      subworkflow finish_branch
  declare_verify_stuck        emit  -> escalate

edges:
  run_integration_tests -> run_e2e_tests             when: pass
  run_integration_tests -> debug_integration_test_failure when: fail
  run_e2e_tests -> finish                            when: pass
  run_e2e_tests -> debug_e2e_test_failure            when: fail
  debug_integration_test_failure -> run_integration_tests when: default
  debug_e2e_test_failure         -> run_e2e_tests    when: default
  (all four loop nodes) -> declare_verify_stuck      when: _loop_exhausted (failed: true)

limits: max_loop_iterations: 3
```

Stories are a first-class input: the E2E tester verifies each story's acceptance
criteria. Test failures route to `debug_merge` (the boundary-focused debugger),
not `debug`. The terminal success path runs `finish_branch`.

### debug

`definitions/workflows/debug.yaml` (version 1). Systematic root-cause debugging.
Callable standalone and as a subworkflow from `implement_task` and
`integrate_component`.

```
inputs:  error_context, error_data?, task?, project_dir

nodes:
  debugger      role/serf   -> investigating | root_cause_confirmed
  write_code    role/serf   -> fix_applied | fix_failed
  declare_stuck emit        -> escalate

edges:
  debugger   -> debugger     when: investigating
  debugger   -> write_code   when: root_cause_confirmed  (passes: debugger)
  write_code -> debugger     when: fix_failed
  debugger / write_code -> declare_stuck  when: _loop_exhausted (failed: true)

limits: max_loop_iterations: 5
```

The debugger follows a 4-phase method (investigate -> find the pattern -> test a
hypothesis -> hand off) and **does not implement fixes** — `write_code` does, off
the confirmed diagnosis passed on the edge. `error_data` carries structured
evidence (e.g. `failing_tests[]`) and is preferred over the `error_context`
summary. On exhaustion, `declare_stuck` emits an `escalate` envelope with the last
debugger/fix messages.

### debug_merge

`definitions/workflows/debug_merge.yaml` (version 1). Same shape as `debug` but
tuned for post-merge integration/E2E failures: `merge_engineer` investigates the
boundary between two independently-passing components, `code_engineer` fixes,
`declare_merge_stuck` aggregates on exhaustion. `max_loop_iterations: 5`.

### finish_branch

`definitions/workflows/finish_branch.yaml` (version 1). One serf node,
`release_manager` (model `gpt-5.4-mini`, low reasoning via `runner_env`), executes
a pre-chosen branch-completion action (merge/pr/keep/discard). The workflow hard-
codes `choice: merge` — it runs the test suite and auto-merges by default, never
merging broken code. No edges; single terminal node.

### brainstorm

`definitions/workflows/brainstorm.yaml` (**version 2**). Front-door workflow when
no spec exists. A serf `brainstormer` interviews the user one question at a time
through Toil's human-gate pause/resume cycles, producing a spec (`data.spec`) and
story cards (`data.stories`) that feed `implement_spec`.

```
nodes:  brainstormer (serf), human_clarify (human gate), human_review (human gate)
        — all three loop_exhaustion: fatal
edges:  brainstormer -> human_clarify   when: needs_info
        human_clarify -> brainstormer   when: clarified
        brainstormer -> human_review    when: design_ready
        human_review -> brainstormer    when: changes_requested
        (human_review "approved" is terminal)
limits: max_loop_iterations: 10
```

Each brainstormer-human exchange is a pause/resume cycle through the approvals
API — not a live conversation. All three nodes are `loop_exhaustion: fatal`
because there is no parent to escalate to.

### Utility: interview / learn

`interview.yaml` pairs an `interviewer` and `interviewee` serf node for
structured Q&A. `learn.yaml` runs a ForEach of `interview` subworkflows then a
`synthesize` serf node — post-run learning, outside the build pipeline.

## Runners

Defined in `definitions/runners/` and registered by type in
`internal/app/app.go` (the five `case` arms below). The `runner:` value on a node
names a runner *id*; the runner's `type` selects the implementation.

```
id      type    command  resume   notes
serf    serf    serf      yes      production agent runner — model via env (default openai/gpt-5.4, low reasoning)
codex   codex   codex     yes      gpt-5.4, medium reasoning (eval/port workflows)
claude  claude  claude    yes      Anthropic CLI harness
shell   shell   bash -lc  no       deterministic ops (worktrees, merges, push)
human   human   —         no       approval gates
```

**serf is the production runner for the software factory.** Nearly every
`kind: role` node in the pipeline (`plan_tasks`, `review_plan`, `write_tests`,
`write_code`, the reviewers, the judge, `debugger`, `resolve_conflict`,
`run_integration_tests`, `run_e2e_tests`, `release_manager`, `brainstormer`)
uses `runner: serf`. `shell` handles every deterministic git/worktree/push
operation; `human` handles brainstorm gates. `codex` and `claude` are available
but in this pipeline appear only in the porting/eval workflows and as alternates.

The serf runner (`serf.yaml`) is parameterized entirely through environment:

```yaml
args: [--agent, ${SERF_AGENT:-worker},
       --model, ${SERF_PROVIDER:-openai}/${SERF_MODEL:-gpt-5.4},
       --reasoning-effort, ${SERF_REASONING_EFFORT:-low},
       --verbose]
timeout_sec: 1200
```

Per-node overrides use `runner_env:` (e.g. `finish_branch`'s `release_manager`
sets `SERF_MODEL: gpt-5.4-mini`, `SERF_REASONING_EFFORT: low`). This replaces the
old design's named per-model runners (`claude-opus`, `claude-sonnet`,
`claude-haiku`, `codex-default`) — there is one serf runner and you tune the model
with env, not a separate runner id.

### Context discipline

Verifier-style nodes set `context: fresh` so they evaluate each verdict
independently with no memory of prior verdicts: `review_plan`, `write_code`,
`verify_code_meets_acceptance_criteria`, `review_code_quality`,
`resolve_conflict`. The dispute judge `resolve_review_dispute` uses
`context: full` precisely because it must see the whole history to adjudicate.
See `runtime.md` for what each context mode does.

## Porting Subsystem

The porting workflows are separate from the software-factory pipeline and serve a
different purpose: porting code between codebases.

- `initial_port.yaml` (version 1) — one-shot port of a source repo's core
  abstractions plus tests into a target language.
- `semantic_port.yaml` (version 1) — looping workflow that tracks upstream commits
  and incrementally ports semantic changes, one commit per iteration.

They share the engine and runners but carry their own inline prompts tuned for
porting work. See `tests/eval/semantic_port.yaml` for the eval harness around them.

## Test / Fixture Workflows

Several workflows under `definitions/workflows/` exist only to exercise engine
behavior in tests and are not part of the product pipeline:
`handoff_smoke` / `handoff_smoke_serf` (handoff plumbing),
`forced_debug_test`, and the `forced_review_test*` family (forced review/dispute
routing, including a `_downstream`, `_dry`, and `_old` variant). Treat these as
test fixtures, not factory stages.

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code (rewritten from design doc to current architecture; Phase-3 verified)._
