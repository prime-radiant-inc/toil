# File Layout

Repository layout
- bin/ (built binaries, e.g. semantic_port; added to PATH during eval)
- definitions/runners
- definitions/workflows
- docs/specs
- tests/eval/ (eval spec YAML files)
- tests/semantic_port/ (setup script for semantic port eval)

Run output is written outside the repo tree by default: $XDG_DATA_HOME/toil/runs
(or ~/.local/share/toil/runs when XDG_DATA_HOME is unset). Override with the
TOIL_RUNS_DIR environment variable. A repo-local runs/ directory is gitignored.

Definition load order
1. runners
2. workflows

Definitions are loaded at startup and cached in memory. There is no hot reload.
Each run stores a snapshot of the workflow file (workflow.yaml) used at run start.

Naming rules
- File name should match id where practical.
- IDs are lowercase with underscores.
- Workflow IDs should be domain-oriented and descriptive, for example brainstorm or implement_spec.

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code (promoted to evergreen reference; Phase-3 verified)._
