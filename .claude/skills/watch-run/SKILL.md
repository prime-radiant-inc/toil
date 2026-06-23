---
name: watch-run
description: Use when monitoring an in-progress toil run, checking if a run is stuck, or observing live workflow execution
---

# Watch Run

Live monitoring of in-progress toil workflow runs.

## Entry Point

Resolve the run to monitor:
- **URL**: extract run ID from `/ui/runs/{id}` or `/runs/{id}` path
- **Run ID**: use directly
- **"Current run"**: `GET /runs` returns `[]string` of IDs only. Fetch `GET /runs/{id}` for recent IDs and check for `status: "running"`. Do NOT fetch all runs — work backwards from the end of the list and stop when you find active ones.

Base URL: `TOIL_URL` env var, default `http://localhost:8080`.

## Monitoring Procedure

1. `GET /runs/{id}` — confirm `status` is `running` or `paused`, review current node statuses
2. `GET /runs/{id}/events` — baseline timeline (**JSONL**, one JSON object per line, NOT a JSON array). Parse line-by-line. Establish what has happened so far.
3. **Monitor via SSE**: `GET /runs/{id}/events/stream` for real-time events (Server-Sent Events, 15s keepalive ping). Fall back to polling `GET /runs/{id}` every ~30s if SSE isn't practical in your environment.

**Focus on active runs first.** Do not scan the full run history. Check `GET /health` for a count of active runs, then find and monitor those.

## Alert Thresholds

Flag these conditions to the operator:

- **`no_progress_count` >3**: concerning — agent may be spinning. **>5: likely stuck.** Check the node's `message` and `error` fields for clues.
- **Node `running` >10 minutes** with no new `node_output` events: may be hung
- **Run `paused`** with unresolved approvals: check `GET /approvals` for pending items
- **Child run failed** while parent still active: check `GET /runs/{id}/compound-graph` for child run status

## Interventions

Suggest these to the operator — always confirm before executing:

- **Paused run** → `POST /runs/{id}/resume`
- **Failed node, possibly transient** → `POST /runs/{id}/retrigger` (body: `{"node_id": "..."}`)
- **Stuck run, no_progress_count >5, no recovery path** → `POST /runs/{id}/cancel`
- **Pending approval blocking progress** → `POST /approvals/{id}/resolve` (body: `{"decision": "...", "message": "..."}`)

## On Failure

If a monitored run transitions to `failed`, suggest switching to `debug-run` for post-mortem investigation.

## Resources

- `@resources/toil-api.md` — endpoint reference
- `@resources/data-model.md` — run/node states, event types
