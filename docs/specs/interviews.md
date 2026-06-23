# Interviews and Interrogation

Two distinct features for extracting information out of a run after a node
ran into trouble.

- **Interview mode** is an *automatic, post-run* mechanism. When a run finishes
  (or fails), the engine inspects the workflow's `interview` trigger mode and, if
  the run had trouble, records a set of candidate nodes and spawns a follow-up
  workflow to interview them. Lives in `internal/interviews` (records and indices)
  and `internal/engine/interview_trigger.go` / `interview_records.go` (the
  trigger logic).
- **Interrogation** is a *manual, live* diagnostic Q&A. An operator forks a
  completed node's agent session and asks it questions, leaving the original
  session untouched. Serf runners only. Lives in `internal/interrogate`.

They share a goal — understanding why a node behaved as it did — but are
otherwise unrelated: interview mode is engine-driven and persists records to
disk; interrogation is request-driven, in-memory, and ephemeral.

---

## Interview mode

### Trigger

A workflow opts in via its top-level `interview` field (`Workflow.Interview` in
`internal/definitions/types.go`). `Workflow.InterviewMode()` normalizes it to one
of three constants, defaulting to `never` when unset:

- `InterviewNever` (`never`) — no interviews.
- `InterviewOnFailure` (`on_failure`) — interview only when the run actually
  failed at a node.
- `InterviewOnIssue` (`on_issue`) — interview when any node failed *or* had to
  retry (more than one attempt).

The `interview` schema field is documented in `schemas.md`.

### Lifecycle

After a run reaches a terminal state, the engine calls
`Engine.maybeEmitInterviewCandidates` (`internal/engine/interview_trigger.go`,
invoked from `internal/engine/resume.go`). The flow:

1. **Mode gate.** Returns immediately if `InterviewMode()` is `never`.
2. **Subworkflow guard.** `failureCausedBySubworkflow` suppresses parent-run
   interviews when the failure propagated up entirely from a child subworkflow —
   the child run interviews at the level where the failure actually occurred.
3. **Candidate collection.** `collectInterviewableNodes` walks the workflow's
   nodes and keeps only those backed by an agent session (a non-empty
   `NodeState.SessionID`), including ForEach-expanded role items whose state IDs
   use the `<templateID>::<index>` form. Each becomes an `InterviewableNode`
   (`NodeID`, `RoleID`, `SessionID`, `Outcome`, `Attempts`). Candidates are
   sorted by `outcomePriority`: failed before retried before succeeded.
4. **Mode-specific suppression.** For `on_failure`, the run must have failed or
   carry `HasUnresolvedFailure`, and purely structural routing failures (no
   direct node failure, per `hasDirectFailedNode`) are skipped. For `on_issue`,
   the trigger also fires when `hasRetriedNodes` reports a retried candidate.
5. **Record + emit.** `createPendingInterviews`
   (`internal/engine/interview_records.go`) writes one pending interview record
   per candidate, and the engine appends an `interview_candidates` event to the
   run log (see `logging-and-state.md`).
6. **Callback.** If `Engine.OnInterviewCandidates` is set, it is invoked
   fire-and-forget. The orchestrator wires this in `Manager.WireInterviewTrigger`
   (`internal/orchestrator/manager.go`), whose handler calls
   `Manager.StartLearnRun` to launch the `learn` workflow with the candidate
   nodes and a text run-context summary as inputs.

When the spawned interview subworkflow (child workflow ID `interview`) completes,
`Engine.maybeRecordInterviewResult` updates the pending record to `completed`,
stamps the interview session ID, and stores any `learnings` from the child
output. On error, `Engine.maybeRecordInterviewFailure` marks the existing record
`failed`. Both live in `internal/engine/interview_records.go`.

### Records

An `Interview` (`internal/interviews/interviews.go`) captures the original node's
context — `RunID`, `NodeID`, `RoleID`, `WorkflowID`, `OriginalSessionID`,
`OriginalOutcome`, `OriginalAttempts` — plus `Status`, the resulting
`InterviewSessionID`, timestamps, `Responses`, and any `Error`. Status values:
`pending`, `in_progress`, `completed`, `failed`, `skipped`, `degraded`.

Records are JSON files at `runs/<id>/interviews/<nodeID>.json` (`InterviewPath`),
one per interviewed node, with IDs of the form `interview-<runID>-<nodeID>`
(`BuildID`). `Create`, `Save`, `Load`, and `ListForRun` manage them. The
`interviews/` directory is noted in `logging-and-state.md`.

`internal/interviews/index.go` additionally maintains cross-run JSONL indices
under `<indexDir>/interviews/by_role/<roleID>.jsonl` and
`.../by_workflow/<workflowID>.jsonl`. Each `IndexEntry` is a lightweight
reference (run/interview/node IDs, role, workflow, outcome, summary, timestamp).
Reads skip corrupt lines rather than failing.

### API

Served by `internal/api/server.go`:

- `GET /runs/{id}/interviews` — list a run's interview records
  (`handleRunInterviews` → `interviews.ListForRun`).
- `GET /runs/{id}/interviews/{nodeId}` — one record (`handleRunInterview` →
  `interviews.Load`); 404 when no record exists for that node.

Both are listed in `api.md`.

---

## Interrogation

Interrogation forks the agent session that backed a completed node and runs a
live diagnostic conversation against the fork. The original session is never
mutated, so interrogating is safe to do against any run. **Serf runners only** —
the runner must support session fork.

### Manager and sessions

`interrogate.Manager` (`internal/interrogate/interrogate.go`) holds active
interrogations in memory, keyed by ID (`int-<hex>`). Each `Interrogation`
records the source `RunID`/`NodeID`, the `OrigSessionID`, the
`ForkedSessionID`, the workspace, the bound `runners.Runner`, and activity
timestamps. A per-interrogation mutex serializes asks.

**Create** (`Manager.Create`): looks up the target node's `SessionID` from run
state (errors if the node has no session), then calls the runner with
`Resume: true, Fork: true` and the operator's question as the prompt. The runner
must return a *distinct* forked session ID; if it is empty or equal to the
original, `Create` refuses, because resuming the original session on later asks
would mutate the diagnostic target. The forked session ID is stored and the
first answer returned.

**Ask** (`Manager.Ask`): resumes the *forked* session (`Resume: true`, no
re-fork) with a follow-up question and returns the answer, refreshing
`LastActive`.

**Expiry**: interrogations expire after `ExpiryDuration` (30 minutes) of
inactivity. `Manager.StartExpiry` runs a background ticker (every 5 minutes)
calling `Manager.Sweep`, which deletes idle sessions.

### Workspace resolution

The HTTP layer (`internal/api/server.go`) resolves the fork's working directory
via `resolveInterrogationWorkspace`. Project-mode nodes reuse their declared
workspace path (resolved through the expression resolver), giving the agent file
access to the code it was reasoning about. Non-project nodes (modes `none` /
`shared`, e.g. the learn workflow) fall back to the run's directory under the
runs dir — serf only needs a CWD for diagnostic chat. If neither is available
the request fails with a 400 explaining how to fix it.

### API

Served by `internal/api/server.go`; also listed in `api.md`:

- `POST /interrogations` — body `{run_id, node_id, question}`. Loads run state
  and the workflow snapshot, resolves the node (falling back to the ForEach
  template for `<id>::<index>` IDs), rejects non-serf runners with a 400, then
  creates the interrogation and returns the first answer
  (`handleInterrogationCreate`).
- `POST /interrogations/{id}/ask` — body `{question}`. Follow-up against an
  existing interrogation (`handleInterrogationAsk`).
- `GET /interrogations` — list active interrogations
  (`handleInterrogationList`).

Both create and ask use a 120-second per-request timeout.

### CLI

`toil interrogate <run-id> <node-id> <question> [--once]`
(`runInterrogate` in `cmd/toil/main.go`; documented in `cli.md`). It POSTs to
`/interrogations`, prints the first answer, then — unless `--once` — drops into
an interactive `>` follow-up loop that POSTs each line to
`/interrogations/{id}/ask` until empty input, `exit`, or `quit`.

---

## Cross-references

- `schemas.md` — the workflow `interview` field.
- `logging-and-state.md` — the `interview_candidates` event and the
  `runs/<id>/interviews/` directory.
- `cli.md` — the `interrogate` command.
- `api.md` — the interview and interrogation HTTP routes.
- `runners.md` — serf runner session fork/resume.

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code._
