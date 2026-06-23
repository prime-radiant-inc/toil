# Toil Reference Documentation

Evergreen reference for how Toil works now. The code is canonical; each doc is a
verified, navigable summary of one topic, carrying a `last-reviewed` provenance
marker. See [`reference-doc-standard.md`](reference-doc-standard.md) for the
contract every doc here follows.

## System

- [`overview.md`](overview.md) — what Toil is, its goals, and key design decisions.
- [`file-layout.md`](file-layout.md) — repository layout, definition load order, and where run output lives (`TOIL_RUNS_DIR`).
- [`server.md`](server.md) — server-as-control-plane architecture and the run lifecycle.

## Authoring workflows

- [`schemas.md`](schemas.md) — the definition schemas: workflow / node / edge / runner fields, enums, and load-time validation rules. *(Canonical owner of field schemas.)*
- [`workflow-reference-example.md`](workflow-reference-example.md) — one comprehensive example workflow demonstrating every field, plus common patterns.
- [`workflows.md`](workflows.md) — catalog of the shipped workflows.
- [`software-factory.md`](software-factory.md) — the software-factory pipeline (`implement_spec` → `build_component` → `implement_task`) and its design principles.
- [`runners.md`](runners.md) — the runner contract (the `Run` Go interface, streaming, resume) and runner types.

## Runtime & execution

- [`runtime.md`](runtime.md) — runtime semantics: readiness, decision routing, loops, retries, timeouts, context modes, ForEach execution, cancellation. *(Canonical owner of runtime/firing semantics.)*
- [`workspaces.md`](workspaces.md) — workspace policies, the per-mode run-directory layout, and artifact handoff.
- [`approvals.md`](approvals.md) — human gates, approval records, and timeout auto-resolution.
- [`interviews.md`](interviews.md) — interview mode (engine-triggered) and interrogation (live diagnostic Q&A on a node).
- [`eval-suite.md`](eval-suite.md) — the eval spec schema and how evals run without human intervention.

## Observability & introspection

- [`logging-and-state.md`](logging-and-state.md) — the event log, the event types, state snapshots, and run/node statuses. *(Canonical owner of event types and statuses.)*
- [`inspect.md`](inspect.md) — the `inspect` aspect system (overview/flow/timing/tokens/…).
- [`metrics.md`](metrics.md) — duration / token / cost accounting and the live metric stream.
- [`document-model.md`](document-model.md) — the run-detail document/transcript tree that the UI renders.
- [`webhooks.md`](webhooks.md) — run-completion callbacks (`callback_url`).

## Interfaces

- [`cli.md`](cli.md) — the `toil` CLI: commands, flags, and environment variables. *(Canonical owner of CLI flags.)*
- [`api.md`](api.md) — the HTTP API routes and request/response contracts. *(Canonical owner of HTTP routes.)*
- [`ui.md`](ui.md) — the web dashboard.

## Meta

- [`reference-doc-standard.md`](reference-doc-standard.md) — the standard every doc here follows, and the brief for writing a new one.
