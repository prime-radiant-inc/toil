# Web API

Base URL defaults to `http://127.0.0.1:8080`.

A machine-readable OpenAPI 3.0 spec for these endpoints is served at `GET /openapi.json` (built by `internal/api/openapi.go:BuildSpec`).

## Workflows

- `GET /workflows` — List all workflow IDs (JSON array of strings).
- `GET /workflows/{id}` — Get a single workflow definition as raw YAML (`Content-Type: text/plain`).
- `GET /workflows/{id}/graph` — Topology graph (nodes + edges, no coordinates) for a workflow definition.

## Runs

- `POST /runs` — Create and start a new run. Returns `{"run_id": "<id>"}`.
- `GET /runs` — List runs. Without filters, returns a JSON array of run ID strings. With any filter, returns `{"runs": [...]}` of enriched summaries sorted by `started_at` descending. Query params: `callback_url` (prefix match), `workflow`, `status`, `limit`.
- `GET /runs/{id}` — Full run state as JSON (status, node states, inputs, outputs, timing, error).
- `GET /runs/{id}/meta` — Lightweight run metadata plus a per-node `{status, decision, message?, error?, data?}` map. Includes each node's `message` and `data` when non-empty; omits artifacts, session, timing, and routing fields.
- `GET /runs/{id}/events` — All events for a run as JSONL (one JSON object per line; `Content-Type: application/json`).
- `GET /runs/{id}/events/stream` — Server-Sent Events stream (`text/event-stream`). Emits each `events.jsonl` event as a named SSE event, plus coalesced `metric-update` events. Auto-closes on `run_completed` or `run_failed`; a `run_cancelled` does not currently trigger close, so a cancelled run's stream stays open until the client disconnects.
- `GET /runs/{id}/graph` — Topology graph reflecting actual run state (node statuses, ForEach expansion, compound nodes).
- `GET /runs/{id}/compound-graph` — Compound graph spanning the entire run tree (child/nested runs).
- `GET /runs/{id}/document` — Document tree for the run's execution group (`{root: RunNode}`). Climbs to the execution-group root automatically.
- `GET /runs/{id}/document/row/{nodeId}` — Disclosure detail for a single node row: inputs, outputs, transcript, and prompt data. Optional `?attempt=N` query param (1-based) scopes to a single attempt.
- `GET /runs/{id}/session/{sid}` — Per-node-attempt sequence for an LLM session ID within a run. Returns `{session_id, parts: [{node, decision, message, ts}]}`.
- `GET /runs/{id}/metrics` — Per-node and run-total metrics (duration, tokens, cost). Add `?follow=true` to switch to an SSE `metric-update` stream coalesced at 500ms.
- `GET /runs/{id}/execution-group/metrics` — Metrics aggregated across the whole execution group rooted at this run.
- `GET /runs/{id}/inspect/{aspect}` — Inspect a run with a named aspect processor (default aspect `overview`). Query params: `?follow=true` (SSE follow mode); `compare/{other-run-id}` may be appended to the aspect for cross-run comparison.
- `GET /runs/{id}/nodes/{nodeId}/inspect/{aspect}` — Inspect a specific node within a run. Optional `?attempt=N` query param (1-based) scopes to a single attempt.
- `GET /runs/{id}/interviews` — List interview nodes for a run.
- `GET /runs/{id}/interviews/{nodeId}` — Get details for a specific interview node.
- `GET /runs/{id}/tools/{toolId}/raw` — Raw `tool_result` event data for a single tool call. (Reachable via the route switch; not currently in the OpenAPI spec.)
- `POST /runs/{id}/resume` — Resume a paused run. No request body. Returns `{"run_id": "<id>"}`.
- `POST /runs/{id}/cancel` — Cancel a running or paused run. No request body. Returns `{"status": "cancelled"}`. Returns 409 Conflict if the run is in a terminal state.
- `POST /runs/{id}/retrigger` — Retrigger a node in an existing run. Body `{"node_id": "<id>"}`. Returns 409 if the node is not terminal, 404 if not found.

## Approvals

- `GET /approvals` — List all approval records across all runs (any status).
- `POST /approvals/{id}/resolve` — Resolve a pending approval. The run resumes automatically.

## Interrogations

- `POST /interrogations` — Fork a runner session and ask a diagnostic question. Body `{run_id, node_id, question}`. Serf runners only.
- `GET /interrogations` — List active interrogation sessions.
- `POST /interrogations/{id}/ask` — Ask a follow-up question in an existing interrogation. Body `{question}`.

## Health

- `GET /health` — Health check. Returns `{status, uptime_seconds, active_runs, total_runs}`.

## OpenAPI

- `GET /openapi.json` — Machine-readable OpenAPI 3.0 spec describing the endpoints above.

## Run Request

```json
{
  "workflow_id": "brainstorm",
  "inputs": {
    "idea": "Build a CLI to-do list app",
    "context": "Use TDD"
  },
  "env": {
    "PROJECT_DIR": "/path/to/project"
  },
  "callback_url": "https://example.com/hook"
}
```

## Run Response

```json
{
  "run_id": "20260203-080215-352cedd46d3bf2c1"
}
```

## Cancel Response

```json
{
  "status": "cancelled"
}
```

Cancel returns 409 Conflict if the run is not in a cancellable state.

## Notes

- The server starts runs asynchronously and continues them after approvals.
- `inputs` keys must match the workflow's declared inputs; unknown or missing required inputs return 400.
- `env` is optional and is used to expand workflow workspace variables and is passed to runner processes.
- `callback_url` is optional; it is called when the run reaches a terminal state.
- `POST /runs` returns 409 Conflict (`{"error": "run_conflict", ...}`) when another non-terminal root run is already active for the same `project_dir` input.
- `POST /runs` returns 503 Service Unavailable with a `Retry-After` header when new-run creation is paused via the drain marker.
- `GET /runs/{id}/events/stream` is a Server-Sent Events stream that emits JSON lines from `events.jsonl` as they arrive.

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code (promoted to evergreen reference; Phase-3 verified)._
