# Eval Suite

The eval suite runs workflow scenarios end-to-end without human intervention.
Each eval is a YAML spec that defines what workflow to run, its inputs, and how
to verify the result.

## Eval spec schema

Eval specs live in `tests/eval/<id>.yaml`.

Required fields:
- id: string (unique identifier)
- workflow_id: string (workflow to run)
- project_dir: string (working directory for the workflow; supports `${VAR}`
  expansion). `LoadSpec` only enforces `id` and `workflow_id`; `project_dir` is
  resolved at run time (see `cli.md` for the resolution rules).

Optional fields:
- name: string (human-readable name)
- inputs: map of string to any (workflow inputs; string values undergo `${VAR}` expansion)
- verify: object with `command` (bash command to run after workflow completes)
  and optional `timeout` (Go duration; defaults to 5 minutes)
- auto_approve: boolean (auto-approve all human gates with sensible defaults)
- approvals: map of node_id to approval spec (per-node approval decisions).
  A non-empty `approvals` map also enables auto-approval even when
  `auto_approve` is false.

Approval spec fields:
- decision: string (decision to apply)
- message: string
- comment: string

## Invocation

Run a spec with `toil eval <id>`; the `<id>` resolves to `tests/eval/<id>.yaml`.
The invocation form and project-directory resolution rules (including
`--project-dir`) are documented in `cli.md`.

## Environment setup

The eval harness performs several environment preparations before running:

1. **ProjectDir expansion**: `${VAR}` references in `project_dir` are expanded
   via `os.ExpandEnv`. A relative result is joined with the toil root. This
   allows eval specs to reference external directories set by a setup script
   (e.g., `project_dir: "${OAG_DIR}"`).

2. **PROJECT_DIR env var**: The resolved project directory is exported as
   `PROJECT_DIR` for use in workflow workspace paths (`${PROJECT_DIR}`).

3. **PATH augmentation**: If a `bin/` directory exists under the toil root, it
   is prepended to `PATH`. This makes project-specific CLI tools (like
   `semantic_port`) available to runner subprocesses without installation.

4. **LEDGER_PATH wiring**: If `ledger_path` appears in the spec inputs, it is
   resolved to an absolute path (relative to the project dir) and exported as
   `LEDGER_PATH`.

5. **Input expansion**: All string values in `inputs` undergo `${VAR}`
   expansion. Non-string values are passed through unchanged. If `inputs` has no
   `project_dir` key, the resolved project directory is added under it.

Before each run, the harness also removes the project directory and any bare
repos under `<root>/repos/` to prevent cross-run contamination.

## Auto-approval

When `auto_approve: true` (or when `approvals` is non-empty), all human gates are
resolved automatically. The harness infers decisions by examining the workflow's
node declarations and the incoming decision from the previous node. The mapping:

- `needs_more_info` -> `clarified`
- `ready_for_review` -> `approved`
- `needs_changes` -> `changes_requested`
- Fallback: `approved`, or the first declared decision

Each inferred decision is only used if the node actually declares it; otherwise
the harness falls back to `approved` or the node's first decision.

Per-node overrides via `approvals:` take precedence over inference.

## Verification

The optional `verify.command` runs as `bash -lc` in the project directory after
the workflow completes, under a timeout (`verify.timeout`, default 5 minutes).
If it exits non-zero or times out, the eval fails and the captured output is
recorded in the result.

## Result

Each eval writes `eval.json` into the run directory
(`<runs-dir>/<run_id>/eval.json`, where `<runs-dir>` defaults to the
XDG data dir; see the runs-dir configuration). `status` is `passed`, `failed`,
or `paused` (the latter when an approval is pending and auto-approval is off).
`verify_output` is included only when verification ran.

```json
{
  "id": "semantic_port",
  "name": "Semantic Port Tracking",
  "run_id": "atlas-lantern-cedar",
  "status": "passed",
  "verify_output": "ok\tprimeradiant.com/...\n",
  "started_at": "2026-02-08T19:38:10Z",
  "finished_at": "2026-02-08T20:36:56Z"
}
```

## Example: semantic_port

The semantic port eval exercises a 7-node looping workflow that tracks upstream
commits and ports them to a Go codebase.

Setup:
```bash
tests/semantic_port/setup.sh
```

This clones your Go port fixture repo (set via `OAG_REPO` in the setup script) and
`openai/openai-agents-python` (the upstream), then runs `make build` (toil + tgwm).
Note: the `semantic_port` CLI the workflow shells out to is built by the separate
`make build-semantic-port` target, which `make build` does not invoke.

Run:
```bash
OAG_DIR=/tmp/openai-agents-go \
UPSTREAM_DIR=/tmp/oap-upstream \
  go run ./cmd/toil eval semantic_port
```

Spec (`tests/eval/semantic_port.yaml`):
```yaml
id: semantic_port
name: Semantic Port Tracking
workflow_id: semantic_port
auto_approve: true
project_dir: "${OAG_DIR}"
inputs:
  goal: >
    Intelligently track and port semantic changes from the upstream
    openai-agents-python repository to our Go implementation.
  project_dir: "${PROJECT_DIR}"
  upstream_dir: "${UPSTREAM_DIR}"
  ledger_path: "semantic_port/ledger.tsv"
verify:
  command: "go test -timeout 60s ./..."
```

## Validation

`toil validate` checks all definitions but does not load eval specs. Eval specs
are validated only when `toil eval` is invoked.

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code (promoted to evergreen reference; Phase-3 verified)._
