# CLI

The `toil` binary is a flag-based CLI (stdlib `flag`, not Cobra) with switch-case
dispatch in `cmd/toil/main.go`. Most commands are thin clients to the Toil server,
connecting via `TOIL_URL` (default `http://127.0.0.1:8080`). A few commands operate
directly on local files (`validate`, `eval`, `runs tree`, `narratives preview`,
`pause`, `resume` with no args, `human`). `drain` is a hybrid: it calls the API to
list/cancel/poll in-flight runs and only writes the `.paused` marker locally.

On startup the CLI loads environment variables from a `.env` file: it reads
`TOIL_ENV_FILE` if set, otherwise best-effort `.env` in the current directory.
Existing environment variables are never overwritten.

## Commands

| Command | Description |
| --- | --- |
| `toil help` (`-h`, `--help`) | Print usage. |
| `toil version` (`-v`, `--version`) | Print version (`toil dev`). |
| `toil validate` | Validate all workflow/runner definitions on disk. Local. |
| `toil workflows list` | List workflow IDs (from the server). |
| `toil workflows show <id>` | Print a workflow definition (from the server). |
| `toil run <workflow_id> --input key=value` | Create a run; prints the run ID. Flags may appear before, after, or interleaved with the workflow ID. |
| `toil resume <run_id>` | Resume a specific paused/waiting run via the API. |
| `toil resume` (no args) | Remove the daemon `.paused` marker, re-enabling new run creation. |
| `toil cancel <run_id>` | Cancel a run. |
| `toil runs list [--workflow <id>] [--status <status>] [--limit <n>]` | List runs; with no filters prints run IDs, with any filter prints a table. |
| `toil runs show <run_id>` | Print run state JSON. |
| `toil runs events <run_id>` | Print the run event log. |
| `toil runs tree <root_run_id>` | Render the run family rooted at `<root_run_id>` as an indented tree, walking `parent_run` pointers on disk. Local (bypasses the API). |
| `toil narratives preview <run_id> [flags]` | Generate run intent/summary narratives from on-disk run artifacts. Local. |
| `toil approvals list` | List pending approvals. |
| `toil approvals resolve <approval_id> --decision <decision> --message <message> [--comment <comment>]` | Resolve an approval. |
| `toil visualize workflow <id>` | Print a workflow graph (JSON). |
| `toil visualize run <run_id>` | Print a run graph (JSON). |
| `toil inspect <run-id> [<node-id>] [<aspect>] [--attempt <n>] [--follow]` | Inspect a run or a specific node, optionally a specific aspect/attempt, optionally streaming with `--follow`. |
| `toil interrogate <run-id> <node-id> <question> [--once]` | Ask an LLM-backed question about a node; interactive follow-up loop unless `--once`. |
| `toil serve [--addr :8080] [--daemon]` | Run the server. With `--daemon`, fork into the background and print the PID and log path. |
| `toil eval <id> [--project-dir /path]` | Run an eval spec (`tests/eval/<id>.yaml`) without human intervention. Local. |
| `toil human` | Runner shim: reads a runner `Request` JSON from stdin, prompts on the TTY, and writes the JSON response to stdout. Invoked by the engine, not normally by hand. |
| `toil pause` | Create the daemon `.paused` marker, causing the server to reject new run creation (HTTP 503). Local. |
| `toil drain [--dry-run] [--force-cancel] [--wait]` | Pause new run creation, then list/cancel/wait on in-flight runs. Interactive prompt unless a mode flag is given. |

Any unrecognized command prints usage and exits non-zero.

## Per-command flags

- **`run`**: `--input key=value` (repeatable). Inputs are validated against the
  workflow definition before the run is created. If `PROJECT_DIR` is set in the
  environment, it is forwarded to the run's env.
- **`runs list`**: `--workflow <id>`, `--status <status>`, `--limit <n>`. With no
  filters the command prints bare run IDs; with any filter it prints a table
  (run ID, workflow, effective status, started-at, duration).
- **`approvals resolve`**: `--decision` and `--message` are required; `--comment`
  is optional. The approval ID is positional.
- **`narratives preview`**: `--runs-dir <dir>` (defaults to `TOIL_RUNS_DIR`),
  `--run-id <id>` (or positional), `--intent`, `--summary` (defaults to both when
  neither is given), `--prompt-only` (no LLM call), `--include-prompt`,
  `--pretty`, `--timeout <dur>` (e.g. `30s`; defaults to
  `TOIL_RUN_NARRATIVE_TIMEOUT` or 30s).
- **`inspect`**: positional `<run-id>`, optional `<node-id>` and `<aspect>`;
  `--attempt <n>` (requires a node ID), `--follow` to stream. See
  `docs/specs/inspect.md` for the aspect catalog.
- **`interrogate`**: `--once` returns the first answer and exits instead of
  entering the interactive follow-up loop.
- **`serve`**: `--addr` (default `:8080`), `--daemon`.
- **`eval`**: `--project-dir <path>` overrides the spec's `project_dir`. See the
  "Eval command" section below for project-directory resolution.
- **`drain`**: `--dry-run` (list in-flight runs and exit without pausing),
  `--force-cancel` (pause + cancel all in-flight runs non-interactively),
  `--wait` (pause + wait for natural completion).

## Notes

- `run` and `resume <run_id>` send requests to the server and return/echo the
  run ID. The server continues runs after approvals are resolved.
- `cancel` cancels a run; in-flight means running or paused.
- Definitions are loaded on the server; the CLI does not need local definition
  files for server-backed commands. `validate` and `eval` read definitions from
  the repo on disk.
- `pause`/`resume` (no-arg) and `drain` operate on the `.paused` marker file in
  the runs directory; the daemon checks for it on each create-run request.

## Eval command

`toil eval <id>` runs a workflow scenario without human intervention. It loads
the eval spec from `tests/eval/<id>.yaml`, prepares the environment, runs the
workflow, and verifies the result. The result JSON is printed to stdout (and
saved under the run directory by the eval runner). See
`docs/specs/eval-suite.md` for the full spec schema and examples.

Project directory resolution:
- `--project-dir <path>` overrides the spec's `project_dir` field.
- Otherwise the spec's `project_dir` is used.
- If neither is set, a fresh temp directory is created per invocation to keep
  concurrent/sequential runs from contaminating each other.

## Environment variables

| Variable | Purpose |
| --- | --- |
| `TOIL_URL` | Server base URL for client commands. Default `http://127.0.0.1:8080`. |
| `TOIL_RUNS_DIR` | Override the runs directory. See `docs/specs/file-layout.md` for the full resolution rule. |
| `TOIL_RUN_NARRATIVE_TIMEOUT` | Default timeout for `narratives preview` (e.g. `30s`). |
| `TOIL_DISABLE_RESTORE` | When truthy, skip restoring in-flight runs on server start. |
| `TOIL_ROOT` | Toil-root path injected into runner subprocesses (exported as `TOIL_ROOT`, with `$TOIL_ROOT/bin` prepended to the runner `PATH` for repo-local tools like `tgwm`). It does not affect definitions loading or runs-dir resolution. |
| `TOIL_ENV_FILE` | Path to a `.env` file to load at startup (instead of `./.env`). |
| `TOIL_LOG_LEVEL` | Server log level: `DEBUG`, `WARN`, `ERROR` (default INFO). |
| `PROJECT_DIR` | Forwarded into a run's env by `run` when set. Also used by eval specs. |

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code (promoted to evergreen reference; Phase-3 verified)._
