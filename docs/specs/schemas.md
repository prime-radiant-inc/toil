# Definition Schemas

Runner definition
Runner files are YAML.

Required fields
- id: string
- type: string (claude | codex | serf | shell | human)

Optional fields
- command: string
- args: list of strings
- env: map of string to string
- timeout_sec: number (per-invocation timeout in seconds)
- resume: boolean (whether the runner supports session resume)

Workflow definition
Workflow files are YAML.

Top-level fields
- id: string (required)
- name: string (required)
- version: number (required)
- description: string
- inputs: map of input name to type description
- input_schema: map of input name to input spec (see below)
- outputs: map of output name to type description
- prompt_inputs_mode: string (default mode for how a node's inputs are surfaced into prompts; all | declared | none; default declared; overridable per node)
- workspace_defaults: workspace object (see below)
- nodes: list of node objects (required)
- edges: list of edge objects (required)
- limits: map
  - max_loop_iterations: number (caps loop edge traversals)
  - max_no_progress_iterations: number (caps identical dispatch/output cycles, default 3)
- tags: list of strings
- runner_overrides: map of tag to runner id (tag-based runner selection)
- retry_target: string (node id to re-execute when any goal gate is unsatisfied)
- context_default: string (default context mode for all nodes; full | fresh | compact | summary)
- interview: string (interview trigger mode; never | on_failure | on_issue; default never)

Node fields
- id: string (required, unique within workflow)
- kind: string (required; role | human | system | subworkflow | emit)
- role: string (optional free-text display label shown on the dashboard and graph, e.g. `surgeon`; not used for execution or runner selection. There are no role definition files — this is a label only.)
- runner: string (runner id; selects the runner for this node — see "Runner selection precedence"; shell runners skip prompt format injection and pass inputs as uppercased env vars)
- runner_env: map of string to string (extra environment variables passed to this node's runner invocation; a load-time warning is emitted if set without a `runner`)
- prompt_inputs_mode: string (per-node override of how inputs are surfaced into the prompt; all | declared | none; falls back to the workflow-level value, then to declared)
- tags: list of strings (matched against runner_overrides for runner selection)
- workflow: string (workflow id, required for kind subworkflow)
- prompt: string (inline prompt text)
- context: string (context mode override; full | fresh | compact | summary)
- session_id: string (explicit session id to resume; may be an expression such as `${input.original_session}`; subject to resume-integrity validation — see "Bundle-level validation")
- prompt_on_resume: boolean (when true, the node's `prompt:` is re-sent on a resume dispatch that has no transition/edge prompt; otherwise resume dispatches send no prompt)
- retry: retry policy object (see below)
- inputs: map of name to value (see below; Phase 1 evaluation)
- outputs_schema: JSON Schema object describing the shape of `data` in this node's output envelope. Toil wraps it into the full envelope schema `{decision, message, data, artifacts}` before passing to runners; `decision.enum` is derived from the node's `decisions:`. Not supported on shell, subworkflow, or human nodes (validation error). See "outputs_schema" below.
- decisions: list of valid decisions (each entry is a plain string, or an object `{id, description, tags}`; see "Decisions" below)
- gate: string (none | optional | required)
- goal_gate: boolean (when true, node must complete for the run to succeed)
- retry_target: string (node id to re-execute when this goal gate is unsatisfied)
- timeout_sec: number (seconds before auto-resolving a human/approval gate:required node; triggers _timeout meta-decision)
- loop: loop object (see below)
- for_each: for-each object (see below)
- join: string (fan-in behavior for multiple upstream sources; only "all" is supported — all required upstream edges must arrive; default all)
- workspace: workspace object (see below)
- max_turns: number (maximum agentic turns per invocation; only effective on claude and codex runners; validation warns if set on a node whose resolved runner is neither claude nor codex)
- output: emit output object (required for kind emit; see "Node Kind: emit" below)
- loop_exhaustion: string (opt-out policy for the SCC coverage lint; allowed values: "" (default) or "fatal". When a node can loop and the workflow declares max_loop_iterations but has no outgoing `when: _loop_exhausted` edge, the loader emits a warning. Set `loop_exhaustion: fatal` to explicitly accept the legacy fatal-exhaustion behavior and silence the warning.)

Fields removed in v7: `inputs_from`, `loop_exhausted_to`, `timeout_default`, `exclude_inputs`.
These were replaced by edge `passes:` blocks (for data flow), `_loop_exhausted` meta-decisions
(for loop exhaustion routing), and `_timeout` meta-decisions (for timeout routing). They are no
longer recognized fields: a workflow that still declares them loads but emits an
unknown-key warning (logged as `toil.workflow.unknown_key`), and the field is silently ignored.

Input spec fields (for input_schema entries)
- type: string
- optional: boolean (when true, missing input.<name> resolves to null)
- description: string

Decisions

A node's `decisions:` is a list of the decision values it may emit. Each entry is either a
plain string or an object:

- `id`: string (the decision value)
- `description`: string (optional; human-readable explanation, surfaced in the UI)
- `tags`: list of strings (optional; cross-cutting semantic labels — e.g. `override` for
  review-escalation waivers)

The two forms may be mixed in one list:

```yaml
decisions:
  - pass
  - { id: escalate, description: "Send to a human reviewer", tags: [override] }
```

Tags are workflow-authored conventions — the engine treats any string as a valid tag. When a node
completes on a tagged decision, the engine materializes the matched tags onto the node's state and
into the `node_completed` event, where downstream consumers (dashboard, `tree.tagged.<tag>`
expressions, topology renderers) read them.

Node inputs
The `inputs:` map on a node declares data that should be available as `${input.X}` during Phase 5
(prompts and emit output). Values are evaluated in Phase 1 (before edge passes) and may be:

- A string expression: `"${workflow_input.task}"`, `"${node.reviewer}"`, `"${env.API_KEY}"` — the
  standard `${expr}` form. Use `${expr!}` to mark the reference as required (error if missing).
- A literal scalar: `42`, `true`, `"static text"` — passed through unchanged.
- A nested map: recursive structure; leaf values follow the same rules above.

`${input.X}` is NOT valid inside `inputs:` or edge `passes:` values (Phase 1/2 context). It is
only valid in Phase 5 (prompts, emit output). Use `${workflow_input.X}` to reference run-start
inputs in early phases. See "Resolution Phase Ordering" below.

outputs_schema

A node's `outputs_schema:` is a JSON Schema object that describes only the
shape of `data` in the output envelope. Toil wraps it into the full runner-
visible envelope schema `{decision, message, data, artifacts}` before
dispatch, and translates it to the runner's native flag:

- claude: `--json-schema '<json>'`
- codex:  `--output-schema <file>` (toil writes a temp file internally)
- serf:   `--output-schema '<json>'`
- shell / human: not supported

The envelope wrapper:

- `decision` is required and its `enum` is derived from the node's
  `decisions:` list (open string when `decisions:` is empty).
- `message` is required and must be a non-empty string.
- `data` is the author's `outputs_schema:` verbatim (or an open object when
  `outputs_schema:` is absent).
- `artifacts` is required and is an array of strings.
- `additionalProperties: false` on the envelope.

Example:

```yaml
- id: surgeon
  kind: role
  runner: serf
  decisions: [ready_for_review]
  outputs_schema:
    type: object
    required: [plan]
    additionalProperties: false
    properties:
      plan:
        type: object
        required: [file_map, architecture, tasks]
        properties:
          file_map:     { type: string }
          architecture: { type: string }
          tasks:
            type: array
            items:
              type: object
              required: [id, name]
              properties:
                id:   { type: string }
                name: { type: string }
```

Rules:

- `outputs_schema` on a shell, subworkflow, or human node is a validation
  error (shell scripts produce JSON themselves, subworkflow nodes dispatch
  to a child run, and the human runner does not enforce schemas).
- The schema must be a valid JSON Schema; validation errors surface at
  workflow load time.
- The deprecated node-level `outputs: [key]` field has been removed.
  Workflows that still declare it will fail to load with a clear error
  pointing to the offending node IDs.

See `docs/plans/2026-04-23-outputs-schema-design.md` for the full design.

Edge fields
- from: string (source node id)
- to: string (target node id)
- when: string (decision value to match, or "default" for fallback; also accepts meta-decision values — see "Meta-Decisions")
- prompt: string (context text passed to the target node when this edge is traversed)
- passes: map of name to value (per-edge data declarations; evaluated in Phase 2; same value forms as node inputs — string expressions or literal scalars; see "Resolution Phase Ordering")
- failed: boolean (only valid on meta-decision edges — `when:` values beginning with `_`. Required on those edges: set `true` if this routing represents a run-level failure, `false` if it is a normal terminus. Setting `failed:` on any non-meta-decision edge is a load-time error.)

Convergent edges: when multiple edges converge on the same target node, all their `prompt:` values
must be identical (or absent). Differing prompts across convergent edges to the same destination
is a load-time error.

Retry policy fields
- max: number (maximum retry attempts; total executions = max + 1)
- backoff: string (fixed | exponential; default exponential)
- initial_delay: string (Go duration, default "1s")
- max_delay: string (Go duration, default "30s")
- jitter: boolean (when true, applies +/-50% random variance to delay)

Loop fields
- on: string (decision that triggers the loop-back)
- back_to: string (node id to return to)

For-each fields
- list: string (input expression resolving to a list, e.g. `${workflow_input.items}` or `${node.surgeon.data.plan.tasks}`; needed at expansion time — only `body` is checked by `toil validate`, a missing `list` fails at dispatch)
- item: string (variable name bound to each element during expansion; referenced as `${input.<item>}` in the template's expressions; like `list`, required at expansion time but not load-validated)
- body: string (REQUIRED; node ID of the per-item template — a separately-declared node providing `kind`, `runner`, `prompt`, and `inputs` for each iteration)
- depends_on: string (optional; when items are structured maps, names the field containing per-item dependency IDs for DAG scheduling; when omitted, all items run concurrently)

Workspace fields
- mode: string (isolated | shared | group | project)
- group: string (group name, required when mode is group)
- path: string (directory path, required when mode is project; supports ${VAR} expansion)
- access: string (accepted values `read_write` | `read_only`; parsed but not consumed by the engine — best-effort/reserved)

Expression Syntax

Expressions use the `${<namespace>.<path>}` form. The six supported namespaces are:

- `input` — the merged dispatch map for the current node (workflow inputs + node inputs + edge
  passes, after all phases complete). **Phase 5 only** — valid in prompts and emit output blocks;
  a load-time error if used inside `inputs:` or `passes:` values.
- `workflow_input` — run-start inputs provided by the caller. Available in all phases.
- `node` — output from a completed node. Path forms:
  - `node.<id>` — full output map `{decision, message, artifacts, data, session_id, tags, status, attempts, last_routing_decision, loop_iterations}`
  - `node.<id>.<field>` — single field; supported fields: `decision`, `message`, `artifacts`,
    `data`, `session_id`, `tags`, `status`, `attempts`, `last_routing_decision`, `loop_iterations`
  - `node.<id>.data.<path>` — nested path into the data map
- `env` — environment variable: `${env.API_KEY}`
- `run` — current run metadata: `${run.id}`
- `tree` — cross-run group: `${tree.tagged.<tag>}` returns all completed nodes across the
  execution group whose decisions carry the named tag (see `docs/specs/software-factory.md`)

Suffixes:
- `${expr}` — optional reference; resolves to null if the value is absent
- `${expr!}` — required reference; error if the value is absent or null

Escaping: `$${` is a literal escape for the two-character sequence `${`. Use it in prompt text
when you need to write a literal dollar-brace that should not be interpreted as an expression.

Type preservation: when an entire YAML string value is a single `${expr}` token and nothing else,
the resolved Go value is passed through with its original type (map, list, number, bool). When
`${expr}` appears embedded within surrounding text, the resolved value is stringified.

YAML quoting: expression strings must be quoted in YAML to avoid the `{` being interpreted as a
map literal. The one exemption is `prompt: |` block-scalar bodies, where YAML does not parse
inline flow nodes — expressions in block-scalar prompts do not need quoting.

Meta-Decisions

Meta-decisions are synthetic decision values emitted by the engine (not by a runner) when
structural conditions are met. They appear as `when:` values on edges. Every meta-decision edge
MUST declare a `failed:` boolean (true if the routing is a run-level failure, false if it is a
normal terminus); omitting it is a load-time error.

The runtime conditions under which the engine emits each meta-decision (loop count reaching
the cap, gate timeout expiring, retry budget being consumed) are owned by `docs/specs/runtime.md`.
This section covers only the edge-schema `when:` values and their load-time validation rules.

`_loop_exhausted`
The node whose loop is exhausted must be part of a loopable cycle in the workflow graph —
declaring `_loop_exhausted` on an edge from a node that cannot loop is a load-time error
(SCC structural rule).

`_timeout`
A node that declares `timeout_sec` and `gate: required` MUST have at least one outgoing edge with
`when: _timeout` (inverse rule — enforced at load time).

`_retry_exhausted`
Routes a node whose retries are all consumed, distinguishing "every retry failed" from a
first-attempt non-retryable failure (the latter still routes via `when: status == 'failed'`
edges). The source node MUST declare `retry: { max: > 1 }` — without retries the meta-decision
can never fire (enforced at load time). If no `_retry_exhausted` edge is present, behavior is
unchanged: retry exhaustion falls through to legacy failure edges.

Node Kind: emit

`kind: emit` nodes produce a fixed output without invoking any runner. They are declared with
an `output:` block instead of a `prompt:` and runner.

`output:` block fields:
- `decision`: string (required; should be a value in the node's `decisions:` list — by convention, not load-validated; an undeclared value is emitted verbatim)
- `message`: string (required; template string; `${input.X}` is valid here — Phase 5)
- `data`: map (optional; recursive map of values; leaf values may be `${input.X}` expressions)

Example:

```yaml
- id: summarize
  kind: emit
  decisions: [done]
  inputs:
    result: "${node.processor}"
  output:
    decision: done
    message: "Processed ${input.result.message}"
    data:
      outcome: "${input.result.data}"
```

Constraints:
- `kind: emit` nodes are not expected to declare `runner`, `retry`, `timeout_sec`, `loop`, or `outputs_schema` (they are ignored on emit nodes; the loader does not currently reject them).
- Emit nodes may be ForEach body nodes.
- Emit nodes may be subworkflow terminal nodes.

Resolution Phase Ordering

When the engine dispatches a node, inputs are resolved in five phases:

1. **Node Inputs** — evaluate the node's `inputs:` map. May reference `${workflow_input.X}`,
   `${node.X}`, `${env.X}`, `${run.X}`, `${tree.X}`. `${input.X}` is forbidden here.
2. **Edge Passes** — evaluate the traversed edge's `passes:` map (same namespace rules as Phase 1;
   `${input.X}` is forbidden).
3. **Merge** — merge the three layers: workflow-level inputs < node `inputs:` < edge `passes:`.
   Edge passes take highest precedence; duplicate keys from lower layers are overwritten.
4. **Expose** — the merged map is exposed as the `input` namespace, making `${input.X}` valid.
5. **Prompt + Emit** — evaluate prompts, emit `message`/`data`, and any remaining Phase 5
   expressions using the fully-resolved `input` map.

For full semantics see `docs/superpowers/specs/2026-05-18-edge-inputs-design.md`.

Input expressions (summary)
- `${input.X}` — merged dispatch map (Phase 5 only)
- `${workflow_input.X}` — run-start inputs (all phases)
- `${node.<id>}` — full output map from a completed node
- `${node.<id>.<field>}` — single field from a completed node
- `${run.id}` — current run ID
- `${tree.tagged.<tag>}` — cross-run group (see docs/specs/software-factory.md)

Choosing granularity for downstream consumers

When a downstream node needs to reason about an upstream node's *findings*
(not just its routing decision), prefer `${node.X}` (bare, full output) or
explicit `${node.X.data}` forwarding. The `.message` field is a one-line
summary intended as a human-readable tag on the dashboard — it's not the
evidence. Structured findings live in `.data` per the node's declared
output schema.

Concrete rule of thumb:

- **Routing or display** — use `.decision` or `.message` alone.
  Examples: `when:` edge guards, dashboard labels, simple acknowledgements.
- **Judge / triage / escalation** — use `node.X` (bare). The judge needs
  everything: decision to know what was claimed, message for context,
  data for the structured concerns. Underfeeding here is how waivers end
  up "too vague to verify" when the concrete concerns were right there
  in `.data`.
- **Debug / diagnose / rework** — forward `error_context` (the message)
  AND `error_data` (the structured findings). `error_data` is where the
  failing-test names, file:line citations, and reproduction details live.
- **Loop-back edges** — if an edge's prompt says "read the X input and
  fix accordingly," the target node MUST declare `X` as an input.
  Otherwise the agent is told to read something that isn't there.

The `${node.<id>}` (bare) form exists precisely because enumerating
`{decision, message, data}` × N upstream nodes is verbose and easy to
under-specify (a common bug: forwarding only `.message` and losing
`.data`). Prefer the bare form unless you have a reason to narrow.

Output format
All role and human nodes must end with a JSON output object (plain JSON or fenced `json` block).

Required keys
- decision: string
- message: string

Optional keys
- artifacts: list of strings
- data: map

If parsing fails, the engine attempts output repair; if repair fails, the node is marked failed and raw output is still logged.

Runner selection precedence
1. node.runner (explicit runner on the node)
2. First matching tag in node.tags against workflow.runner_overrides

If neither resolves a runner, the node has no runner configured and dispatch fails with an error.
There is no engine-wide default runner.

Validation
Definitions and run inputs are validated at CLI and API boundaries. Runtime
guard checks still apply during execution.

Graph-level validation (per workflow):
- Duplicate node IDs (error)
- Node IDs containing the reserved substring "::" (error; used for ForEach expansion naming)
- Edge references to nonexistent nodes (error)
- Unreachable nodes (warning)
- Edge when values not matching declared decisions (warning)
- Invalid context / context_default values (error)
- Invalid prompt_inputs_mode values (error)
- Invalid retry targets (error)
- outputs_schema on shell/subworkflow/human nodes or invalid JSON Schema (error)
- _loop_exhausted edge source not in a loopable SCC (error)
- _timeout edge without gate:required + timeout_sec on source node (error)
- gate:required + timeout_sec node missing outgoing _timeout edge (error)
- _retry_exhausted edge on node without retry: { max: > 1 } (error)
- Unknown meta-decision in a `when:` value (error)
- Meta-decision edge missing required `failed:` field (error)
- `failed:` set on a non-meta-decision edge (error)
- goal_gate without retry_target (warning)
- Convergent edges to same target with differing prompts (error)
- Convergent edges to same target with overlapping passes: keys (INFO; highest edge-index wins)
- Join node structural rules (only join: "all" supported; zero-incoming, conditional, foreach, or self-loop edges into a join — errors; single incoming edge — warning)
- ForEach body/template/orchestrator structural rules (error)
- Node can loop (non-trivial SCC) and workflow declares max_loop_iterations but has no outgoing _loop_exhausted edge and loop_exhaustion is not "fatal" (warning; set loop_exhaustion: fatal to silence)
- Invalid loop_exhaustion value (error; only "" or "fatal" allowed)

The removed v7 fields (`inputs_from`, `loop_exhausted_to`, `timeout_default`, `exclude_inputs`)
are not special-cased here; they surface as unknown-key load warnings (see "Fields removed in
v7" above). The deprecated node-level `outputs:` field is the exception — it is a hard load error.

Bundle-level validation (cross-workflow):
- Node `runner` references to nonexistent runners (error)
- Subworkflow references to nonexistent workflows (error)
- Subworkflow cycles (error)
- runner_overrides referencing unknown runners (error)
- runner_overrides tag matching no nodes (warning)
- max_turns set on a node whose resolved runner is neither claude nor codex (warning)
- Resume integrity: a node sets `session_id` but its runner type cannot resume (only serf/claude/codex can), or pairs `session_id` with `context: fresh`, or overrides a model-identity env var via `runner_env` on a resuming node (error)

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code (promoted to evergreen reference; Phase-3 verified)._
