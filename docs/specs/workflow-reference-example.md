# Workflow Reference Example

This document is a comprehensive example workflow that demonstrates every
available field and feature. It is not meant to be run as-is; it is a
reference for workflow authors.

For field definitions, see schemas.md. For runtime behavior, see
runtime.md.

## Complete Workflow

```yaml
id: reference_example
name: Reference Example Workflow
version: 1
description: Demonstrates every workflow, node, and edge field.

# --- Inputs ---
# Simple input declarations (name -> type description).
inputs:
  idea: string
  items: list
  context: string
  project_dir: string

# Input schema provides validation and optional/description metadata.
# When both inputs and input_schema declare the same key, input_schema
# provides the richer definition.
input_schema:
  context:
    type: string
    optional: true
    description: Optional background context for the workflow.
  items:
    type: list
    description: A list of items to process in the for-each node.

# --- Outputs ---
# Declares what the workflow produces. Output values come from terminal
# node outputs referenced by downstream consumers or parent workflows.
outputs:
  result: string

# --- Workspace defaults ---
# Applied to any node that does not declare its own workspace.
# Modes: isolated (default), shared, group, project.
workspace_defaults:
  mode: project
  path: "${env.PROJECT_DIR}"

# --- Limits ---
limits:
  max_loop_iterations: 5
  max_no_progress_iterations: 3

# --- Tags ---
tags: [example, reference]

# --- Runner overrides ---
# Map of tag -> runner ID. When a node has a matching tag and no
# explicit runner, the override applies. First matching tag wins.
# Referenced runner IDs must exist in the bundle's runners.
runner_overrides:
  fast: claude
  heavy: codex

# --- Retry target (workflow-level) ---
# Fallback node to re-execute when any goal_gate is unsatisfied and
# the goal_gate node has no retry_target of its own.
retry_target: planner

# --- Context default ---
# Default context mode for all nodes. Overridden by node.context.
# See runtime.md for what each mode does.
context_default: compact

# --- Nodes ---
nodes:

  # --- Basic role node ---
  # `role:` is a free-text display label only (shown on the dashboard and
  # graph); it does NOT select a runner. Runner selection comes from
  # `runner:` or a tag matched against runner_overrides.
  - id: planner
    kind: role
    runner: serf
    role: surgeon
    prompt: |
      Write an implementation plan based on the idea.
      Idea: ${input.idea}
      Context (may be empty): ${input.context}
    # Node inputs are string expressions evaluated in Phase 1. Use
    # ${workflow_input.X} to read run-start inputs (${input.X} is forbidden
    # here — it only exists in Phase 5 prompts/emit output). The ! suffix
    # marks a reference as required; a bare ${...} resolves to null when
    # absent.
    inputs:
      idea: "${workflow_input.idea!}"
      context: "${workflow_input.context}"
      review_feedback: "${node.plan_review.message}"
    outputs_schema:
      type: object
      required: [plan]
      properties:
        plan: { type: string }
    decisions: [ready_for_review]

  # --- Node with tags for runner override ---
  # Tags are matched against workflow.runner_overrides. Since this node
  # has tag "fast", it uses the claude runner (from runner_overrides) and
  # needs no explicit runner: field.
  - id: quick_check
    kind: role
    role: verifier
    tags: [fast]
    prompt: |
      Quick verification pass of the plan: ${input.plan}
    inputs:
      plan: "${node.planner.message}"
    decisions: [verified, failed]

  # --- Node with explicit runner ---
  # node.runner takes precedence over runner_overrides.
  - id: plan_review
    kind: role
    runner: claude
    role: plan_reviewer
    prompt: |
      Review the plan for completeness.
    inputs:
      plan_doc: "${node.planner.artifacts}"
    decisions: [approved, changes_requested]

  # --- Human node with gate, timeout, and approval ---
  # Human nodes do not need a runner: field — the engine dispatches them
  # to the built-in human runner directly.
  - id: human_approval
    kind: human
    gate: required              # Pauses the run until resolved
    prompt: |
      A human must review and approve the plan before proceeding.
    decisions: [approved, rejected]
    timeout_sec: 3600           # Auto-resolve after 1 hour, firing _timeout

  # --- Node with context mode override ---
  # Overrides the workflow-level context_default for this node.
  - id: implementer
    kind: role
    runner: serf
    role: implementer
    context: fresh              # See runtime.md for context modes
    prompt: |
      Implement the approved plan: ${input.plan}
    inputs:
      plan: "${node.planner.artifacts}"
    decisions: [done]

  # --- Node with retry policy ---
  - id: flaky_step
    kind: role
    runner: serf
    role: executor
    retry:
      max: 3                   # Up to 3 retries (4 total attempts)
      backoff: exponential     # exponential | fixed
      initial_delay: 2s        # Go duration string
      max_delay: 30s           # Cap on backoff delay
      jitter: true             # +/-50% random variance
    prompt: |
      Run an operation that might fail transiently.
    inputs:
      work: "${node.implementer.message}"
    decisions: [success, failure]

  # --- Node with custom workspace ---
  - id: isolated_analysis
    kind: role
    runner: serf
    role: researcher
    workspace:
      mode: isolated            # Dedicated workspace for this node
    prompt: |
      Analyze the codebase in isolation.
    decisions: [done]

  # --- Node with group workspace ---
  - id: grouped_step_a
    kind: role
    runner: serf
    role: implementer
    workspace:
      mode: group
      group: backend            # Shares workspace with other "backend" nodes
    prompt: |
      Work on the backend.
    decisions: [done]

  # --- System node ---
  # System nodes run internal engine logic, not an external runner, so
  # they need no runner: field.
  - id: check_result
    kind: system
    decisions: [pass, fail]

  # --- Goal gate node ---
  # Must complete for the run to succeed. If unsatisfied after a wave,
  # the engine re-executes the retry_target.
  - id: quality_gate
    kind: role
    runner: serf
    role: verifier
    goal_gate: true
    retry_target: implementer   # Re-run implementer if gate unsatisfied
    prompt: |
      Verify all quality criteria are met.
    inputs:
      work: "${node.implementer.message}"
    decisions: [passed, needs_work]

  # --- Subworkflow node ---
  # Executes another workflow definition as a child run. Subworkflow nodes
  # take a workflow: id instead of a runner.
  - id: run_subtask
    kind: subworkflow
    workflow: implement_task
    inputs:
      task: "${node.planner.data.task}"
      project_dir: "${workflow_input.project_dir}"

  # --- For-each orchestrator (sequential) ---
  # Expands into per-iteration nodes: process_item::0, process_item::1, etc.
  # The orchestrator declares only for_each; the template node (body)
  # carries kind/runner/prompt.
  - id: process_items
    for_each:
      list: "${workflow_input.items}"  # Expression resolving to a list
      item: current_item               # Variable bound to each element
      body: process_item               # Template node run for each iteration

  # --- For-each template node (sequential) ---
  # The item is auto-injected under the `item:` name and is referenced as
  # ${input.current_item} in Phase 5 expressions (the prompt).
  - id: process_item
    kind: role
    runner: serf
    role: executor
    prompt: |
      Process the current item: ${input.current_item}
    decisions: [processed]

  # --- For-each orchestrator with depends_on (DAG scheduling) ---
  # When items are structured maps, depends_on names the field holding
  # per-item dependency IDs. Independent items run concurrently; dependent
  # items wait for their predecessors. Omit depends_on to run all items
  # concurrently.
  - id: parallel_process
    for_each:
      list: "${workflow_input.items}"
      item: current_item
      body: parallel_process_item

  # --- For-each template node ---
  - id: parallel_process_item
    kind: role
    runner: serf
    role: executor
    prompt: |
      Process this item: ${input.current_item}
    decisions: [processed]

  # --- Node with loop ---
  # Loop back to planner when changes_requested.
  - id: final_review
    kind: role
    runner: serf
    role: plan_reviewer
    loop:
      on: changes_requested     # Decision that triggers the loop
      back_to: planner          # Node to loop back to
    prompt: |
      Final review of the complete work: ${input.result}
    inputs:
      result: "${node.implementer.message}"
    decisions: [approved, changes_requested]

# --- Edges ---
# Edges define the flow between nodes. The "when" field matches the
# source node's decision. The "prompt" field provides context to the
# target node when this edge is traversed.
edges:
  - from: planner
    to: plan_review
    when: ready_for_review
    prompt: |
      Review the plan for completeness and feasibility.

  - from: plan_review
    to: human_approval
    when: approved

  - from: plan_review
    to: planner
    when: changes_requested
    prompt: |
      Revise the plan based on review feedback.

  - from: human_approval
    to: implementer
    when: approved

  - from: human_approval
    to: planner
    when: rejected

  # Meta-decision edge: _timeout is synthesized by the engine when the
  # gate:required node times out. Every meta-decision edge (when: starting
  # with "_") MUST declare failed: (true = run-level failure, false =
  # normal terminus).
  - from: human_approval
    to: implementer
    when: _timeout
    failed: false

  - from: implementer
    to: quality_gate
    when: done

  - from: quality_gate
    to: final_review
    when: passed

  - from: flaky_step
    to: check_result
    when: success

  - from: check_result
    to: final_review
    when: pass
```

## Field Coverage Checklist

A flat index of every field this example exercises. See schemas.md for
definitions.

Workflow top-level:
- id, name, version, description
- inputs, input_schema, outputs
- prompt_inputs_mode
- workspace_defaults
- nodes, edges
- limits, max_loop_iterations, max_no_progress_iterations
- tags
- runner_overrides
- retry_target
- context_default
- interview

Node:
- id, kind
- role, runner, runner_env, tags
- prompt_inputs_mode
- workflow
- prompt, context, session_id, prompt_on_resume
- retry, max, backoff, initial_delay, max_delay, jitter
- inputs
- outputs_schema, decisions
- gate
- goal_gate, retry_target
- timeout_sec
- loop, on, back_to
- for_each, list, item, body, depends_on
- join, loop_exhaustion, max_turns
- output
- workspace, mode, group, path

Edge:
- from, to
- when
- prompt
- passes
- failed

## Patterns

### Review loop
A common pattern is a review loop where a reviewer can request changes
and the implementer revises until approved:

```yaml
nodes:
  - id: implement
    kind: role
    runner: serf
    decisions: [ready_for_review]

  - id: review
    kind: role
    runner: serf
    decisions: [approved, changes_requested]

edges:
  - from: implement
    to: review
    when: ready_for_review
  - from: review
    to: implement
    when: changes_requested
    prompt: Fix the issues found in review.

limits:
  max_loop_iterations: 3
```

### Human approval gate
Pause for human decision before proceeding. See schemas.md for the
_timeout mandatory-edge rule.

```yaml
nodes:
  - id: approval
    kind: human
    gate: required
    decisions: [approved, rejected]
    timeout_sec: 7200

edges:
  - from: approval
    to: proceed
    when: approved
  # Required: route the engine-synthesized _timeout meta-decision.
  - from: approval
    to: proceed
    when: _timeout
    failed: false
```

### ForEach with per-item failure handling

When one failed item shouldn't kill the whole ForEach, declare a failure
edge from the template back to the orchestrator and aggregate edges off
the orchestrator's settled decision. See runtime.md for the routing and
settling mechanics.

```yaml
nodes:
  - id: process_item
    kind: role
    runner: worker
    prompt: |
      Process one task from the batch: ${input.task}
    decisions: [done, needs_review]

  - id: process_all
    for_each:
      list: "${workflow_input.tasks}"
      item: task
      body: process_item
    decisions: [all_succeeded, some_failed, all_failed]

  - id: recover
    kind: role
    runner: triage
    prompt: |
      Some tasks failed. See node.process_all.data.items for per-item
      status and failure_context. Decide whether to retry or escalate.

edges:
  # Per-item failure edge: absorbs individual item failures. Must route
  # back to the orchestrator.
  - from: process_item
    to: process_all
    when: "status == 'failed'"

  # Aggregate edges after all items settle
  - from: process_all
    to: next_step
    when: all_succeeded
  - from: process_all
    to: recover
    when: some_failed
  - from: process_all
    to: recover
    when: all_failed
```

### Goal gate with retry
Ensure a quality check passes, retrying the implementation if it fails:

```yaml
nodes:
  - id: build
    kind: role
    runner: serf
    decisions: [done]

  - id: quality_check
    kind: role
    runner: serf
    goal_gate: true
    retry_target: build
    decisions: [passed, needs_work]

edges:
  - from: build
    to: quality_check
    when: done
```

### Subworkflow composition
Compose workflows by delegating to child workflows:

```yaml
nodes:
  - id: subtask
    kind: subworkflow
    workflow: implement_task
    inputs:
      task: "${node.planner.data.task}"
      project_dir: "${workflow_input.project_dir}"
```

The child run is linked to the parent. Cancelling the parent cascades
to child runs.

### Context modes for long workflows
Set a workflow-level `context_default` and override per node with
`context:`. See runtime.md for what each mode does.

```yaml
context_default: compact

nodes:
  - id: late_stage
    kind: role
    runner: serf
    decisions: [approved]

  - id: independent_step
    kind: role
    runner: serf
    context: fresh
    decisions: [done]

  - id: continuation
    kind: role
    runner: serf
    context: full
    decisions: [done]
```

### Runner overrides by tag
Route nodes to different runners based on tags. Referenced runner IDs
must exist in the bundle:

```yaml
runner_overrides:
  quick: claude
  thorough: codex

nodes:
  - id: triage
    kind: role
    tags: [quick]
    decisions: [done]

  - id: deep_analysis
    kind: role
    tags: [thorough]
    decisions: [done]
```

### Retry with exponential backoff
Handle transient failures automatically:

```yaml
nodes:
  - id: api_call
    kind: role
    runner: serf
    retry:
      max: 3
      backoff: exponential
      initial_delay: 2s
      max_delay: 30s
      jitter: true
    decisions: [success, failure]
```

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code (promoted to evergreen reference; Phase-3 verified)._
