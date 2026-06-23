# Document Model

The **document model** is the structured, tree-shaped representation of a run's
execution group that the run-detail view renders as a single readable,
foldable scroll. It lives in `internal/document` and is built on demand from
on-disk run state and event logs — nothing about the document is persisted.

Where the topology graph (`internal/visualize`) answers "what is the shape of
this workflow," the document answers "what happened, in order, when this run
executed." It folds a root run, its sub-runs, and any ForEach fan-out into one
chronological transcript with per-node disclosure rows.

## Concepts / data model

The types live in `internal/document/model.go` and `internal/document/tree.go`.

### Document

`document.Document` is the top-level value. It carries metadata about the root
run (`RootRunID`, `RootTitle`, `RootStatus`, `TotalRuns`) plus a *brief block*
(`BriefText`/`BriefSource`/`BriefFields`) that orients the reader from the run's
inputs — `inputs.spec` when present, otherwise a short list of `BriefField`
key/value pairs (`buildBrief`, `briefFieldsFromInputs`). The tree itself hangs
off `Document.Root`, a `*RunNode`.

### RunNode

`document.RunNode` is one workflow run — root or sub. It holds run-level metadata
(`RunID`, `WorkflowID`, `WorkflowName`, `Title`, `Status`, terminal `Decision`/
`DecisionFamily`, `DurationMs`) and an optional inline `Topology`
(`visualize.TopologyGraph`) for the run's own node graph. Its `Children` is an
ordered list of `NodeChild`. Two derived flags shape rendering: `Compact` (set by
`isCleanSubtree` when every leaf completed cleanly — no retries, no bad-family
decision, no running node) and `Summary` (a `·`-joined list of the run's row
roles, from `summarizeChildren`).

### NodeChild — the three child variants

`NodeChild` is a sealed interface with three implementations, each marshaling
with a `"kind"` discriminator so templates and JSON consumers can branch:

- **`RowChild`** (`"row"`) — one node execution within a run. The unit of
  disclosure. Carries `NodeID`, `Role`, `Runner`, attempt position
  (`AttemptOrdinal`/`AttemptTotal`), session info (`SessionID`/`IsResume`),
  `Decision`/`DecisionFamily`/`DecisionDescription`/`DecisionMessage`, timing,
  optional `CostUSD`, inline `Artifacts`, a `DisclosureHint`, the local `Prompt`,
  and the per-execution `Transcript`.
- **`SubRunChild`** (`"subrun"`) — a single dispatched sub-workflow, wrapping a
  child `*RunNode`. Recurses.
- **`ParallelChild`** (`"parallel"`) — the N sub-runs spawned by one ForEach
  iteration of a base node. `Index`/`Total` disambiguate iterations ("iteration 1
  of 2") when a base re-fans-out after a retry; `Outcome` carries the base node's
  decision for that iteration.

### Transcript

`document.Transcript` is a node execution's trace, segmented per `Attempt`. Each
`Attempt` has an `Ordinal`, an `Outcome` (`succeeded`/`failed`/`""`), and an
ordered `[]Message`. A `Message.Kind` is one of `system_prompt`, `user_prompt`,
`assistant`, `tool_call`, or `decision`:

- `assistant` / `user_prompt` carry `Text` plus pre-rendered, sanitized `HTML`
  (goldmark + bluemonday, in `BuildTranscript`).
- `tool_call` carries a `MessageTool` (tool name, args, paired `Result`, and an
  optional server-rendered `MessageDiff` for `edit_file` calls).
- `decision` carries a `MessageDecision` (id, description, family, tags).

### ArtifactRef and inline detail

`ArtifactRef` (name/kind/desc) is a short reference shown inline on a row so the
reader sees what the agent produced — a child run, a commit, a plan, a ForEach
fan-out — without expanding. `rowArtifacts` derives these from common
`NodeState.Data` shapes (max three). `disclosureHint` produces the short string
on the "show details" affordance.

## Behavior / lifecycle

### Loading abstraction

The builder reads through `document.Loader` (`LoadRun`, `ChildRuns`) so it can
be unit-tested without disk. The real implementation, `RunStoreLoader`
(`internal/document/runstore.go`), reads `state.json`, `events.jsonl`, and the
per-run `workflow.yaml` snapshot from `<runs-dir>/<id>/`, and also satisfies the
optional `EventLoader` and `WorkflowSnapshotLoader` interfaces. A `Registry`
(implemented by `WorkflowRegistry`) resolves machine ids to display names,
runners, edge targets, and decision metadata; a `PromptResolver` (implemented by
`WorkflowPromptResolver`) resolves a node's local prompt from the workflow
template.

### Building the tree

`BuildDocument` (and the registry/resolver-aware
`BuildDocumentWithRegistryAndResolver`) drives the build:

1. `buildRunNode` constructs each `RunNode`. If the run's event log has
   `node_started` events, `walkTreeEvents` produces children in chronological
   order — one `RowChild` per `node_started`, matched to its
   `node_completed`/`node_failed` by `findExecutionCompletion`; ForEach bases
   become `ParallelChild`s; `subworkflow_started` (or a node carrying
   `Data["child_run"]`) becomes a `SubRunChild`. Runs with no event log fall back
   to `walkTreeByState`, which emits rows ordered by `NodeState.StartedAt`.
2. `annotateAttemptTotals` sets `AttemptTotal` per `NodeID`; `buildRunNode` then
   sets `Compact`, `Summary`, `DurationMs`, and terminal `Decision`. When a
   workflow snapshot is available, an inline per-run `Topology` is attached.
3. `enrichRunNode` (`tree_enrich.go`) hydrates each `RowChild` from its
   per-execution event slice (`SliceExecutionEvents`): the local prompt, the
   `Transcript` (`BuildTranscript`), per-attempt session/resume, inline
   artifacts, and a `Result` (the last `communicate` tool-call message, falling
   back to `NodeState.Message`).
4. Optional annotation passes apply registry names/roles/runners/next-targets,
   decision descriptions, and resolver-supplied prompts across the tree.

The root `RunNode` never auto-compacts — on the run-detail page the root *is* the
page header.

### Decision families

`decisionFamily` / `classifyDecision` map a workflow's decision id onto a small
set of display families (pass/fail/escalate/skip/neutral, with renderer-facing
variants `ok`/`bad`/`plan`) so the UI can color rows consistently without the
document hardcoding per-workflow semantics. `DecisionFamily` is the exported
entry point used by the API to enrich decision messages.

### Building the transcript

`BuildTranscript` walks a node's event slice into `Attempts → Messages`:

- Attempts are demarcated by `node_attempt_started` events; older runs without
  them fall back to using `node_started` as attempt markers.
- `node_prompt` is split via `ExtractLocalPrompt` into a `system_prompt`
  (boilerplate) message and a `user_prompt` (LOCAL) message.
- `node_output` lines that are serf `TOOL_CALL_START`/`TOOL_CALL_END` NDJSON
  become paired `tool_call` messages; other serf event kinds are ignored; the
  agent's final structured-output dump (`{decision, message, …}`) is suppressed;
  remaining lines coalesce into `assistant` text.
- `node_completed` appends a `decision` message and marks the attempt succeeded;
  `node_attempt_failed` marks it failed with a reason.

`UnifiedDiff` (`internal/document/diff.go`, a Myers shortest-edit-script
implementation) computes diff hunks from two strings. The document builder does
not populate `MessageTool.Diff` itself — `edit_file` diffs are extracted and
wired into the transcript by the API layer (`enrichTranscriptDiffs` in
`internal/api/server.go`), which calls `UnifiedDiff`.

### LOCAL prompt markers

A node's full rendered prompt typically mixes reusable role boilerplate with the
attempt-specific instructions for *this* node. The markers `LocalMarkerOpen`
(`<!-- LOCAL -->`) and `LocalMarkerClose` (`<!-- /LOCAL -->`), defined in
`internal/document/prompt.go`, delimit the attempt-specific section.
`ExtractLocalPrompt` returns `(local, boilerplate)`: text between the markers is
*local* and shown in the document's quote-prompt block; everything outside is
*boilerplate*, suppressed behind a "show role prompt" affordance. When no markers
are present the entire prompt is returned as local (conservative fallback), so an
unmarked workflow still renders sensibly.

### Compound and run-tree graphs

`BuildCompoundGraph` and `BuildRunTreeGraph` (`compound_graph.go`) walk the runs
directory to assemble the whole execution group — climbing to the group root via
`ParentRun`, then collecting all descendants — and delegate to
`internal/visualize` to produce the orientation graph for the run-detail view.

### Agent-session lookup

`FindAgentExecutions` (`agent_executions.go`) scans every run for `node_started`
events carrying a given agent `session_id`, returning the executions (with
`Ordinal` for event slicing) sorted by start time — the data behind the
per-session diagnostic page.

## Surfaces

The document model is exposed over two HTTP routes — the full `Document` for an
execution group and per-row disclosure detail. `api.md` owns the route
signatures, query params, and the climb-to-group-root behavior; handlers are in
`internal/api/server.go`.

The row-disclosure response is document-model-specific. For a single node row it
carries `inputs`, `outputs`, `transcript`, and a split `prompt`
(`local`/`boilerplate`/`boilerplate_bytes`, from `ExtractLocalPrompt`).
`handleDocumentRow` builds the transcript for the requested execution window
(scoped by attempt via `SliceExecutionEvents`) and applies the API-layer
enrichment — decision metadata and the `edit_file` diffs
(`enrichTranscriptDiffs`), which are added in the API layer, not by the
`internal/document` builder.

The run-detail UI is the consumer: the `run_detail_doc.html` / `run_node.html`
templates and `run_view.js` render the tree, fold compact sub-runs, and fetch
row disclosures on expand. See `ui.md` for the UI surface.

## Cross-references

- `api.md` — the document HTTP routes in full.
- `ui.md` — the run-detail view that renders the document.
- `logging-and-state.md` — the `events.jsonl` / `state.json` sources the
  document is built from.

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code._
