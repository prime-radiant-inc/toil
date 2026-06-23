# Server Architecture

Goal
- Make the server the control plane. The CLI should not be the parent of agent
  processes. The server owns run lifecycles and resumes after approvals.

Principles
- Server-first: the daemon runs workflows and owns process lifetimes.
- CLI is a thin client: it calls the server API and prints results.
- Append-only logs and snapshots remain on disk for auditability.

Components
1. `toil serve` (server)
   - Loads definitions without requiring env expansion.
   - Hosts the API for runs, approvals, workflows, and diagrams.
   - Hosts the UI at `/ui` with live run introspection and control plane actions.
   - Restores in-progress runs on startup and resumes them, unless
     `TOIL_DISABLE_RESTORE` is set.
   - `--addr` sets the bind address; `--daemon` runs the server in the
     background and logs to `<runs-dir>/server.log`. See `cli.md` for flag
     defaults and `file-layout.md` for how `<runs-dir>` is resolved.
2. Run manager
   - Starts runs asynchronously via `engine.CreateRun`.
   - Resumes runs when approvals are resolved.
   - Ensures only one worker per run.
3. CLI
   - Uses `TOIL_URL` (default `http://127.0.0.1:8080`).
   - Sends requests; does not execute workflow steps locally.

Lifecycle
- Client calls `POST /runs` to create a run (see `api.md` for the request shape).
- Server creates the run directory, writes `state.json`, `events.jsonl`, and
  `workflow.yaml`, then starts execution in a background worker.
- If approvals are required, the worker pauses.
- When approvals are resolved, the server resumes the run automatically.
- On restart, the server scans the runs directory for root runs (no parent)
  in `running` or `paused` status and resumes them.

Rationale
The CLI is a thin client and the daemon controls long-lived processes. That makes
the system usable as a software factory and unlocks web/UI automation.

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code (promoted to evergreen reference; Phase-3 verified)._
