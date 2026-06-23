# Toil API Reference

Base URL: `TOIL_URL` env var (default `http://localhost:8080`).

## Runs

| Method | Endpoint | Response | Notes |
|--------|----------|----------|-------|
| GET | `/runs` | `[]string` | Run IDs only, not full objects |
| POST | `/runs` | `{run_id}` | Body: `{workflow_id, inputs, env?, callback_url?}` |
| GET | `/runs/{id}` | JSON object | Full run state (status, nodes map, inputs, timestamps) |
| GET | `/runs/{id}/events` | JSONL | One JSON object per line. NOT a JSON array. Parse line-by-line. |
| GET | `/runs/{id}/events/stream` | SSE | Server-Sent Events. 15s keepalive ping. |
| GET | `/runs/{id}/graph` | JSON object | Graph layout for this run using snapshotted workflow |
| GET | `/runs/{id}/meta` | JSON object | Lighter-weight: aggregated node status/decision/data |
| GET | `/runs/{id}/compound-graph` | JSON object | Parent + child run topology with node positions |
| GET | `/runs/{id}/interviews` | JSON array | Interview/learning data |
| GET | `/runs/{id}/interviews/{node_id}` | JSON object | Per-node interview data |
| POST | `/runs/{id}/resume` | `{run_id}` | Resume a paused run |
| POST | `/runs/{id}/retrigger` | `{run_id}` | Body: `{node_id}` — retrigger a failed node |
| POST | `/runs/{id}/cancel` | `{status}` | Cancel an active run |

## Workflows

| Method | Endpoint | Response | Notes |
|--------|----------|----------|-------|
| GET | `/workflows` | `[]string` | Workflow IDs |
| GET | `/workflows/{id}` | `text/plain` | Raw YAML. NOT JSON. |
| GET | `/workflows/{id}/graph` | JSON object | Workflow graph topology |

## Approvals

| Method | Endpoint | Response | Notes |
|--------|----------|----------|-------|
| GET | `/approvals` | JSON array | Pending and resolved approvals |
| POST | `/approvals/{id}/resolve` | JSON object | Body: `{decision, message, comment?}` |

## Health

| Method | Endpoint | Response | Notes |
|--------|----------|----------|-------|
| GET | `/health` | JSON object | Uptime, active/total run counts |

## Finding the failed node in state

To find why a run failed from `GET /runs/{id}`:
1. Check top-level `status` field (expect `"failed"`)
2. Scan `nodes` map for entries where `status == "failed"`
3. Read the failed node's `message` and `error` fields for error text (both may contain failure info depending on the code path)
4. Check `decision`, `attempts`, `no_progress_count` for context

## Finding the latest failed run

`GET /runs` returns only IDs. To find the latest failure:
1. Fetch the run list
2. For each recent ID, `GET /runs/{id}` and check `status`
3. Compare `started_at` timestamps to find most recent failure
