# Logging and State

## Event log
Each run has an append-only JSONL log at `runs/<run_id>/events.jsonl`. Every event
is a single JSON object. The fields are `timestamp` (RFC3339 UTC), `type`, and
`run_id`; events about a specific node also carry `node_id`, and may carry
`stream`, `text`, `data`, and `duration_ms`. See the `Event` struct in
`internal/state/logger.go`.

The logger also mirrors each event to stdout as a single slog-compatible JSON
line (`msg: "toil.event"`). Stdout records larger than 8 KiB are emitted with
`text`/`data` stripped and `truncated: true` set. Configured secret values are
redacted to `[REDACTED]` in both the file and stdout output.

## Event types
The complete set of event types emitted by the engine:

Run lifecycle:
- `run_started`
- `run_paused`
- `run_resumed`
- `run_completed`
- `run_failed`
- `run_cancelled`

Node lifecycle:
- `node_started`
- `node_prompt`
- `node_inputs_resolved`
- `node_edge_prompt`
- `node_output`
- `node_completed`
- `node_failed`
- `node_failed_handled` — a ForEach expanded item's failure was absorbed by a template failure edge; carries the `failure_context` payload
- `node_failure_routed` — a node failure was routed along a failure edge rather than terminating the run
- `node_skipped` — a ForEach expanded item was skipped. Emitted when (a) a DAG-scheduled item's dependency failed, (b) the run was cancelled via ctx.Done(), or (c) a sibling item's genuine failure triggered dagCancel. The `reason` field identifies which case

Node attempts, retries, and retriggers:
- `node_attempt_started`
- `node_attempt_failed`
- `node_retry`
- `node_retriggered`
- `cascade_retrigger`
- `node_resume_degraded`
- `circuit_breaker_tripped`
- `loop_exhausted_failed`
- `failure_edge_fired`
- `dedup_dropped`

Approvals:
- `approval_requested`
- `approval_resolved`
- `approval_timed_out`

Goal gates:
- `goal_gate_satisfied`
- `goal_gate_unsatisfied`

Waves (DAG scheduling):
- `wave_started`
- `wave_completed`

Subworkflows:
- `subworkflow_started`
- `subworkflow_pending`
- `subworkflow_reentry`
- `subworkflow_completed`
- `subworkflow_failed`

Interviews:
- `interview_candidates`

## Node output logging
Every line a node emits on stdout and stderr is logged as a `node_output` event,
with the source stream recorded in the event's `stream` field. There are no
separate per-node `.log` files — `node_output` events in `events.jsonl` are the
only record of node output.

## Snapshots
`runs/<run_id>/state.json` is a JSON snapshot of the current `RunState` (see
`internal/state/state.go`). It is written via atomic temp-file-and-rename and is
re-saved throughout a run — on node status transitions, approval changes, and run
completion. The snapshot is the read path for the dashboard, API, and inspect
tooling; external tooling should read run state through the HTTP API rather than
these files directly (state.json is written mode 0600 and may contain sensitive
inputs, outputs, and error details).

## Run directory layout
Run data lives under `<runs-dir>/<run_id>/`:
- `events.jsonl` — append-only event log
- `state.json` — current run state snapshot (plus transient `state.json.tmp.*`
  files during atomic writes)
- `dispatches/<node_id>/<n>/inputs/` — per-dispatch resolved input files
- `approvals/<approval_id>.json` — approval records (when the run uses approvals)
- `interviews/` — interview records (when the run uses interviews)

`<runs-dir>` defaults to `$XDG_DATA_HOME/toil/runs`; see `file-layout.md`.

## Run statuses
- `running`: actively executing nodes
- `paused`: waiting for human approval
- `completed`: all terminal nodes finished successfully
- `failed`: a node or constraint failed
- `cancelled`: run was cancelled via API or CLI

## Node statuses
- `pending`: not yet started
- `running`: currently executing
- `completed`: finished successfully
- `failed-handled`: a ForEach expanded item whose runtime failure was absorbed by a template failure edge. Distinct from failed so resume can short-circuit absorbed failures and aggregate decisions see the correct status.
- `failed`: execution error
- `paused`: waiting for approval
- `skipped`: bypassed during execution
- `retrying`: retry in progress
- `cancelled`: terminated by run cancellation
- `awaiting_approval`: approval requested, waiting for resolution

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code (promoted to evergreen reference; Phase-3 verified)._
