# Workspaces and Artifacts

Goals
1. Support isolated execution per role node.
2. Support shared workspaces for collaborating nodes.
3. Support artifact handoffs without direct filesystem access to other workspaces.
4. Containerization is not required for V1.

Workspace policies
- isolated: default. Each node gets its own workspace directory.
- shared: node uses the run-level shared workspace.
- group: node uses a named shared workspace group.
- project: node uses an explicit project directory.

Workspace declaration
- A node selects a policy via its `workspace` object (mode isolated | shared | group | project).
- `group` names a shared workspace group; `path` points at an explicit project directory; `access` expresses an intended read_write/read_only boundary that the engine does not yet enforce.
- For field types and required-when constraints (e.g. `group` required when mode is group, `path` required when mode is project), see `schemas.md` (Workspace fields). The `access` value is parsed but not consumed — see `schemas.md` for its accepted values and best-effort status.

Workspace path strings are run through the toil expression resolver, so namespaced expressions like ${env.PROJECT_DIR}, ${input.X}, and ${workflow_input.X} resolve at dispatch time. A bare ${PROJECT_DIR} (no namespace prefix) is treated as a literal, not expanded.

Workflow defaults
- workflow.workspace_defaults can set a default workspace for all nodes. For how node.workspace and workflow.workspace_defaults are resolved (precedence), see `runtime.md` (Workspaces).

Standard environment variables
- PROJECT_DIR: absolute path to the project directory used by code workflows. Reference it in a workspace path as ${env.PROJECT_DIR}.

Run directory layout
- runs/<run_id>/
  - events.jsonl
  - state.json
  - workspaces/
    - shared/
    - groups/<group_id>/
    - nodes/<node_id>/
  - artifacts/
    - <node_id>/
Project workspaces live outside the run directory.

Artifact handoff
- Nodes emit artifacts via their structured JSON output (for example, a fenced ```json block or communicate().output).
- Artifacts are copied (or referenced) into runs/<run_id>/artifacts/<node_id>/.
- Downstream nodes access artifacts via inputs, not by reading other workspaces.
- This allows handoff without granting filesystem access to other workspaces.

Access model (V1)
- V1 does not require containerization.
- The engine sets the working directory for each role process.
- Access limits are best-effort. In V2, containers or sandboxing can enforce read-only and workspace boundaries.

Examples
1) Isolated workspaces with artifact handoff
- implement node writes artifacts
- review node reads artifacts from runs/<run_id>/artifacts/implement

2) Shared workspace for iterative collaboration
- implement, spec_review, and code_review use workspace.mode: group with group "task-1"

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code (promoted to evergreen reference; Phase-3 verified)._
