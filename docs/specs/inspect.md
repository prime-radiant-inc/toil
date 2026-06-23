# Inspect

Inspect is the run/node introspection system in `internal/inspect`. It answers
"what happened in this run?" from a chosen angle — timing, tokens, decisions,
errors, the LLM transcript, and so on — by replaying a run's event log through a
small, focused **processor** for that angle. Each angle is an **aspect**. The
same processors back both the `toil inspect` CLI command and the
`GET /runs/{id}/inspect/...` HTTP routes.

## Concepts

### Aspect and Processor

An aspect is a named view over a run. Every aspect is implemented as a
`Processor` (`internal/inspect:Processor`):

```go
type Processor interface {
    ProcessEvent(event state.Event)
    Changed() bool
    Result() any
}
```

A processor is fed the run's events one at a time via `ProcessEvent`, then asked
for its `Result()` (a JSON-serializable struct). `Changed()` reports whether the
last event moved the result — follow mode uses it to decide when to re-emit.
Some processors don't replay events at all and instead read directly from
`state.RunState` (`timing`, `inputs`, `outputs`, `review_overrides`, `tree`);
their `ProcessEvent` is a no-op and `Changed()` returns false.

### Registry

Aspects self-register at package init via `Register(name, factory)` into a
package-level map (`internal/inspect/registry.go`). `NewProcessor(name, rs)`
looks up the factory and constructs a processor bound to a `state.RunState`;
unknown names return an `unknown aspect` error. `Aspects()` returns the
registered names.

### Parsing runner output

Most aspects derive their data from `node_output` events, whose `Text` carries a
runner's structured NDJSON line. `ParseRunnerEvent` (`internal/inspect/events.go`)
decodes one such line into an `InnerEvent`, recognizing kinds like
`ROUND_TIMINGS`, `SESSION_START`, `TOOL_CALL_START` (including the special
`communicate` tool, parsed into a decision/message/data via `parseCommunicate`),
`TOOL_CALL_OUTPUT_DELTA` (schema-validation errors), `ASSISTANT_TEXT_END` (token
usage), and `STEERING_INJECTED`. `ChildRun(node)` extracts a `child_run` ID from
a node's untyped `Data` map, and `DetectAttemptBoundaries(events, nodeID)`
returns the event indices where each attempt of a node begins (one per
`node_started`).

## Aspects

These are the aspects registered in the package (each registers itself in its own
file under `internal/inspect`):

| Aspect | What it shows |
| --- | --- |
| `overview` | Run-level summary: run ID, workflow, status, duration, models used, total tokens, and a per-node summary (status, attempts, dispatches, decision, duration, child run), sorted by start time. (`overview.go:OverviewResult`) |
| `flow` | Ordered timeline of execution events (started, completed, failed, failed_handled, skipped, edge, decision, steering) plus computed annotations: detected loops, per-node steering counts, and concurrent node groups. (`flow.go:FlowResult`) |
| `timing` | Per-node durations with percent-of-total and concurrency overlap, plus a `bottlenecks` list (the longest-running node). Computed from `RunState`, not events. (`timing.go:TimingResult`) |
| `tokens` | Per-node and run-total token counts (input, output, cache read/miss, reasoning), cache-hit rate, and estimated cost; priced per-node by each node's model via `internal/metrics`. Lists models seen. (`tokens.go:TokensResult`) |
| `decisions` | Every `communicate` decision in the run: node, attempt number, decision, message, and decision data. (`decisions.go:DecisionsResult`) |
| `errors` | Detected error events — `schema_validation`, `steering`, and `silent_exit` (a `node_failed` with an exit code and no text) — each with node, attempt, message, and timestamp. (`errors.go:ErrorsResult`) |
| `prompts` | Per-node prompts: the full prompt, the edge prompt, and the computed system portion (full minus edge). (`prompts.go:PromptsResult`) |
| `inputs` | The run's top-level input map, read straight from `RunState.Inputs`. (`inputs.go:InputsResult`) |
| `outputs` | Per-node output: message, data, and artifacts, read from `RunState` nodes. (`outputs.go:OutputsResult`) |
| `transcript` | Per-node, per-attempt LLM transcript: session ID, model, and rounds, each round listing tool calls (name, an args preview capped at 200 chars, and args size) plus the round's duration. The attempt's final decision and message are stored per-attempt, not per-round. (`transcript.go:TranscriptResult`) |
| `tree` | The run hierarchy: this run plus child runs discovered via `child_run` links, recursively, with cycle protection. Requires a `RunLoader`. (`tree.go:TreeResult`) |
| `compare` | Side-by-side delta of two runs: duration, total attempts, and token total/cost, each as A, B, delta, and percent change. Requires a `RunLoader` and the other run's ID. (`compare.go:CompareResult`) |
| `review_overrides` | Nodes whose decision carried the `override` tag — review-escalation waivers — derived from `RunState.NodesTagged(OverrideTag)` at read time. (`review_overrides.go:ReviewOverridesResult`) |

`review_overrides` is registered and reachable through the API, but it is not in
the CLI's recognized-aspect list (see below), so the CLI does not treat it as an
aspect positional.

### Run-level vs. node-level

Every aspect runs at the **run level** by default — the processor sees the whole
run. The HTTP layer additionally supports **node-level** inspection: when a node
is named, the API filters events to that node and narrows the `RunState` to just
that node (`state.RunState.NarrowToNode`) before constructing the processor, so
aspects that iterate the Nodes map (`outputs`, `timing`, `tokens`, …) return only
that node's data instead of the whole run's. `tree` and `compare` are the
cross-run aspects; they require a `RunLoader` (set by the server via
`SetLoader`) to read other runs' state and events, and `compare` additionally
needs the other run's ID via `SetOtherRunID`.

### Attempts

A node can run more than once (retries). `DetectAttemptBoundaries` splits a
node's event stream at each `node_started`. The API's `?attempt=N` (1-based)
restricts the events fed to the processor to a single attempt's window; out-of-
range values 404. Several processors also surface attempt numbers in their
results (`decisions`, `errors`, `transcript`).

## Surfaces

### CLI

The `toil inspect` command signature and its flags are documented in `cli.md`
(`cmd/toil/main.go:runInspect`). The command resolves positionals by consulting
`isKnownAspect` (`cmd/toil/main.go`): the first non-flag positional after the run
ID is treated as the aspect if it names a known aspect, otherwise as a node ID.
The special form `toil inspect <run-id> compare <other-run-id>` consumes the next
positional as the comparison run and sends it as the `compare/<other-run-id>`
aspect path. The CLI is an HTTP client (`internal/client`: `Inspect`,
`InspectNodeAttempt`, `InspectFollow`, `InspectNodeFollow`) — it talks to a
running server.

### HTTP API

The run-level and node-level inspect routes, their `?attempt=N` / `?follow=true`
query params, and the `compare/{other-run-id}` aspect-path form are documented in
`api.md`. All are handled by `internal/api:Server.handleInspect`.

### Follow mode (live)

With `?follow=true`, `handleInspect` switches to a Server-Sent Events stream
(`text/event-stream`). It emits the current `Result()` immediately, returns at
once if the run is already terminal, and otherwise tails `events.jsonl` from the
exact byte offset of the initial read (`state.ReadEventsWithOffset` /
`state.TailEvents`, to avoid racing writers). Each new event is processed, and a
fresh result is emitted only when `Changed()` is true. The stream closes when a
`run_completed` or `run_failed` event arrives. Node-scoped follow filters tailed
events to the target node.

## Cross-references

- `cli.md` — the `inspect` command and its flags.
- `api.md` — the inspect HTTP routes.
- `logging-and-state.md` — the `events.jsonl` log and `state.json` snapshots
  these processors read.
- `internal/metrics` — token and cost accounting used by `tokens` and `compare`.

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code._
