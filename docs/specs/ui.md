# Web UI

Base path
- The dashboard is served at `/ui` from the same `toil serve` process (mounted with `http.StripPrefix("/ui", ...)`; `/ui` redirects to `/ui/`). The dashboard is a separate server from the root API server; its routes are relative to `/ui`.

Required views
- Overview page with counts for workflows, runs, active execution groups, and active child runs, plus a banner when approvals are pending.
- Workflow catalog page (`/workflows`) that links to workflow detail.
- Workflow detail view with a tabbed layout (Graph / Runs / Start Run): an interactive graph and a run-start form.
- Run detail view with an interactive graph, an execution timeline/document, and a streaming transcript viewer.
- Approvals inbox with decision, message, and comment inputs.
- Learnings page (`/learnings`).

Control plane actions
- Start a run from a workflow detail page.
- Resume, cancel, or retrigger a run from the run detail page (run actions are POSTed to the dashboard server itself, e.g. `/ui/runs/{id}/cancel`, which calls the in-process orchestrator — not the root API server).
- Resolve approvals from the approvals inbox.

Visualization
- Graphs are rendered client-side with D3.js (SVG). Layout is computed in the browser by ELK.js (`elkjs`, layered algorithm); the server sends graph *topology* (nodes and edges) with no layout coordinates — see `internal/visualize/topology.go` (`TopologyGraph`) and `internal/dashboard/static/js/elk-graph.js`.
- Graph topology is embedded into the page HTML on first paint as a `<script type="application/json">` block (e.g. `run-compound-graph-source`, `workflow-compound-graph-source`), which the client parses and lays out — there is no server-side SVG/positioned-graph render.
- On live updates, the client patches node status in place (`ElkGraph.updateNodeStatus`) from SSE `graph-update` events rather than re-fetching the whole graph; the compound graph can also be refreshed from `GET /runs/{id}/compound-graph`.
- Cycles are supported and are not treated as DAG-only.

Data sources
- Event stream for graph status updates comes from the dashboard SSE endpoint `GET /ui/runs/{id}/stream`; the transcript view subscribes to the root API SSE endpoint `GET /runs/{id}/events/stream`. Cross-run metric updates stream from `GET /runs/{id}/execution-group/metrics?follow=true`.
- Graph topology JSON is available from the root API server: the single-run graph view consumes `GET /runs/{id}/graph`, the execution-group view consumes `GET /runs/{id}/compound-graph`, and the workflow view consumes `GET /workflows/{id}/graph`. All return `TopologyGraph` JSON (no coordinates); see `api.md` for each route's contract.

Style
- Tailwind utility classes (served locally from `/static/js/tailwind.min.js`; config inline in `_head.html`), Alpine.js for interactivity, and HTMX for AJAX swaps, with a left sidebar navigation layout (`_sidebar.html`). Design tokens: accent `#1a6b5a`, ink `#1b2631`, muted `#6b7c8f`, surface `#f6f8fa`; fonts Fraunces (display) and DM Sans (body).

---
<!-- doc-audit:last-reviewed -->
_Last reviewed: 2026-06-07 · commit `972b726` · verified against code (promoted to evergreen reference; Phase-3 verified)._
