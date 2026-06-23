# End-to-End Tutorial

This tutorial shows how to build real software with the software-factory workflows.
It walks through running an end-to-end workflow, handling approvals, and
inspecting logs. It also includes the eval run used for validation.

## Prerequisites
- Go 1.25+
- `codex` CLI installed and authenticated
- `claude` CLI installed and authenticated
- Toil server running at `TOIL_URL` (default `http://127.0.0.1:8080`)

## Build A Real App (Brainstorm Workflow)
This is the entry point for building software (for example, a to-do list
CLI or a small Snake game). The brainstorm workflow interviews you to refine an
idea into a validated spec and story cards.

### 1) Choose a project directory

```bash
export PROJECT_DIR=~/work/toil-demo/todo
mkdir -p "$PROJECT_DIR"

# Keep run output under the repo (otherwise runs default to
# ~/.local/share/toil/runs). Run these commands from the repo root.
export TOIL_RUNS_DIR="$PWD/runs"
```

If you want a repo, initialize it now (optional):

```bash
cd "$PROJECT_DIR"
git init
```

### 2) Run the workflow

This example builds a small to-do list CLI in Go using TDD.

```bash
# Start the server (in another terminal)
go run ./cmd/toil serve --addr :8080

# Or run it as a background daemon (logs in runs/server.log)
go run ./cmd/toil serve --addr :8080 --daemon

go run ./cmd/toil run brainstorm \
  --input idea="Build a CLI to-do list app in Go" \
  --input context="Keep scope small: add, list, done. Use TDD. Store data in a local file."
```

### 3) Resolve approvals (in another terminal)

List approvals:

```bash
go run ./cmd/toil approvals list
```

Resolve each approval (examples):

```bash
go run ./cmd/toil approvals resolve <approval_id> \
  --decision approved \
  --message "Approved." \
  --comment "Proceed."
```

If asked for clarification during brainstorming:

```bash
go run ./cmd/toil approvals resolve <approval_id> \
  --decision clarified \
  --message "Here are the clarifications..."
```

When asked about finishing the branch, you can keep it:

```bash
go run ./cmd/toil approvals resolve <approval_id> \
  --decision keep \
  --message "Keep the branch for now."
```

The run will continue automatically after each approval is resolved.

### 4) Inspect logs

Each run writes a directory under `runs/`:

```bash
ls runs/<run_id>/
```

Key files:
- `events.jsonl`: append-only log, one JSON event per line
- `state.json`: snapshot of current run state
- `workflow.yaml`: workflow snapshot used for the run

To view recent output lines:

```bash
tail -n 50 runs/<run_id>/events.jsonl
```

### 5) Visualize

```bash
go run ./cmd/toil visualize workflow brainstorm
```

```bash
go run ./cmd/toil visualize run <run_id>
```

Workflows can contain cycles; the graph output is not treated as a strict DAG.

## End-to-End Eval (Validation)

This eval auto-approves human gates and runs the full pipeline against the demo
fixture.

```bash
go run ./cmd/toil eval todo_end_to_end
```

The result is saved to `runs/<run_id>/eval.json`.

## Semantic Port Eval

The semantic port eval exercises a 7-node looping workflow that tracks upstream
Python commits and ports semantic changes to a Go codebase. It uses an external
Go port fixture repo (set via `OAG_REPO` in the setup script).

### 1) Run the setup script

```bash
tests/semantic_port/setup.sh
```

This clones (or resets) the Go port fixture and the upstream Python repo, and
builds the `semantic_port` CLI tool into `bin/`.

### 2) Run the eval

```bash
OAG_DIR=/tmp/openai-agents-go \
UPSTREAM_DIR=/tmp/oap-upstream \
  go run ./cmd/toil eval semantic_port
```

The eval auto-approves all gates and verifies with `go test ./...` at the end.
A typical run takes 30-60 minutes depending on runner performance.

### 3) Inspect results

```bash
cat runs/<run_id>/eval.json
```

The run state shows per-node status, decisions, and timing:

```bash
cat runs/<run_id>/state.json | python3 -m json.tool
```

### Resetting between runs

The setup script resets the fixture repo to its baseline state. Run it again
before each eval to start clean.

## Customize Runners
Each `kind: role` node selects its runner via the node's `runner:` field. There
are no role definition files — node prompts and runner choices live directly in
the workflow YAML.

To switch the runner for a node, edit its `runner:` field:

```yaml
- id: write_code
  kind: role
  runner: serf
  prompt: |
    ...
```

Available runner IDs (see `definitions/runners/`):
- `codex`
- `claude`
- `serf`
- `shell`
- `human`

## Troubleshooting
- Missing `project_dir` input: pass `--input project_dir=/path/to/project` (or via API inputs).
- Paused run: approvals are pending. Resolve them; the run keeps going.
- Runner not found: check `definitions/runners/` and node `runner:` fields.

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code._
