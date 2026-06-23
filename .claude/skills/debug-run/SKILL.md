---
name: debug-run
description: Use when a toil run has failed or produced unexpected results and you need to investigate what happened
---

# Debug Run

Systematic post-mortem investigation of toil workflow run failures.

## Entry Point

Resolve the run to investigate:
- **URL**: extract run ID from `/ui/runs/{id}` or `/runs/{id}` path
- **Run ID**: use directly
- **"Latest failure"**: `GET /runs` returns `[]string` of IDs (not objects). Fetch `GET /runs/{id}` for recent IDs, find most recent with `status: "failed"` by comparing `started_at` timestamps. Do NOT sample randomly — work backwards from the end of the list.

Base URL: `TOIL_URL` env var, default `http://localhost:8080`.

## Tier 1: Quick Triage (default)

Single agent. Do this first — escalate to Tier 2 only if inconclusive.

1. `GET /runs/{id}` — check `status`, scan `nodes` map for `status: "failed"`, read both `message` and `error` fields on failed nodes
2. `GET /runs/{id}/events` — **JSONL** (one JSON object per line, NOT a JSON array). Parse line-by-line. Find `node_failed` and `run_failed` events. Build a timeline.
3. **Check `@resources/failure-patterns.md` for known signature match.** Do this BEFORE proposing a fix. If the error matches a known pattern, use the documented fix.
4. Identify the **original failure** vs downstream cascades. If a run was retriggered and failed again, the first `node_failed` event is usually the root cause. Later failures may be consequences of retrigger attempts. Report them separately.
5. **Check for parent context.** If the run has a `parent_run` field, this is a child run — always check the parent run's state to understand why this child was spawned and whether the parent has additional context. Do NOT debug a child run in isolation.
6. Report: failed node, error message, suggested fix, confidence level

**Escalate to Tier 2 if:** error is in a child run, root cause is ambiguous, no pattern match, or the failure involves multiple interacting nodes.

## Tier 2: Deep Investigation

Multi-agent fan-out. Run in addition to Tier 1 findings.

1. `GET /runs/{id}/compound-graph` — discover all child runs and their topology. **Do NOT manually trace child runs from state/events** — use this endpoint.
2. Fan out subagents (cap ~10): each child run gets `GET /runs/{child_id}` + `GET /runs/{child_id}/events` (JSONL). Build timeline per child. Aggregate results back in the parent agent.
3. `GET /workflows/{workflow_id}` — returns raw **YAML** (`text/plain`, NOT JSON). Cross-reference the failing node's definition: check its `decisions`, `outputs`, `inputs`, and edges. Understanding what a node declares tells you what validation it triggers.
4. `GET /health` — server status, uptime, active run counts
5. Examine project code artifacts if accessible (check node `data` for file paths)
6. **Systemic audit**: are other nodes in the same workflow (or other workflows) vulnerable to the same failure pattern? For output validation failures, check all shell nodes that declare `decisions` or `outputs`.
7. Synthesize: full chronological timeline, root cause analysis, systemic recommendations

**When debugging a child run**, always trace upward to the parent. A child run failure may be a symptom of a parent-level issue. Check `parent_run` field in the child's state.

## Output Validation Quick Reference

When a node declares `decisions` or `outputs` in workflow YAML, its JSON output MUST include all three at the **top level**:
```json
{"decision": "done", "message": "completed successfully", "data": {}}
```

The `data` field must be a JSON object (not null, not missing). **Do NOT nest decision/message inside data.** They are siblings, not children.

Validation is skipped entirely for nodes that declare neither `decisions` nor `outputs`.

## Failure Pattern Feedback

After Tier 2, if root cause is novel:
1. Check `@resources/failure-patterns.md` — does this signature already exist?
2. If not, append it using the template in that file (signature, root cause, fix, affected, found date)
3. Commit to current working branch

## Resources

- `@resources/toil-api.md` — endpoint reference
- `@resources/data-model.md` — run/node states, event types, validation contract
- `@resources/failure-patterns.md` — known failure signatures
