# Toil Overview

Purpose
Toil is a standalone workflow orchestrator for role-driven processes. It executes
file-defined workflows, coordinates roles and humans, and records a complete
run history.

Goals
1. Provide a workflow engine that is usable and testable on its own.
2. Keep definitions in text files rather than a database.
3. Support multiple runners per step, including Claude, Codex, and serf.
4. Log every single line of role output.
5. Resume roles by session token without losing run history.
6. Offer both CLI and web API interfaces.
7. Provide a web UI for state and logs.

Success criteria (V1)
1. Run a full software-factory workflow end-to-end in Toil, from brainstorming to
completion, creating a small to-do list app in a new repo.
2. Encode the success criterion as an eval suite scenario.

Non-goals
1. No token budgets or tool allow-lists.
2. No hot reload of workflow or role definitions.
3. No dependence on a specific business org model.

Key decisions
1. Definitions are files loaded at startup.
2. Workflow specs are YAML; node prompts are written inline in the workflow YAML.
   Runners are defined as YAML under `definitions/runners/`.
3. A node's role is a string label on the node, not a separate definition file.
4. Run history is append-only JSONL plus snapshots.
5. Runner selection is supported per node: a node's explicit `runner` attribute,
   falling back to tag-based workflow runner overrides.
6. If a process is still running, the engine never detaches.
7. Resume uses session tokens when a process died.

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code (promoted to evergreen reference; Phase-3 verified)._
