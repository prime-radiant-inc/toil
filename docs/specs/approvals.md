# Approvals & Human Gates

A gate lets a node pause a run until a decision is recorded, then route on that
decision. Gate semantics live in `internal/engine` (`approvals.go`); the durable
approval records and their list/load/resolve helpers live in
`internal/approvals`. An approval is a file under `runs/<id>/approvals/`; nothing
is held in a database.

## Gates

Every node carries an optional `gate` field (`definitions.Node.Gate`, schema in
`schemas.md`). Only one value changes execution: `required`. In
`engine.processApprovals`, a node whose `Gate != "required"` is added to the
runnable set and dispatched normally; a node with `gate: required` is diverted
into the approval path. Any other value — `none`, `optional`, or empty —
therefore runs without pausing.

A `gate: required` node behaves like any other node otherwise: it has a prompt,
declares `decisions`, and routes through outgoing edges on the chosen decision.
The difference is that the decision comes from a recorded approval rather than a
runner.

## The approval record

An approval is `approvals.Approval` (`internal/approvals`, `approvals.go`),
serialized to `runs/<id>/approvals/<approval_id>.json` — the path is built by
`approvals.ApprovalPath`. The `<approval_id>` is `<run_id>-<node_id>-<attempt>`
(`approvals.BuildID`), so each retried attempt of a gate gets its own record.

Fields include `id`, `run_id`, `node_id`, `attempt`, `status`, the `question`
shown to the approver, the `choices` (copied from the node's declared decision
IDs via `Decisions.IDs()`), an optional `timeout_sec`, and — once resolved — the
`decision`, `message`, `comment`, and `resolved_at`. `status` moves from
`pending` to `resolved` or `timed_out`.

Storage helpers in `internal/approvals`:
- `Create` / `Save` write the record (`Create` also makes the `approvals/`
  directory and stamps `created_at`).
- `Load` reads one record from a known run directory.
- `ListAll` walks every run under the runs dir and returns all approvals (this
  backs `GET /approvals`).
- `Find` locates a record by id across runs, returning it and its run directory.

## Lifecycle

When the engine reaches a `gate: required` node with no prior attempt, it
increments the node's attempt counter, sets `NodeState.Status` to
`awaiting_approval`, composes the question from the node prompt and inputs,
builds an `Approval` with `status: pending` and the node's decision IDs as
`choices`, persists it with `approvals.Create`, and emits `approval_requested`
(`engine.approvalOutput`). If no `Approver` resolves it immediately (the default
`FileApprover` does not), `processApprovals` returns `pending` and the run loop
saves state and stops with `ErrApprovalPending`. The run is paused, waiting for
an external resolution.

On a later pass over the same node, the engine reloads the pending approval and:
- if it is now `resolved`, converts it to the node's output
  (`approvalToOutput`: `decision` and `message` are both required), marks the
  node `completed`, emits `approval_resolved` and `node_completed`, and routes
  on the decision (`applyApprovalOutput`);
- if its `timeout_sec` has elapsed, auto-resolves it as timed out (see below);
- otherwise it stays pending and the run keeps waiting.

### Resolving

Resolution flows through `approvals.Resolve` (`internal/approvals`,
`resolve.go`), which takes a `ResolveInput{Decision, Message, Comment}`, finds
the record, sets `status: resolved` with `resolved_at`, saves it, and appends an
`approval_resolved` event. Both decision and message are load-bearing: the
engine rejects a resolved approval that lacks either when turning it into node
output (`approvalToOutput`).

The decision should be one the node declares. The node's decision IDs are
published on the approval as `choices`, and the dashboard offers exactly those
as `DecisionOptions`. Routing then matches the chosen decision against the
node's outgoing edges, so a decision the node does not declare has no edge to
fire.

The server resumes the paused run on resolution. Both the API handler
(`api.Server.handleApprovalResolve`) and the dashboard handler call
`Manager.NotifyApproval(ctx, approval.RunID)` (`internal/orchestrator`,
`manager.go`) after `approvals.Resolve` succeeds, which signals the run worker to
continue. On its next pass the run loop reloads the now-`resolved` approval and
routes on its decision.

`internal/approvals` also defines an `Approver` interface for in-process
resolution (`approver.go`). The default `FileApprover` returns nil (leaving the
approval pending for external resolution); `AutoApprover` picks the record's
`default`, else its first choice, else `approved` (used by eval / CI runs);
`CallbackApprover` delegates to a supplied function. When set on the engine, an
`Approver` is consulted at request time and on each subsequent pass
(`engine.tryResolveApproval`), guarded by a re-read of the on-disk record so an
external resolution (e.g. via the dashboard) wins over a racing auto-resolve.

### Timeout auto-resolution

If a node sets `timeout_sec` alongside `gate: required`, a pending approval that
has been open at least that long auto-resolves instead of waiting forever
(`engine.checkApprovalTimeout`). The check re-reads the record from disk first;
if it was resolved externally in the meantime, the timeout is abandoned.
Otherwise the record is saved with `status: timed_out` and a `"timed out after
Ns"` comment, the node is marked `completed`, and an `approval_timed_out` event
is emitted (`markApprovalNodeTimedOut`).

A timeout does not produce a normal decision. Instead the run loop synthesizes
the `_timeout` meta-decision (`engine.synthesizeMetaCompletion` with
`MetaDecisionTimeout`) and routes through an outgoing edge whose `when` is
`_timeout`. Because a timed-out gate would otherwise hang forever, a node that
combines `timeout_sec` with `gate: required` needs a `_timeout` routing edge for
the timeout to go anywhere; validation enforces this. See `schemas.md` for the
exact `_timeout` edge rule and the meta-decision rules.

## Surfaces

- **CLI** — `toil approvals list` and `toil approvals resolve`, both of which go
  through the HTTP API. See `cli.md` for flags.
- **HTTP** — `GET /approvals` lists every approval record across runs (`ListAll`
  applies no status filter); `POST /approvals/{id}/resolve` resolves one and
  resumes the run. See `api.md` for the routes.
- **UI** — the dashboard has an approvals inbox with decision/message/comment
  inputs and a pending-approvals banner. See `ui.md`.
- **Events** — `approval_requested`, `approval_resolved`, and
  `approval_timed_out`, plus the `awaiting_approval` node status and the
  `paused` run status. See `logging-and-state.md`.

## Cross-references

- `schemas.md` — the `gate` field, `timeout_sec`, and `_timeout` edge rules.
- `logging-and-state.md` — approval events, run/node statuses, and on-disk
  layout (`approvals/<approval_id>.json`).
- `cli.md` — the `approvals` CLI subcommands.
- `api.md` — the approvals HTTP routes.
- `ui.md` — the approvals inbox.

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code._
