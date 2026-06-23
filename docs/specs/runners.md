# Runner Contract

A runner executes a node's work and streams its output back to the engine.
Every runner implements a single Go interface (`internal/runners/runner.go`):

Runner interface
- Run(ctx, request, handler) -> (result, error)

The engine calls `Run` once per dispatch. There is no separate
start/resume/cancel/wait API and no persistent process handle:
- `request` (`runners.Request`) carries the prompt, workspace, session ID,
  resume flag, fork flag, env, max turns, and optional output schema.
- `handler` (`runners.LineHandler`) receives each output line as it streams.
- `result` (`runners.Result`) carries the final output, stderr, session ID,
  exit code, and tool-call count.

Each `Run` spawns a fresh child process (`exec.CommandContext`). Cancellation
and timeouts are driven by the context: when `ctx` is cancelled or a runner's
`timeout_sec` elapses, the engine kills the child's process group. Nothing is
detached or reattached between calls.

Logging
Runners stream stdout and stderr line by line. The engine captures every line
and stores it as `node_output` events (one per line, tagged with the `stream`
and `text`) and raw logs.

Resume
- Resume is session-based, expressed through the request, not a separate method.
- Each dispatch is a fresh process; the engine never keeps a process alive
  between dispatches.
- To resume, the engine sets `request.Resume = true` and supplies
  `request.SessionID`; the runner re-invokes its CLI with the session
  (e.g. `--resume <session_id>`). A resume with an empty session ID is an error.

Runner selection
The engine resolves which runner executes a node via the precedence rules in
`docs/specs/schemas.md` ("Runner selection precedence"): explicit `node.runner`
first, then the first matching `node.tags` entry against `workflow.runner_overrides`.
If neither resolves, dispatch fails with "node has no runner configured".

Runner types
- claude
- codex
- serf
- shell
- human

Runner configuration
Runners are defined in `definitions/runners/*.yaml`. The runner-definition
schema (`id`, `type`, `command`, `args`, `env`, `timeout_sec`, `resume`) is
owned by `docs/specs/schemas.md` ("Runner definition").

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code (promoted to evergreen reference; Phase-3 verified)._
