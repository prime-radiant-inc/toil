# Webhooks (run-completion callbacks)

Toil can POST a JSON callback to a client-supplied URL when a run reaches a
terminal state. The payload builder and HTTP delivery live in
`internal/webhook`; registration and firing are wired through the API server
(`internal/api`), the engine (`internal/engine`), and the orchestrator
(`internal/orchestrator`). Delivery is best-effort: a single POST, no retries,
no signing, no auth.

## Registering a callback

Set `callback_url` on the `POST /runs` request body. It is an optional field on
`runRequest` (`internal/api/server.go`). The value is threaded through
`Manager.StartRun` (`internal/orchestrator`) into the engine, which records it on
the run as `RunState.CallbackURL` (`internal/engine/engine.go` sets
`runState.CallbackURL = callbackURL`; the field is defined in
`internal/state`). It is persisted to `state.json` and survives reload, so
resumed and cancelled runs still know where to call back.

Re-runs inherit the original callback: the dashboard's re-run path passes
`rerunState.CallbackURL` back into `StartRun` (`internal/dashboard/server.go`).

There is no separate "subscribe" endpoint and no per-run de-registration — the
callback is a property of the run, set once at creation.

See `api.md` for the full `POST /runs` request/response shape.

## When it fires

The webhook fires once, when the run reaches a terminal status: **completed**,
**failed**, or **cancelled**.

The trigger is the engine's `OnRunComplete` hook (`internal/engine/engine.go`),
which the engine calls synchronously at each terminal branch of the run loop in
`internal/engine/resume.go` — after status is finalized, totals are computed,
the terminal event (`run_completed` / `run_failed` / `run_cancelled`) is
appended, and any run summary is generated. The orchestrator installs the hook
via `Manager.WireWebhookCallback` (`internal/orchestrator/manager.go`), which
points `OnRunComplete` at `Manager.maybeFireWebhook`.

`maybeFireWebhook` is a no-op when `RunState.CallbackURL` is empty; otherwise it
calls `Manager.deliverWebhook`, which builds the payload and delivers it.

One terminal path does not go through `OnRunComplete`: cancelling a **paused**
run. `Manager.markCancelled` finalizes such a run itself (no engine loop is
running to fire the hook) and calls `maybeFireWebhook` directly when the prior
status was `paused` (`internal/orchestrator/manager.go`).

## Payload

`webhook.PayloadFromRunState` (`internal/webhook`) builds the `Payload` struct
from the terminal `RunState`; `webhook.Deliver` marshals it to JSON and POSTs
it. Fields (JSON names):

- `event` — `run_completed`, `run_failed`, or `run_cancelled`, derived from the
  run status.
- `run_id`, `workflow_id`, `status` — run identity and terminal status.
- `error` — `RunState.Error`, omitted when empty.
- `title`, `summary` — run title and generated summary, omitted when empty.
- `started_at`, `finished_at` — RFC 3339 timestamps; `finished_at` is empty when
  `RunState.FinishedAt` is nil.
- `nodes` — map of node ID to `{status, decision}` (`NodePayload`), one entry per
  node in the run.
- `has_unresolved_failure` — true when the run completed but carries unresolved
  failures.

The `Payload` also carries delivery fields, populated only when the relevant
delivery nodes ran: `repo_url` (from `RunState.Inputs["repo_url"]`), `branch`
(from the `push_to_remote` node's `branch` data), `merge_commit_sha` and
`pr_url` (from the `finalize_remote_branch` node's data), and `delivery_error`
(the error from a failed `finalize_remote_branch` or `push_to_remote` node).
These are read from completed/failed node state and omitted when absent.

### Unresolved-failure remapping

When a run's status is `completed` but `RunState.HasUnresolvedFailure` is true,
`PayloadFromRunState` rewrites the payload to failure semantics for external
consumers: `event` becomes `run_failed` and `status` becomes `failed`, while
`has_unresolved_failure` stays true. The persisted run status is unchanged — the
remap is payload-only.

## Delivery behavior

`webhook.Deliver` (`internal/webhook`) does exactly one HTTP POST:

- Content-Type `application/json`, body is the marshaled `Payload`.
- Client timeout is `deliverTimeout` (10 seconds).
- An empty callback URL, a transport error, or a non-2xx response status all
  return an error.

There is **no retry**, **no backoff**, **no payload signing**, and **no
authentication header** — the code POSTs the JSON body and inspects the status
code, nothing more.

Delivery errors are logged but not propagated: `Manager.deliverWebhook` logs
`toil.webhook.delivery_failed` (with run ID, callback URL, and error) on failure
and `toil.webhook.delivered` on success (`internal/orchestrator/manager.go`). A
failed callback never affects the run's recorded outcome. See `logging-and-state.md`
for the logging surface.

## Querying by callback URL

`GET /runs` accepts a `callback_url` query parameter that filters runs by
**prefix match** against the stored `RunState.CallbackURL`
(`internal/api/server.go` uses `strings.HasPrefix`). Supplying it (or any other
filter) switches the endpoint to the enriched-summary response, where each
summary echoes the run's `callback_url`. See `api.md` for the `GET /runs`
filter set and response shapes.

## Cross-references

- `api.md` — `POST /runs` request body, `GET /runs` filters and summaries.
- `server.md` — server architecture and route mounting.
- `logging-and-state.md` — run state persistence and the structured-log surface.

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code._
