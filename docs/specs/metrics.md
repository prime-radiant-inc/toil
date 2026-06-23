# Metrics & Cost Accounting

Toil derives duration, token, and cost metrics for every run by replaying its
event log — there is no separate metrics store. The accounting logic lives in
`internal/metrics`; the HTTP surface lives in `internal/api`
(`metrics_handlers.go`). A metrics view is always computed on demand from
`runs/<id>/events.jsonl` and `runs/<id>/state.json`, never persisted as its own
artifact.

## What is measured

Three quantities, all rolled into `state.NodeTotals`
(`internal/state/totals.go`):

- **Duration** (`DurationMs`) — wall time a node spent executing, summed across
  attempts.
- **Tokens** (`Tokens`, a `state.TokenBreakdown`) — input, output, cache-read,
  cache-write (5-minute and 1-hour tiers), and reasoning counts.
- **Cost** (`CostUSD`, a `*float64`) — estimated USD, with
  `UnpricedEventCount` flagging events whose model had no pricing entry.

Tool-call counts are **not** part of metrics. Serf runners do tally tool calls
into the runner `Result` (`internal/runners`, `SerfRunner.Run`), but that count
does not flow into the collector — `state.NodeTotals` has no tool-call field.

### Token breakdown

`state.TokenBreakdown` fields:

| Field | Meaning |
| --- | --- |
| `Input` | New, uncached input tokens (billed at the input rate) |
| `Output` | Total output tokens — reasoning is already counted here on every supported provider |
| `CacheRead` | Cached input read back (cache-read rate) |
| `CacheWrite` | Input written to the 5-minute-TTL cache |
| `CacheWrite1h` | Input written to the 1-hour-TTL cache |
| `Reasoning` | Provider-reported reasoning tokens — metadata only |
| `ReasoningEstimated` | Char-based reasoning estimate for providers that don't report native counts — display only |
| `Total` | `Input + Output + CacheRead + CacheWrite + CacheWrite1h` |

`Reasoning`/`ReasoningEstimated` are **not** added into `Total`: reasoning is
already a subset of `Output`. This rule is enforced in
`metrics.Collector.consumeRunnerEvent` and in every rollup/total computation.

### Cost model

Cost is a per-event estimate, not a flat run-level multiplier.
`metrics.EstimateCost(model, TokenUsage)` looks the model up in serf's embedded
LiteLLM catalog via `llm.DefaultPrice` (`primeradiant.com/serf/llm`, returning
an `llm.Price`). It multiplies each token bucket by its own per-million rate:

- `UncachedInput` → input rate; `Output` (including reasoning) → output rate.
- `CacheRead` → the catalog's cache-read rate, falling back to the input rate
  when absent.
- `CacheCreation5m` → the cache-creation rate, falling back to input.
- `CacheCreation1h` → the 1-hour rate, falling back to the 5-minute rate, then
  to input. (Serf does not currently populate the 1-hour bucket.)

Each `node_output` usage event is priced at **its own** model's rate, then
accumulated, so a node that called several models is the sum of correctly-priced
events rather than one rate applied to combined tokens. Unknown models return
`(0, false)`; the node's `CostUSD` stays `nil` and `UnpricedEventCount`
increments — `CostUSD` is reported only once at least one event was priced.

`CostUSD` is a pointer so JSON distinguishes three states: `nil` (unknown
model, cost not recorded), `&0` (priced but zero tokens), and a non-zero value.

## How metrics are sourced

The data path runs from the runner up through the event log into the collector:

1. A serf/LLM runner streams provider output. Each line the runner emits is
   logged by the engine as a `node_output` event whose `text` carries the raw
   stream line (`internal/engine`, `execute.go`). See
   `logging-and-state.md` for event-log mechanics.
2. Serf emits a kind-keyed `ASSISTANT_TEXT_END` envelope on the stream
   carrying a `usage` payload (input/output/cache/reasoning token counts) and
   the model name.
3. `metrics.Collector.ProcessEvent` dispatches on event type.
   `consumeRunnerEvent` parses the `node_output` text as JSON, and only when
   `kind == "ASSISTANT_TEXT_END"` does it add the usage counts to the node's
   accumulator and price the event.
4. Duration comes from event *timestamps*, not usage: `node_started` opens an
   attempt, `node_completed`/`node_failed` closes it and adds the elapsed
   milliseconds. `node_skipped` with a cancellation reason closes an in-flight
   attempt so duration stops ticking. A still-open attempt contributes
   `time.Since(lastStarted)` when totals are read live.

Because everything is derived from events, a metrics view is reproducible by
replaying `events.jsonl` at any time. A terminal run's `state.json` snapshot may
also carry the run-total as `RunState.Totals` (a `*state.NodeTotals`,
`internal/state/state.go`) so readers don't have to replay — see
`logging-and-state.md`.

## Aggregation levels

`metrics.Collector` (safe for concurrent use) accumulates per-node state and
exposes three views:

- **Per-node own** — a single node's own duration, tokens, and cost.
  `Collector.NodeMetrics(id)` returns this as `own`.
- **Per-node rollup** — a node plus all its descendants.
  `Collector.NodeMetrics(id)` returns this as `rollup`. Parent/child links come
  from ForEach iteration namespacing (`foo::N` rolls up under `foo`,
  auto-registered by `linkForEachIteration`) and from explicit
  `Collector.SetParent`. Rollup tokens/cost are summed; rollup duration is wall
  time (`maxEnd - minStart`) across descendants, not a sum.
- **Run total** — the sum across all *leaf* nodes (any node that is not itself a
  parent), via `Collector.RunTotal`. Counting only leaves avoids
  double-counting a ForEach parent against its iterations. Run-total duration is
  also wall time across leaves.

Above the single run, the **execution group** spans the whole parent/child run
tree. `handleExecutionGroupMetrics` collects the group's run ids
(`collectGroupRunIDs` indexes runs by `ParentRun`, then walks down from the root
to all descendants), builds a collector per run, and sums each run's
total into `group_total`. Group tokens and cost are summed; `group_total`
duration takes the **max** across runs (`addInto`), reflecting the longest run
rather than serial wall time.

## Live metric stream

`Collector.Changes()` emits a batch of affected node IDs (the touched leaf plus
its ancestors) after each processed event, buffered size 64 with oldest-dropped
overflow. The API uses this to stream live updates.

The per-run and execution-group metrics routes both offer a `?follow=true` SSE
mode (see `api.md` for the route signatures). After an initial snapshot, the
handler tails new events, feeds them into the collector, and coalesces changed
nodes into one `metric-update` SSE event flushed roughly every **500 ms**
(`metrics_handlers.go`, `handleMetrics` / `streamExecutionGroupMetrics`). Each
`metric-update` payload carries the changed nodes' `own`/`rollup` totals plus
the current `run_total`; execution-group events carry the source `run_id` so a
client can qualify node IDs as `runID::nodeID`. The per-run stream closes on
`run_completed`/`run_failed` or client disconnect; the execution-group stream
currently closes only on client disconnect (its writer is not signaled when the
group's runs reach a terminal state).

The general run event stream (`GET /runs/{id}/events/stream`) also interleaves
coalesced `metric-update` events alongside raw run events.

### Number formatting

`internal/metrics/format.go` renders the human-facing strings used by the
dashboard (the CLI's `inspect <run> tokens` emits raw JSON numbers, not these):

- `FormatDuration` → `"34s"`, `"2m 18s"`, `"1h 04m"`.
- `FormatTokens` → `k`/`M` compaction above 1000 (e.g. `"12.4k"`).
- `FormatCost` → `"—"` for nil, `"$0.00"` for priced-zero, and magnitude-aware
  decimals otherwise (4 dp under `$0.001`, 3 dp under ~`$1`, else 2 dp).

## Surfaces

- **HTTP** — a per-node + run-total view and a group-aggregated view, each
  with an optional `metric-update` SSE follow mode. See `api.md` for the route
  signatures and response shapes.
- **Events** — metrics consume `node_output`, `node_started`,
  `node_completed`, `node_failed`, and `node_skipped` events. See
  `logging-and-state.md` for the event log and the full event-type set.

## Cross-references

- `api.md` — the metrics HTTP routes and the event/SSE streams.
- `logging-and-state.md` — the `events.jsonl` log and `state.json` snapshot
  that metrics are derived from.
- `runners.md` — the runners whose stream output carries token-usage
  payloads.

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code._
