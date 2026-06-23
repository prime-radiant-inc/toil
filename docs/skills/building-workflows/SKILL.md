---
name: building-workflows
description: Use when writing, editing, validating, or debugging toil workflow YAML — node/edge structure, ForEach, decision tags, expression syntax (${workflow_input.X} vs ${input.X}, supported node fields, required ! refs), or analyzing why a run misbehaved
---

# Building, Testing, and Improving Toil Workflows

Practical guide for developing toil workflows — from writing YAML to analyzing production runs and iterating on agent prompts.

## Workflow Development Cycle

```
1. Write/edit workflow YAML + shell scripts
2. Validate: go test ./internal/definitions/
3. Build: make build && go build -o bin/tgwm ./cmd/tgwm/
4. Restart server: kill server, make serve-daemon
5. Trigger a run
6. Analyze results: API + events.jsonl + serf transcripts
7. Identify issues → go to 1
```

## Building Workflows

### Workflow YAML Structure

```yaml
id: my_workflow
name: My Workflow
version: 1
description: What this workflow does
interview: on_issue
max_loop_iterations: 5
inputs:
  spec: string
  project_dir: string
prompt_inputs_mode: declared
workspace_defaults:
  mode: project
  path: ${PROJECT_DIR}    # cwd for runners — not the run dir
nodes:
  - id: my_node
    kind: role          # role (single agent) or subworkflow
    runner: serf         # serf, shell, or human
    context: fresh       # fresh = new session each attempt
    prompt: |
      Your prompt here
    inputs:
      spec: "${workflow_input.spec}"   # run-start input → workflow_input (NOT input) in an inputs: block
    decisions:
      - id: done
      - id: needs_work
        tags: [override]         # optional — attaches cross-cutting meaning.
                                 # See "Decision Tags" below.

edges:
  - from: my_node
    to: next_node
    when: done
    prompt: |
      Context for the next node about why it's being triggered.
```

### Key Patterns

**Workspace is PROJECT_DIR, not the run dir.** The engine creates PROJECT_DIR if it doesn't exist. Runners run from there. The run dir (TOIL_CURRENT_WORKFLOW_DIR) is toil's metadata — runners access its outputs subdirectory through the `TOIL_WORKFLOW_OUTPUTS` env var, not cwd-relative paths.

**Failure routing**: any node can have edges `when: "status == 'failed'"`. When the node fails (runner crash, timeout, unhandled loop exhaustion), the engine routes through matching failure edges. Without a failure edge, failure is fatal. The engine builds structured failure context on `node.X.data.failure.*` — includes `child_run`, `decision_history`, `failed_child` for subworkflow nodes.

**Loop escapes**: `loop_exhausted_to: <node-id>` on a node declares where to route when `max_loop_iterations` is hit. Good for bouncing review loops.

**Shell nodes** output structured JSON with `decision`, `message`, `data`, `artifacts`:
```bash
cat <<ENDJSON
{
  "decision": "merged",
  "message": "Clean merge",
  "data": {},
  "artifacts": []
}
ENDJSON
```
Both branches of any conditional must include `data: {}`.

**Converging paths**: Two edges into a node without `join: all` — node runs on whichever edge fires first. Use for mutually exclusive paths (e.g., clean merge OR conflict resolution both lead to export).

**Subworkflow env inheritance**: Child workflows inherit parent's `TOIL_CURRENT_WORKFLOW_DIR`, `PROJECT_DIR`, `TOIL_ROOT`. `TOIL_RUN_ID` is always the current run's ID (not inherited).

**Decision tags**: decisions can carry `tags: [...]` to surface them generically in dashboard badges, `toil inspect` aspects, and `tree.tagged.<name>` expressions — without the harness hardcoding decision names. See **Decision Tags** below.

**Expression resolution**: input/prompt/edge values are `${...}` template strings over six namespaces — `workflow_input`, `input`, `node`, `env`, `run`, `tree`. The `input` vs `workflow_input` distinction, the closed set of `node` fields, and the silent-nil-without-`!` behavior are each easy to get wrong. See **Expression Reference** below; `tree.tagged.<tag>` is covered under **Decision Tags**.

### Expression Reference

Input, prompt, and edge-`passes` values are **`${...}` template strings** — not an `expr:` map. A value that is exactly one `${...}` keeps the resolved native type (list, map, number); a `${...}` embedded in surrounding text interpolates as a string. Write a literal dollar-brace as `$${`.

| Namespace | Resolves to | Valid positions |
|-----------|-------------|-----------------|
| `${workflow_input.X}` | run-start inputs (top-level `inputs:`) | all |
| `${input.X}` | merged dispatch map | **Phase 5 only**: node `prompt:` and emit `output:` — never `inputs:`/`passes:` |
| `${node.<id>.<field>}` | a completed node's output | downstream of that node |
| `${env.X}` | process environment | all |
| `${run.id}` | current run id | all |
| `${tree.tagged.<tag>}` | tagged decisions across the run tree | all (see Decision Tags) |

**`input` vs `workflow_input`.** In `inputs:` and edge `passes:` blocks you MUST use `${workflow_input.X}` for run-start inputs; `${input.X}` is rejected there — it exists only in the Phase-5 dispatch map that prompts and emit-output see. This is the single most common authoring mistake.

**`node.<id>.<field>` — closed field set:** `decision`, `message`, `artifacts`, `data`, `session_id`, `tags`, `status`, `attempts`, `last_routing_decision`, `loop_iterations`. Reach into data with `${node.x.data.a.b}`; bare `${node.x}` returns the whole output map. Any other field — `${node.x.output}`, `${node.x.result}`, or a typo like `${node.x.mesage}` — is a **load-time error**.

**Missing references resolve to nil _silently_ — mark mandatory ones with `!`.** A reference to a missing upstream node, an absent `data` key, or an undeclared input — e.g. `${node.ghost.message}`, `${node.plan.data.typo}`, `${workflow_input.undeclared}` — resolves to empty/nil and the node runs anyway, so a typo'd path or an absent upstream field fails *quietly* and downstream receives blanks. Append `!` to fail loudly instead: `${node.plan.data.summary!}`, `${workflow_input.spec!}`. The suffix covers the whole reference, including `data.*` subpaths. Use it for anything required.

**Validate before running.** `go test ./internal/definitions/` (equivalently `toil validate`) checks namespaces, the Phase-5 `input` rule, `node` field names, and `!`-satisfiability at load time. It cannot check *intent* — that a decision routes where you meant, or that a prompt asks for what the upstream data actually contains. Read the YAML and do a dry run for those.

### Decision Tags

Decisions can carry semantic tags that let the harness surface them in generic ways — dashboard badges, `toil inspect --aspect <X>`, `tree.tagged.<name>` expressions, topology styling — without the harness hardcoding knowledge of specific decision names.

```yaml
decisions:
  - id: force_approve
    description: "Accept with unresolved reviewer concerns"
    tags: [override]
  - id: send_back
    description: "Reviewer was right; engineer must address"
  - id: skip_task
    description: "Partial, move on"
    tags: [override]
```

**How it flows:**

1. Workflow author declares `tags: [...]` on decisions that carry cross-cutting meaning.
2. At node completion, the engine looks up the matched decision in the node's `decisions:` list and copies its `Tags` onto `NodeState.Tags`. Tags are also included in `node_completed` event data for live SSE consumers.
3. Downstream surfaces read `NodeState.Tags` generically: the dashboard renders `override`-tagged nodes with an amber badge + "Review Overrides" section; `toil inspect --aspect review_overrides` returns them; graph renderers can style them distinctly. Adding a new tag-aware surface means reading `NodeState.Tags`, not patching the engine.

**Authoring guidelines:**

- Tags are workflow conventions — pick names that read like what they mean. The canonical one today is `override` for review-escalation waivers. If a new concern emerges (e.g., risk acceptance, flake suppression, audit flags), pick a short descriptive tag name and apply it consistently.
- Tags are declared per-decision, not per-node. A node with decisions `force_approve` (tagged `override`) and `send_back` (untagged) correctly distinguishes waivers from resolutions.
- Tags are materialized at completion time — changes to the YAML after a run completes don't retroactively re-tag historical runs. This matters for audit trails.
- The string `"force_approve"` no longer has any special engine meaning. If a workflow doesn't tag its decisions, none of the surfaces engage — including the amber badge. This is deliberate: the harness stays generic.

**Cross-run queries:**

When a deep child run (e.g., `debug_merge` running inside `integrate` running inside `integrate_component`) needs to see decisions made in sibling or ancestor runs, use `tree.tagged.<name>` rather than threading data through every intermediate workflow:

```yaml
# debug_merge.yaml — merge_engineer reads upstream waivers
inputs:
  upstream_overrides:
    expr: tree.tagged.override
    optional: true
```

The resolver walks the execution group rooted at the current run's top-level ancestor and returns all matching nodes. Each entry carries `run_id`, `workflow_id`, `node_id`, `decision`, `message`, `data`, and `tags`. Empty list (not error) when no matching nodes exist. Missing `TreeResolver` on the context (unit tests) errors loudly rather than silently returning empty.

### Output Protocols for Tagged Decisions

When a decision carries a tag, downstream consumers often expect specific structure inside `data`. These are **prompt-level protocols**, not schema-enforced — the engine doesn't validate the data shape. Document the contract in the node's prompt and trust the agent.

**Example: `override` tag expects `data.waived_concerns`.**

The `resolve_review_dispute` node (in `implement_task.yaml`) has its prompt instruct the agent: when deciding `force_approve` or `skip_task`, emit a structured list:

```json
{
  "decision": "force_approve",
  "data": {
    "waived_concerns": [
      {
        "source": "write_code",
        "concern": "Entrypoint smoke test unresolved",
        "justification": "Verified src/index.ts runs; the engineer's concern is stylistic"
      }
    ]
  }
}
```

The dashboard and `toil inspect` surface `waived_concerns` when present. If missing, a prominent warning surfaces — the prompt required it, so absence signals an unjustified waiver that needs manual audit. This is the recommended pattern for any tag that accepts risk: require structured justification, surface it prominently, flag absence.

### Bootstrap-From-Clean Verification

A recurring class of bug: a scaffold or merged tree passes tests on the author's machine because their PATH has `tsc`, `eslint`, or similar, but a fresh clone without dependencies installed can't even build. The tests "pass" deceptively and the issue surfaces later at integration.

`definitions/workflows/build_component/bootstrap_check.sh` is a reusable shell-level gate that proves the committed tree bootstraps from zero. It:

1. Copies `HEAD` into a scratch directory via `git archive HEAD | tar -x` — no pre-existing `node_modules/`, `vendor/`, `target/`, `.venv/` leaks in.
2. Detects the stack from manifest presence (`package.json` → node, `go.mod` → go, `Cargo.toml` → rust, `pyproject.toml`/`requirements.txt` → python).
3. Runs the canonical install → build → lint → test sequence for each detected stack.
4. Outputs JSON `{decision, message, data}` with decisions `pass`, `fail`, or `no_stack_detected`.

**When to wire it in:**

- **After scaffold tasks** — gates `commit_component` in `build_component.yaml`. Catches scaffolds that pass because of local toolchain state.
- **Pre-merge** — gates `verify_integration` in `integrate_component.yaml`. The exported merged tree must bootstrap before integration starts.
- **As a companion to `env_bootstrap_failed`** — `integrate.yaml`'s `integration_tester` and `e2e_tester` can distinguish toolchain failures (`exit 127`, `command not found`, missing manifest modules) from code-level failures. Route `env_bootstrap_failed` to `bootstrap_repair_integration` / `bootstrap_repair_e2e` (shell runner that installs deps in place) rather than `debug_merge` — repair is cheap; LLM debugging is expensive.

Both scripts are in `definitions/workflows/build_component/` and `definitions/workflows/integrate/`; `integrate_component/bootstrap_check.sh` is a symlink so behavior stays single-sourced.

**Authoring rule for scaffold tasks:** the scaffold task's `acceptance_criteria` must include a bootstrap-from-clean clause:

> Starting from a fresh checkout with no dependency cache, the canonical command sequence — install → build → lint → test — completes successfully.

`plan_tasks` in `build_component.yaml` enforces this for greenfield scaffolds. Workflows that create or modify scaffolds elsewhere should mirror the convention.

### ForEach: Iterating Over Items

A ForEach is declared as two nodes: a **template** (what one item does) and an **orchestrator** (the loop). The template is a normal node with `kind`, `runner`, and `prompt` — it defines the per-item work. The orchestrator has a `for_each:` block that references the template via `body:`. The engine expands the template into one copy per item (`implement::0`, `implement::1`, ...), runs them, and when all items have settled the orchestrator emits an aggregate decision.

#### Canonical Example — `build_component.yaml`

```yaml
nodes:
  # Template: defines what one task does. No incoming edges.
  - id: implement_one_task
    kind: subworkflow
    workflow: implement_task
    inputs:
      project_dir:
        expr: input.project_dir

  # Orchestrator: runs the loop. No kind/runner/workflow.
  - id: implement_tasks
    for_each:
      list: node.plan_tasks.data.plan.tasks   # expression resolving to a list
      item: task                              # each item is passed as "task"
      depends_on: depends_on                  # field name on each item (DAG mode)
      body: implement_one_task                # REQUIRED: the template node's ID
    decisions: [all_succeeded, some_failed, all_failed]

edges:
  # Per-item failure edge — routes a failed item back to the orchestrator.
  # This marks the item as "handled" so siblings continue running.
  - from: implement_one_task
    to: implement_tasks
    when: "status == 'failed'"

  # Aggregate edges — fire after ALL items have settled.
  - from: implement_tasks
    to: verifier
    when: all_succeeded
  - from: implement_tasks
    to: plan_tasks
    when: some_failed
    prompt: |
      Some build tasks failed. node.implement_tasks.data.items has per-item
      status and failure_context for each failed item. Re-plan only the
      failed tasks.
  - from: implement_tasks
    to: plan_tasks
    when: all_failed
    prompt: |
      All build tasks failed. node.implement_tasks.data.items has per-item
      failure_context with decision history. Produce a revised plan.
```

#### Required Fields in `for_each:`

- **`list:`** — expression that resolves to the list of items (e.g., `node.plan_tasks.data.plan.tasks`, `input.items`).
- **`item:`** — variable name; each item is injected into the template's input context under this name. Reference it in template inputs as `expr: item` or `expr: item.some_field`.
- **`body:`** — REQUIRED. The node ID of the per-item template.
- **`depends_on:`** — optional. Field name on each item that lists the IDs of items it must wait for. Enables DAG scheduling (see below). Omit for fully concurrent execution.

#### Aggregate Decisions

After all items settle, the orchestrator emits one of three decisions. Declare all three on the orchestrator and wire edges accordingly:

| Decision | When it fires |
|---|---|
| `all_succeeded` | Every item succeeded (also fires on an empty list) |
| `some_failed` | At least one item succeeded AND at least one failed |
| `all_failed` | Every item failed (skipped items don't count as successes) |

Edges out of the orchestrator use `when: all_succeeded`, `when: some_failed`, and `when: all_failed`.

#### Per-Item Failure Edge

Without a failure edge from the template, a single failed item aborts the entire ForEach (fail-fast). To let siblings continue:

```yaml
- from: implement_one_task    # template ID, not expanded ID
  to: implement_tasks         # MUST route to the orchestrator
  when: "status == 'failed'"
```

When this edge exists, a failing item is recorded as `failed-handled` and its siblings keep running. The orchestrator waits for all items to settle before emitting `some_failed` or `all_failed`.

**Edges from the template must route to the orchestrator.** Routing template edges to any other node is a validation error — per-item complex recovery belongs inside the template (as a subworkflow or local recovery node), not as an edge to an arbitrary downstream node.

#### Items[] Structure

After the orchestrator completes, `node.<orchestrator>.data.items[]` holds a record for each item. Reference individual fields via expressions like `node.implement_tasks.data.items`.

```json
{
  "items": [
    {
      "id": "task-0",           // item's "id" field, or index if absent
      "expanded_id": "implement_one_task::0",  // engine's state node ID
      "status": "succeeded",
      "decision": "approved",
      "message": "Implementation complete",
      "data": { }              // per-item output data
    },
    {
      "id": "task-1",
      "expanded_id": "implement_one_task::1",
      "status": "failed-handled",
      "decision": "",
      "message": "loop exhausted in review_code_quality",
      "failure_context": {
        "child_run": "abc-123",
        "last_decision": "test_changes_requested",
        "decision_history": [ ],
        "failed_child": { "node_id": "review_code_quality", "message": "loop exhausted" }
      }
    },
    {
      "id": "task-2",
      "expanded_id": "implement_one_task::2",
      "status": "skipped",
      "reason": "dependency task-1 failed"
    }
  ]
}
```

**Status values:**

- `succeeded` — item ran and completed cleanly.
- `failed-handled` — item failed and a failure edge exists; siblings continued. `failure_context` is populated.
- `failed` — item failed and no failure edge exists; the orchestrator itself failed (run is fatal without further error-handling edges on the orchestrator).
- `skipped` — DAG mode only; a dependency failed so this item never ran. `reason` is populated.

Downstream nodes can use `node.implement_tasks.data.items` in edge prompts and template inputs to reason about which items failed and why (see the `some_failed` edge prompt in the example above).

#### DAG Scheduling with `depends_on:`

When items carry dependency information, the `depends_on:` field on the orchestrator names the field on each item that lists the IDs of items it must wait for:

```yaml
for_each:
  list: node.plan_tasks.data.plan.tasks
  item: task
  depends_on: depends_on    # each item has a "depends_on" field: ["task-0"]
  body: implement_one_task
```

Independent items run concurrently. An item only starts after all items it depends on have succeeded. If a dependency fails (with a failure edge), all transitive dependents are marked `skipped` rather than started. This lets an otherwise-independent subset of items continue running while a failed branch is skipped.

Omit `depends_on:` when items are fully independent — all items launch concurrently.

**Note on `exclude_inputs`**: Declare input filtering on the orchestrator, not the template. The orchestrator's `exclude_inputs` is applied once as the predecessor's output flows into the ForEach; the template's `exclude_inputs` has no effect today.

**Per-item NodeState status**: Absorbed failures set the expanded item's NodeState.Status to `failed-handled` (distinct from `failed`). This is what makes resume idempotent for handled-failure scenarios and what tools like `toil inspect` can use to distinguish absorbed from unhandled failures.

**Access per-item fields via dotted indices**: `node.<orchestrator>.data.items.0.status`, `node.implement_tasks.data.items.0.failure_context.session_id`, etc.

#### Validation Rules

- `body:` is required. `for_each:` without `body:` is a validation error.
- The template node must exist in the same workflow.
- The orchestrator may NOT have `kind`, `runner`, or `workflow` — those go on the template.
- Edges `from: <template>` must route `to: <orchestrator>`. Routing to any other node is a validation error.
- Nested ForEach is not supported (the template may not itself be a ForEach orchestrator).
- Self-reference (`body: implement_tasks` on `implement_tasks`) is not supported.

### Validation

```bash
# Validate all definitions parse and pass structural checks
go test ./internal/definitions/ -v

# Build both binaries
make build && go build -o bin/tgwm ./cmd/tgwm/

# Full test suite
go test ./internal/... -count=1
```

### Server Management

```bash
# Restart after workflow/runner/env changes
kill $(pgrep -f 'bin/toil serve')
make serve-daemon

# Definitions are loaded at startup — changes require restart
```

## Analyzing Runs

### toil inspect — Structured Run Analysis

`toil inspect` provides structured analysis of any run. All commands hit
the API server; the CLI is a thin client.

```bash
# Overview — status, duration, nodes, models, tokens
toil inspect <run-id>

# Full run hierarchy — recursively follows child_run links
toil inspect <run-id> tree

# What each node decided and why
toil inspect <run-id> decisions

# Where time was spent, bottlenecks, concurrent nodes
toil inspect <run-id> timing

# Token usage, cache hit rate, estimated cost
toil inspect <run-id> tokens

# Decision flow with loop detection and steering annotations
toil inspect <run-id> flow

# Schema validation errors, steering events, silent exits
toil inspect <run-id> errors

# System prompts and edge prompts per node
toil inspect <run-id> prompts

# Run inputs
toil inspect <run-id> inputs

# Node outputs (message, data, artifacts)
toil inspect <run-id> outputs

# Side-by-side comparison of two runs
toil inspect <run-id> compare <other-run-id>
```

**Node-scoped queries** — drill into a specific node:

```bash
# Node overview
toil inspect <run-id> <node-id>

# Per-node transcript (tool calls grouped by attempt and round)
toil inspect <run-id> <node-id> transcript

# Node-specific tokens
toil inspect <run-id> <node-id> tokens

# Narrow to a specific attempt
toil inspect <run-id> <node-id> tokens --attempt 2
```

**Live monitoring** — SSE streaming for in-progress runs:

```bash
toil inspect <run-id> flow --follow
```

### Quick Triage Workflow

1. **Start with overview**: `toil inspect <run-id>` — status, duration,
   node summary
2. **Check the tree**: `toil inspect <run-id> tree` — see full hierarchy,
   find the child run that matters
3. **Check decisions**: `toil inspect <child-run-id> decisions` — what did
   each node decide and why?
4. **Check errors**: `toil inspect <child-run-id> errors` — schema
   validation failures, steering events
5. **Check flow**: `toil inspect <child-run-id> flow` — look for loop
   annotations (same pair 3+ times)
6. **Check timing**: `toil inspect <child-run-id> timing` — find
   bottleneck nodes
7. **Check tokens**: `toil inspect <child-run-id> tokens` — cost and
   cache efficiency

### Navigating the Run Hierarchy

implement_spec runs form trees. Use `toil inspect <run-id> tree` to see the
full hierarchy:

```
implement_spec (signal-barley-ember) [completed] 493s
  build_one_component::0 → build_component (brisk-cedar-cirrus) 255s
    build_one_component::0 → build_component (banyan-spruce-ridge) 255s
      implement_one_task::0 → implement_task (ridge-wave-solstice) 141s
      implement_one_task::1 → implement_task (sparrow-ridge-flint) 57s
  integrate_one_component::0 → integrate_component (sparrow-grove-mist) 238s
```

ForEach items use `::` notation with the TEMPLATE's ID as prefix: `implement_one_task::0`, `implement_one_task::1` for the `implement_tasks` orchestrator whose body is `implement_one_task`.

### Reading Raw events.jsonl

For analysis not covered by inspect aspects, the raw event log is
available:

```bash
# Raw events for a run
curl -s http://localhost:8080/runs/<run-id>/events

# What structured event kinds exist in a run
grep 'node_output' runs/<run-id>/events.jsonl | python3 -c "
import sys, json
seen = set()
for line in sys.stdin:
    e = json.loads(line)
    text = e.get('text', '')
    if text.startswith('{'):
        try:
            inner = json.loads(text)
            kind = inner.get('kind', inner.get('type', ''))
            if kind and kind not in seen:
                seen.add(kind)
                print(kind)
        except: pass
"
```

### Interviewing Serf Sessions

When a node behaves unexpectedly, resume its session and ask the agent directly:

```bash
# Find session ID from events
grep 'SESSION_START' runs/<run-id>/events.jsonl | python3 -c "
import sys, json
for line in sys.stdin:
    e = json.loads(line)
    inner = json.loads(e.get('text','{}'))
    if inner.get('kind') == 'SESSION_START':
        print(f'{e.get(\"node_id\")}: {inner.get(\"data\",{}).get(\"session_id\",\"\")}')"

# Resume with a different model and ask what happened
cd <any-dir> && \
set -a && source ../serf/.env && set +a && \
serf --model openai/gpt-5.4 \
  --reasoning-effort low \
  --state-dir ~/.local/state/serf/projects/<project-hash> \
  --resume <session_id> \
  "STOP. Do not use any tools. Just explain: what were you trying to do and why?"
```

## Debugging Common Issues

### Structured Output Rejected by Runner Schema

**Symptom**: A serf, claude, or codex node tries to emit nested `data.*` content and the runner's schema validator rejects it.

**Cause**: The node's `outputs_schema:` is too strict (typically `additionalProperties: false` on a field whose real value is an object the author didn't enumerate), or required keys don't match what the prompt actually produces.

**Fix**: Broaden or correct the schema so it describes the real output shape. For the `data` portion only — toil wraps `{decision, message, data, artifacts}` automatically. Run `PROJECT_DIR=... go run ./cmd/toil validate` to catch schema-compile errors before a run. See `docs/specs/schemas.md` ("outputs_schema") for the wrapping semantics.

### Shell Node Fails Silently

**Symptom**: Shell node exits with code 1, no output captured.

**Diagnosis**: The script likely failed before producing JSON output. Check:
- Are env vars available? (`set -euo pipefail` + unset var = silent exit)
- Is the script executable? (`chmod +x`)
- Does `$TOIL_WORKFLOW_SCRIPT_DIR` resolve?

**Reproduce**: Run the script manually with the same env:
```bash
COMPONENT_ID=implementation \
TOIL_RUN_ID=test \
TOIL_ROOT=/path/to/toil \
PROJECT_DIR=/tmp/sample-app \
bash -x definitions/workflows/<workflow>/<script>.sh
```

### Cross-Subworkflow Identity

**Symptom**: Merge phase can't find worktree created by build phase.

**Cause**: `TOIL_RUN_ID` differs between subworkflows. Worktree name using `TOIL_RUN_ID` in one subworkflow won't match in another.

**Fix**: Pass a stable identifier (e.g., the parent implement_spec run's ID) as `parent_run_id` input to both subworkflows. Use it for worktree names.

### Bare Repo Identity

**Symptom**: shell node fails with `--repo is required` or operates on the wrong bare repo.

**Cause**: tgwm has exactly one way to identify a bare repo: `--repo <path>`. There is no env-var fallback, no slug guessing, no scanning of `repos/`.

**Fix**: At the top of the implement_spec workflow, capture the bare repo path from `tgwm init`'s stdout: `REPO=$(tgwm init --source "$PROJECT_DIR")`. Thread it through every subworkflow as a `repo` input. Every shell node passes `--repo "$REPO"` to its tgwm calls.

## Improving Agent Prompts

### The Analysis Loop

1. **Run the workflow** against a test spec
2. **Check timing**: Which tasks took longest? Which nodes had multiple attempts?
3. **Trace decisions**: Extract the communicate calls to see what each node decided and why
4. **Identify patterns**: Are rejections legitimate? Are review loops productive or bouncing?
5. **Check the prompts**: Does the prompt clearly explain what the agent should do? Are the decision descriptions unambiguous?
6. **Edit and re-run**: Change the prompt, restart server, trigger new run, compare

### What to Look For in Transcripts

**Productive review loops**: Reviewer found a real issue, engineer fixed it, reviewer approved. These are working correctly. Don't "fix" them.

**Bouncing loops**: Same issue goes back and forth between write_code and verify_code_meets_acceptance_criteria 3+ times. Usually means:
- The reviewer is looking at the wrong thing (code vs tests)
- The edge prompt doesn't give enough context about WHY this node was triggered
- The reviewer's rejection criteria are unclear

**Wasted write_tests cycles**: write_tests declares `tests_already_passing` without verifying existing tests cover the current task's acceptance criteria. Then verify_code_meets_acceptance_criteria rejects, sending work through unnecessary loops.

**Slow rounds**: A single round taking 60+ seconds usually means a large tool call (writing a big test file or patch). Check if the task is too large and should be decomposed further.

**Steering events**: Serf injecting steering prompts means the model is going off-track. Check if the node prompt is clear enough.

### Task Schema Hierarchy

Every agent that receives a task should understand this hierarchy:

| Field | Binding? | Role |
|-------|----------|------|
| `acceptance_criteria` | Yes | The definition of done. Reviewers reject when these are unmet. |
| `interfaces` | Yes | Architectural contracts (types, signatures, selectors). Must be implemented as specified. |
| `guidance` | No | Advisory notes from the architect. Follow when possible, but not grounds for rejection. |
| `steps` | No | Suggested approach. Not prescriptive. |
| `files` | No | Expected file scope. Not a hard boundary. |

This hierarchy must be consistent across write_tests, write_code, verify_code_meets_acceptance_criteria, and review_code_quality prompts. If one agent treats guidance as binding and another treats it as advisory, review loops result.

### Surgeon Decomposition Principles

**File ownership determines independence.** No two concurrent tasks may list the same file. Each task gets its own test file. Violation causes concurrent tasks to write conflicting changes to shared files.

**Plan the delta, not from scratch.** When the workspace has existing code, plan_tasks should explore it and plan only what's missing or broken. Don't redesign interfaces that already satisfy the spec.

**Dependencies are about data flow AND file conflicts.** A task depends on another when:
- It can't start work without the other's output (imports, types)
- Both tasks need to modify the same file (file ownership conflict)

**Smaller tasks are faster.** Each task goes through a full TDD + review cycle. A task with one responsibility and 2-3 acceptance criteria moves through faster than a task with five responsibilities. More tasks with parallelism beats fewer sequential tasks.

### Testing Prompt Changes

After editing a workflow prompt:

1. `go test ./internal/definitions/` — structural validation
2. `make build` — rebuild
3. Kill server, `make serve-daemon` — restart with new definitions
4. Trigger an implement_spec run against a simple spec (e.g., "build a simple calculator app")
5. Compare results against the previous run:
   - Same number of tasks? Better decomposition?
   - Fewer review attempts? Less bouncing?
   - Faster total time?
   - Legitimate quality catches still working?

### Serf Environment for Manual Testing

Run serf standalone to test prompt variants:
```bash
cd /tmp && \
set -a && source /path/to/serf/.env && set +a && \
serf --model openai/gpt-5.4 \
  --reasoning-effort low \
  --max-rounds 10 \
  "Your test prompt here"
```

Note: standalone serf doesn't have the toil communicate tool or output format instructions. For testing plan_tasks prompts, you need the full toil context (trigger a real run). For testing isolated logic, standalone works.

## Workflow Design Principles

### Shell for Mechanics, LLM for Judgment

The happy path should be fully mechanical (shell nodes) wherever possible. LLMs are reserved for tasks requiring judgment — planning, code generation, conflict resolution, code review. This minimizes hallucination risk and cost.

Example: integrate_component uses a shell node for the merge attempt. Only on conflict does an LLM resolver get involved.

### Converging Paths Over Duplicate Nodes

When two paths lead to the same outcome, converge at a shared node rather than duplicating downstream nodes. Two edges into one node (without `join: all`) fires the node on whichever edge arrives.

### Edge Prompts Carry Context

The `prompt` field on edges tells the next node WHY it was triggered. This is critical for nodes that receive input from multiple edges (e.g., verify_code_meets_acceptance_criteria can be reached from write_code via `code_written`, `spec_issue`, or from write_tests via `tests_already_passing`). The edge prompt should give the node enough context to act correctly.

### Review Loop Circuit Breakers

When write_code escalates `spec_issue` and verify_code_meets_acceptance_criteria keeps deciding `code_changes_requested`, the loop is stuck. The edge prompt from `spec_issue` should tell verify_code_meets_acceptance_criteria to check TESTS first, since that's usually the root cause.

`max_loop_iterations` on the workflow provides a hard cap, but prompt-level guidance should prevent most bouncing before the cap is hit.

## Serf Event Format Reference

Runner output is double-encoded: structured JSON from serf appears
inside the `text` field of `node_output` events with `stream: "stderr"`.
Each event has a `kind` field. Here's where data actually lives:

### Token Usage → ASSISTANT_TEXT_END

Token counts are in `ASSISTANT_TEXT_END`, NOT in `ROUND_TIMINGS`.

```json
{"kind": "ASSISTANT_TEXT_END", "data": {
  "model": "gpt-5.4-2026-03-05",
  "usage": {
    "input_tokens": 12120,
    "output_tokens": 240,
    "cache_read_tokens": 11136,
    "reasoning_tokens": 45
  }
}}
```

### Timing Breakdowns → ROUND_TIMINGS

Per-round nanosecond timing only. No token data.

```json
{"kind": "ROUND_TIMINGS", "data": {
  "round": 0,
  "llm_call_ns": 5104100167,
  "tool_exec_ns": 1772417,
  "total_round_ns": 5112762375
}}
```

### Session Info → SESSION_START

Model, provider, and session ID. Note: `session_id` is at the top
level, not inside `data`.

```json
{"kind": "SESSION_START", "session_id": "01KPJ5ZP...", "data": {
  "profile": "openai",
  "model": "gpt-5.4",
  "context_window_size": 128000
}}
```

### Decisions → TOOL_CALL_START (communicate)

Agent decisions go through the `communicate` tool. The output is
double-encoded as JSON inside `arguments_json`.

```json
{"kind": "TOOL_CALL_START", "data": {
  "tool_name": "communicate",
  "arguments_json": "{\"output\":{\"decision\":\"approved\",\"message\":\"...\"}}"
}}
```

### Other Event Kinds

| Kind | What It Contains |
|------|-----------------|
| `ASSISTANT_TEXT_START` | Model name at start of generation |
| `TOOL_CALL_END` | Tool output (full, not truncated) |
| `TOOL_CALL_OUTPUT_DELTA` | Streaming tool output chunks |
| `STEERING_INJECTED` | Serf steering prompt (model went off-track) |
| `SESSION_END` | Reason, state, turn count |
| `PROMPT_LOADED` | System prompt sections loaded |
| `COMMUNICATE` | Serf-internal communicate event |
