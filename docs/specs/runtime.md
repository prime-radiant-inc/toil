# Runtime Semantics

Workflow graph
The workflow is a directed graph that may contain cycles. The engine treats
cycles as explicit review loops controlled by edges and loop limits.

Node readiness
A node becomes ready when an incoming edge fires (decision routing); a join node
(`join: all`) fires only after every required incoming edge arrives. Inputs are
resolved at dispatch time — a missing required input fails the node then, it does
not hold the node back from being scheduled.

Execution
1. Load workflow snapshot.
2. Resolve `${env.X}` references against the env snapshot captured at run
   creation (see Environment variable expansion).
3. Activate start nodes (nodes with no incoming edges).
4. Execute all ready nodes concurrently in waves.
5. Parse outputs and route using decision matching.
6. Check goal gates after each wave.
7. Repeat until completion, pause, cancellation, or failure.

Environment variable expansion
- Workflow strings (prompts, workspace paths, node inputs, edge passes, emit
  output) may reference process environment via `${env.X}` expressions.
- At run creation, the engine collects every referenced env key and snapshots
  the matching process-environment values (plus any matching dispatch inputs)
  into the run state.
- `${env.X}` references are resolved at dispatch time from that snapshot.
- A missing `${env.X}` reference is a resolution error that fails the node's
  dispatch (an env var that is set but empty captures as an empty string).
  Unlike `input`/`node` references, a non-`!` env reference is not optional; the
  trailing-`!` marker (`${env.X!}`) only changes the error wording.

Decision routing
- If one or more edges match the decision, follow all matching edges.
- If no edge matches, follow edges with when: default.
- If no edges match and no default edge exists, the node is terminal.

Loops
- Loop edges are allowed and tracked.
- limits.max_loop_iterations stops unbounded loop traversals.
- limits.max_no_progress_iterations stops repeated identical dispatch/output
  cycles (default 3) and fails the run with a circuit-breaker event.

Gates
- gate: required pauses the run and creates a human approval event.
- Any other gate value (including optional or none) runs the node normally
  without pausing for approval.

Goal gates
- goal_gate: true marks a node as a mandatory completion constraint.
- After each execution wave, the engine checks whether all goal gate nodes
  have status "completed".
- If a goal gate is unsatisfied, the engine looks for a retry target:
  1. The goal gate node's own retry_target field.
  2. The workflow-level retry_target field.
  3. If neither exists, the run fails with "goal gate unsatisfied".
- The retry target node is added to the ready queue and execution continues.
- Events logged: goal_gate_satisfied, goal_gate_unsatisfied.

Retries
- Nodes with a retry policy are automatically retried on retryable errors.
- The retry policy fields (max, backoff, initial_delay, max_delay, jitter) and
  their defaults and backoff formulas are defined in schemas.md.
- Each retry sets node status to "retrying" and increments RetryCount.
- When retry.max > 1 and all attempts fail, the engine emits the
  _retry_exhausted meta-decision if an outgoing edge with `when: _retry_exhausted`
  exists; otherwise the failure routes via `when: status == 'failed'` edges (or
  fails the run if none exist).

Timeouts
- timeout_sec on a node sets a countdown for a pending approval gate.
- When the timeout expires, the gate is resolved by emitting the _timeout
  meta-decision, which routes through an outgoing edge declared `when: _timeout`.
- Only meaningful for nodes with gate: required (the approval gate path).
- A node with timeout_sec > 0 and gate: required MUST have an outgoing
  _timeout edge; the validator rejects the workflow otherwise (silent
  forever-pending approval is forbidden).

Context modes
Each node execution resolves a context mode that controls how conversation
history is handled for the runner:
- full (default): Preserves the existing session. The runner continues
  the same conversation.
- fresh: Clears the session. The runner starts a new conversation with
  no prior context.
- compact: Clears the session. A machine-readable preamble summarizing
  completed node outputs is prepended to the prompt. Shows the last 50
  completed nodes, each message truncated to 200 characters.
- summary: Same as compact, but the most recently completed node's full
  message and data keys are included without truncation.

Resolution order:
1. Node's explicit context field.
2. Workflow's context_default field.
3. Defaults to full.

Subworkflows
- kind: subworkflow executes another workflow and returns its final output.
- Subworkflow runs are recorded as child runs and linked to the parent.

## For-Each Execution

A ForEach is declared as two nodes:

- **Template** — a regular node (any kind, any runner) that defines what one item does. Declared with `kind`, `runner`, `prompt`, `inputs`, etc.
- **Orchestrator** — a node whose only ForEach-specific attribute is `for_each:`, referencing the template via `body:`. The orchestrator must NOT carry `kind`, `runner`, or `workflow` — those fields belong on the template.

### Expansion and state

When the engine executes the orchestrator, it:
1. Resolves `for_each.list` to a slice.
2. For each index N, creates a state node with ID `<template-id>::<N>`.
3. Binds the item's value to the name given by `for_each.item` and merges it into the template's input context.
4. Dispatches each expanded item to run under the template's definition.

Items are either all run concurrently (no `depends_on`) or scheduled as a DAG (with `depends_on`, see below).

### Scheduling

- **Concurrent** (default): all items launch together and run in parallel.
- **DAG**: with `for_each.depends_on: <field-name>`, each item is expected to be a structured map with an `id` field and a dependency list at the named field. The engine computes a dependency DAG and runs independent items concurrently, dependents after their prerequisites succeed. Cycle detection happens at dispatch time.

### Per-item edges

Edges declared `from: <template-id>` apply to each expanded item. By validation rule, they must route `to: <orchestrator-id>`. The only edge type whose *presence* affects runtime behavior is a failure edge (a `when:` expression that matches `status == 'failed'` for the item):

- **With failure edge**: a failed item is recorded as `failed-handled` with an enriched `failure_context`, and siblings continue running. In DAG mode, the failed item's transitive dependents are marked `skipped`.
- **Without failure edge**: a failed item causes the orchestrator to fail fast (backward-compatible with the inline form that preceded this design).

### Orchestrator settling

The orchestrator completes only when every item reaches a terminal state (`succeeded`, `failed-handled`, `failed`, or `skipped`). On completion, the orchestrator emits one of three decisions:

- `all_succeeded` — every item succeeded; the empty-list case also produces this decision.
- `some_failed` — at least one item succeeded AND at least one item failed (or failed-handled).
- `all_failed` — no items succeeded (all items are either failed, failed-handled, or skipped).

Downstream edges from the orchestrator use these decisions in their `when:` clauses.

Skipped items do not count toward the "all" calculation — a DAG where A fails and B skips (depending on A) produces `all_failed` if no other items succeed, because the only non-skipped item failed.

### Aggregate output

The orchestrator's `data.items` contains a per-item record for each index:

| Field | Type | When populated |
|-------|------|---------------|
| `id` | string | always — from the item's `id` field (DAG mode) or the index as a string |
| `expanded_id` | string | always — the engine's state node ID (`<template-id>::<N>`) |
| `status` | string | always — one of `succeeded`, `failed-handled`, `failed`, `skipped` |
| `decision` | string | when the item produced a non-empty decision |
| `message` | string | when the item produced a non-empty message |
| `data` | map | when the item produced output data |
| `artifacts` | list | when the item produced artifacts |
| `failure_context` | map | only for `failed` and `failed-handled` items — contains `session_id`, `last_decision`, `last_message`, `attempts`, and (for subworkflow children) `child_run`, `decision_history`, `failed_child` |
| `reason` | string | only for `skipped` items — identifies the upstream failure (e.g., "dependency X failed") |

Downstream nodes reference items as `node.<orchestrator>.data.items.<N>.<field>` — e.g., `node.build_task.data.items.0.status`.

### Resume and re-execution

- **Resume** (the run was crashed while the orchestrator was still running): items in `completed`, `failed-handled`, or `skipped` status are preserved; remaining items launch fresh.
- **Re-execution** (a later edge fires the already-completed orchestrator again, e.g. a workflow loop): the orchestrator's state is reset — expanded items' outputs are cleared before the new pass runs, so stale per-item data doesn't leak across iterations.

Cancellation
- A running or paused run can be cancelled via API or CLI.
- Cancellation sets the run status to "cancelled" and timestamps FinishedAt.
- The engine context is cancelled, which:
  1. Kills running subprocesses (runners use exec.CommandContext).
  2. Triggers ctx.Err() checks at wave boundaries in the run loop.
  3. Sets all running nodes to "cancelled" status.
- Cascading: child runs (subworkflows) are also cancelled.
- A cancelled run cannot be resumed.
- Events logged: run_cancelled.

Resume
- If a process is running, the engine never detaches.
- If the process ended, the engine resumes the session using the runner token.
- Resume uses the run state and event log to restore node state.
- Resuming a completed run returns its cached final output without re-execution.
- Resuming a cancelled run is rejected with an error.
- A failed run can be resumed: it re-enters the run loop from its persisted state.

Workspaces
- Each node executes in a workspace based on workspace.mode.
- Resolution order: node.workspace overrides workflow.workspace_defaults when
  present; the default mode is isolated.
- The per-mode workspace path layout is defined in workspaces.md.

Artifacts
- Artifact storage and inputs-based handoff are defined in workspaces.md.

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code (promoted to evergreen reference; Phase-3 verified)._
