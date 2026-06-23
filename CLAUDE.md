# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Dev Commands

```bash
make build          # Build binary to bin/toil
make test           # Run all tests (go test ./...)
make fmt            # Format all Go files
make serve          # Build + run server on :8080 (serf on PATH)
make dev            # Build + daemon + tail logs (serf on PATH)

# Run a single test
go test ./internal/engine/ -run TestName

# Run a single package's tests
go test ./internal/visualize/

# Validate all definitions
PROJECT_DIR=/path/to/project go run ./cmd/toil validate
```

### Server prerequisites

`make serve` handles both automatically:
- **serf on PATH**: Adds `../serf/` to PATH. Build serf first: `cd ../serf && make build`
- **LLM API keys**: Sources `../serf/.env` (contains OPENAI_API_KEY, etc.)

When restarting the server manually (not via `make serve`):
```bash
pkill -f "bin/toil serve"
set -a; source ../serf/.env; set +a
PATH="../serf:$PATH" bin/toil serve --addr :8080
```

## Architecture

**Toil** is a file-defined workflow orchestrator. Workflows are YAML, runners are YAML. Everything persists to disk — no database.

### Module: `primeradiant.com/toil`

### Package Map

- **`cmd/toil/main.go`** — Flag-based CLI (not Cobra). Single file, switch-case dispatch.
- **`internal/app`** — Bootstrap. `Load()` builds the full app (bundle + engine + registry).
- **`internal/definitions`** — Load and validate workflow/runner YAML from `definitions/`. Key types: `Workflow`, `Node`, `Edge`, `Runner`, `Bundle`, `Decision`, `DecisionList`.
- **`internal/engine`** — Core execution engine (~40 files, largest package). Handles the run loop, node execution, expression resolution (`input.x`, `node.foo.message`), ForEach expansion, retries, circuit breakers, approvals.
- **`internal/state`** — Thread-safe run state (`RunState`, `NodeState`). Append-only JSONL event log + JSON snapshots in `runs/<id>/`. Use `WithNode(id, fn)` to mutate, `WithNodes(fn)` to read.
- **`internal/runners`** — Runner registry with implementations: `codex`, `claude`, `serf`, `shell`, `human`. Each has a stream parser for real-time output.
- **`internal/orchestrator`** — Manages concurrent run workers with background goroutines, resume signaling, and cancellation.
- **`internal/api`** — HTTP API server. REST routes at root path (`/runs`, `/workflows`, `/approvals`, etc.).
- **`internal/dashboard`** — Web UI server mounted at `/ui/` (StripPrefix). Go templates + Alpine.js + HTMX + D3.js + ELK.js. Label sanitization helpers (`sanitizeGraphLabel`, `sanitizeGraphLabelLimit`) live here, not in `visualize`.
- **`internal/visualize`** — Server-side graph *data* builder, not a layout engine. `WorkflowTopology()`/`RunTopology()`/`CompoundWorkflowTopology()` (in `topology.go`) produce a `TopologyGraph` of nodes + edges with no coordinates; ELK.js computes layout client-side.
- **`internal/approvals`** — File-based approval records under `runs/<id>/approvals/`.
- **`internal/client`** — HTTP client for CLI→server communication. Uses `TOIL_URL` env var (default `http://127.0.0.1:8080`).
- **`internal/eval`** — Evaluation suite runner with auto-approval mode.

### Server Architecture

Two HTTP servers on different path prefixes:
- API server at root `/` (REST JSON endpoints)
- Dashboard server at `/ui/` (HTML templates, static assets)

Route mounting is in `runServe` (`cmd/toil/main.go`). JavaScript fetches use absolute URLs hitting the API server (e.g., `/runs/{id}/graph`).

### Frontend

Dashboard uses server-rendered Go templates enhanced with:
- **Alpine.js** — Reactive UI state
- **HTMX** — AJAX interactions
- **D3.js v7** — SVG graph rendering (positioned by client-side ELK.js)
- **ELK.js (elkjs@0.9.3)** — client-side graph layout
- **Tailwind CSS** — Styling

Custom JS logic is in `internal/dashboard/static/js/toil.js`.

Design tokens: accent `#1a6b5a`, ink `#1b2631`, muted `#6b7c8f`, surface `#f6f8fa`. Fonts: Fraunces (headings), DM Sans (body). Avoid Tailwind color names that collide (use `accent` not `teal`, `muted` not `slate`).

### Graph Visualization

Graph *data* is built server-side in Go (`internal/visualize/`), producing a `TopologyGraph` (nodes + edges, no coordinates). Layout is computed **client-side** by ELK.js (`elkjs@0.9.3`) in `internal/dashboard/static/js/elk-graph.js`; D3.js v7 renders the SVG from ELK's positions.

- **Topology builders** (in `topology.go`): `WorkflowTopology()`, `RunTopology()` / `RunTopologyWithMetrics()`, `CompoundWorkflowTopology()`, `RunTreeTopology()`, `CompoundExecutionGroupTopology()`
- **Compound nodes**: child nodes carry a `Parent` reference; ELK lays out the nested subgraphs
- **Escape edges**: `_loop_exhausted` meta-decision edges are flagged `IsEscape` on the topology edge
- **Decision tags**: `TopologyNode.Tags` carries the matched workflow-declared decision tags (see Key Patterns → Decision tags)
- Node ID namespacing for ForEach: `{nodeID}::{index}`
- Mobile: bottom sheet for node details, responsive tables-to-cards

### Key Patterns

- **State mutation**: `rs.WithNode(nodeID, func(n *state.NodeState) { n.Status = "x" })`
- **State reading**: `rs.WithNodes(func(nodes map[string]*state.NodeState) { ... })`
- **Expression resolution**: six namespaces — `${input.X}` (merged dispatch map; Phase 5 only — prompts and emit output), `${workflow_input.X}` (run-start inputs; all phases), `${node.X[.field]}` (completed node output), `${env.X}`, `${run.X}`, `${tree.X}`. The `!` suffix marks a reference as required (e.g. `${node.foo.message!}`). See `internal/engine/resolver.go`. Supported `node.<id>.<field>`: `decision`, `message`, `artifacts`, `data`, `session_id`, `tags`, `status`, `attempts`, `last_routing_decision`, `loop_iterations`. Anything else fails with an enumerated-supported-fields error at resolve time.
- **Decision tags**: Workflows annotate decisions with `tags: [...]` (e.g. `override` for review-escalation waivers). The engine materializes matched tags onto `NodeState.Tags` at completion time; downstream consumers (dashboard, inspect, visualize topology) key off them without the harness hardcoding tag semantics. Any workflow can declare its own tags.
- **Cross-run expressions**: `tree.tagged.<tag>` returns all nodes in the current run's execution group whose decision carries the tag. Walks from current run up to root via `ParentRun`, then all descendants (see `internal/engine/tree_resolver.go`). Optional `Tree` field on `RunContext` — nil means no tree access (unit tests).
- **Append-only logging**: All events go to `<runs-dir>/<id>/events.jsonl`, snapshots to `<runs-dir>/<id>/state.json`. `<runs-dir>` defaults to `$XDG_DATA_HOME/toil/runs` (or `~/.local/share/toil/runs`); override with `TOIL_RUNS_DIR`. `node_completed` event data includes `tags` when the matched decision carried any — consumers filter on event.data.tags rather than distinct event types.

### Definition Files

- `definitions/runners/*.yaml` — Runner configs (codex, claude, serf, shell, human)
- `definitions/workflows/*.yaml` — Workflow specs with nodes, edges, inputs. Node prompts are inline. Shared patterns use sub-workflows (e.g., `verify_integration.yaml`). Decisions support descriptions: `{id: "pass", description: "..."}`.

### Dependencies

Direct deps: `gopkg.in/yaml.v3`, `github.com/yuin/goldmark`, `github.com/microcosm-cc/bluemonday`, `github.com/getkin/kin-openapi`, `github.com/santhosh-tekuri/jsonschema/v5`, and `primeradiant.com/serf`. No web framework — stdlib `net/http` only.

## Debugging Skills

Two skills in `.claude/skills/` for investigating toil run failures and monitoring live runs:

- **`debug-run`** — Post-mortem investigation. Tiered: Tier 1 (quick triage via API) escalates to Tier 2 (multi-agent deep dive with compound-graph, workflow cross-reference, systemic audit). Checks a living failure-patterns catalog before proposing fixes.
- **`watch-run`** — Live monitoring. SSE streaming with polling fallback, `no_progress_count` thresholds for stuck detection, actionable intervention suggestions (resume/retrigger/cancel/approve).

Shared resources in `.claude/skills/resources/`: `toil-api.md` (endpoint reference), `data-model.md` (run/node states, event types), `failure-patterns.md` (known failure catalog — grows over time).

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code (the `internal/engine` "~40 files" count reviewed and kept — 37 non-test / 104 with tests)._
