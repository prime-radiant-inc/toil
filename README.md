# Toil

Toil is a file-defined workflow orchestrator for role-driven processes. It runs
workflows described in YAML and writes append-only run logs plus snapshots to
disk. Node prompts are written inline in the workflow YAML.

Everything is definitions-first. You edit files under `definitions/`, then run
workflows via CLI or API. The runtime persists state in `runs/` and supports
resume, approvals, parallel execution, and visualization.

**What It Does**
- Executes workflows with roles, humans, subworkflows, and system nodes
- Routes decisions via edges (including cycles with loop limits)
- Runs agents via real CLI runners (Codex, Claude, and serf)
- Logs every output line to `runs/<id>/events.jsonl`
- Resumes in-progress runs and preserved sessions
- Exposes a minimal web API and interactive graph views (D3 + ELK.js)
- Serves a Stockyard-style web UI at `/ui`

**Requirements**
- Go 1.25+
- `codex` CLI (for Codex runner)
- `claude` CLI (for Claude runner)

## Brainstorm a Design (Example)

Run the brainstorm workflow to interview and iterate on an idea until you have a
validated spec and story cards.

```bash
export PROJECT_DIR=~/work/toil-demo/todo
mkdir -p "$PROJECT_DIR"

# Keep run output under the repo (otherwise runs default to
# ~/.local/share/toil/runs). Run from the repo root.
export TOIL_RUNS_DIR="$PWD/runs"

# Start the server (in another terminal)
go run ./cmd/toil serve --addr :8080

# Or run it as a background daemon (logs in runs/server.log)
go run ./cmd/toil serve --addr :8080 --daemon

go run ./cmd/toil run brainstorm \
  --input idea="Build a CLI to-do list app in Go with add, list, done commands. Store data in a local JSON file." \
  --input context="Use TDD. Keep scope small."
```

Resolve approvals in another terminal:

```bash
go run ./cmd/toil approvals list

go run ./cmd/toil approvals resolve <approval_id> \
  --decision approved \
  --message "Approved."
```

The run continues automatically after each approval.

## Quickstart
1. Validate definitions.
   - `PROJECT_DIR=/path/to/project go run ./cmd/toil validate`
2. List workflows.
   - `go run ./cmd/toil workflows list`
3. Start the server.
   - `go run ./cmd/toil serve --addr :8080`
   - `go run ./cmd/toil serve --addr :8080 --daemon`
   - Open `http://127.0.0.1:8080/ui`
4. Run the end-to-end eval (auto-approves human gates).
   - `go run ./cmd/toil eval todo_end_to_end --project-dir /path/to/project`
5. Inspect logs.
   - `ls runs/<run_id>/`
   - `tail -n 50 runs/<run_id>/events.jsonl`

## Core Concepts
- Runner definition: YAML in `definitions/runners/`
- Workflow definition: YAML in `definitions/workflows/`
- Runs: append-only logs plus snapshots in `runs/<run_id>/`

## Directory Layout
- `definitions/runners/`: runner config (Codex, Claude, serf, shell, human)
- `definitions/workflows/`: workflow specs
- `runs/`: runtime logs and state snapshots
- `docs/specs/`: reference documentation (index: `docs/specs/README.md`)
- `docs/tutorials/`: hands-on guides
- `tests/eval/`: eval suite definitions
- `tests/fixtures/`: small demo repos used in evals

## CLI Highlights
- `validate`
- `workflows list | show <id>`
- `run <workflow_id> --input key=value`
- `resume <run_id>`
- `runs list | show <run_id> | events <run_id>`
- `approvals list | resolve <id> --decision <d> --message <m> --comment <c>`
- `visualize workflow <id> | run <run_id>`
- `serve --addr :8080 --daemon`
- `eval <eval_id>`

For the full command list, run `toil` with no arguments.

## Approvals
Human gates are represented as approval records under `runs/<run_id>/approvals/`.
The server resumes runs after approvals are resolved.

## Server Connection
The CLI talks to the server at `TOIL_URL`. If unset, it defaults to
`http://127.0.0.1:8080`.

## Visualization
`visualize` prints graph JSON for a workflow or run state. Workflows can contain
cycles and are not treated as DAG-only.

## Documentation
- End-to-end tutorial: `docs/tutorials/end-to-end.md`
- Reference docs: `docs/specs/` (start at `docs/specs/README.md`)

## License
Licensed under the Apache License, Version 2.0. See [`LICENSE`](LICENSE) and
[`NOTICE`](NOTICE).

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code (the "Stockyard-style" UI note deferred to review)._
