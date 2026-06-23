# Toil Data Model Reference

## Run Statuses
`running` | `paused` | `completed` | `failed` | `cancelled`

## Node Statuses
`pending` | `running` | `completed` | `failed` | `failed-handled` | `paused` | `skipped` | `retrying` | `awaiting_approval` | `cancelled`

- `failed-handled` — A ForEach expanded item whose runtime failure was absorbed by a template failure edge. Distinct from `failed` so resume can short-circuit absorbed failures and aggregate decisions (`all_succeeded`/`some_failed`/`all_failed`) see the correct status.

## State JSON Structure

Top-level keys from `GET /runs/{id}`:
- `id`, `workflow_id`, `status` — identity and current state
- `error` — error message if run failed
- `nodes` — map of node ID → node state object
- `inputs`, `env` — run configuration
- `started_at`, `finished_at` — ISO timestamps
- `title`, `summary`, `description` — generated text
- `parent_run` — parent run ID if this is a child subworkflow
- `callback_url` — URL to notify when run completes
- `join_state` — map of join node ID → list of arrived predecessor IDs

Node state keys (each entry in `nodes` map):
- `id`, `status` — identity and current state
- `error` — error message if node failed
- `started_at`, `ended_at` — ISO timestamps
- `message` — last output message or error text
- `decision` — the decision the node made (e.g., "done", "retry", "skip")
- `data` — structured output data (JSON object)
- `artifacts` — list of artifact file paths produced by the node
- `session_id` — serf/codex session ID (for LLM nodes)
- `attempts` — number of execution attempts
- `retry_count` — number of retries performed
- `no_progress_count` — cycles without forward progress (>3 concerning, >5 likely stuck)
- `last_dispatch_hash`, `last_output_hash` — change detection hashes

## Event Types

Events in `GET /runs/{id}/events` (JSONL format):

**Run lifecycle:** `run_started`, `run_completed`, `run_failed`, `run_paused`, `run_resumed`, `run_cancelled`
**Run metadata:** `run_intent_generated`, `run_summary_generated`
**Waves:** `wave_started`, `wave_completed`
**Nodes:** `node_started`, `node_output`, `node_completed`, `node_failed`, `node_prompt`
**Subworkflows:** `subworkflow_started`, `subworkflow_completed`
**Gates:** `gate_requested`, `gate_resolved`
**Compaction:** `compaction_created`

Event schema: `{timestamp, type, run_id, node_id?, text?, data?, stream?}`
- `stream`: `"stdout"` or `"stderr"` on `node_output` events

## Output Validation Contract

When a shell node declares `decisions` or `outputs` in its workflow YAML, its JSON output MUST include:
- `decision` — non-empty string
- `message` — non-empty string
- `data` — JSON object (not null, not missing)

Validation is SKIPPED for nodes that declare neither `decisions` nor `outputs`.
