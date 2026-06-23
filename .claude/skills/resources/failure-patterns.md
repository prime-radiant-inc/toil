# Toil Failure Patterns

Living catalog of known failure signatures. When debugging a run, check this file first for known patterns before deep investigation.

When you identify a novel failure pattern during a Tier 2 investigation, append it here following the template below and commit to the current working branch.

---

### Missing `data` field in shell node output

**Signature:** `node output validation failed: field "data" is required and must be a JSON object`

**Root cause:** Shell node declares `decisions` (or `outputs`) in its workflow YAML, but its script outputs JSON without a `data` key. The output validation requires `data` to be a non-null JSON object whenever structured output parsing is triggered.

**Fix:** Add `data: {}` to the node's jq output template. Both branches of any conditional must include it.

Before: `jq -n '{decision:"skip", message:"..."}'`
After: `jq -n '{decision:"skip", message:"...", data:{}}'`

**Affected:** Any shell node with `decisions` or `outputs` declared. Check all workflow YAML files for shell nodes that use jq/JSON output.

**Found:** 2026-03-12 (run `brook-brisk-falcon`, node `check_delivery_configured` in `deliver` workflow)

---

### Missing required secrets (pre-dispatch failure)

**Signature:** `node <node_id>: missing required secrets: <KEY>`

**Root cause:** Run input `secret_keys` lists secrets that were not present in the server's OS environment when the run was created. Secrets are captured at run creation via `captureEnvWithSecrets()` and held in memory only — never persisted to disk. If the server was started without the required env var exported, or restarted mid-run, secrets are lost.

**Fix:** Export the required secret in the server's environment before starting: `export GITHUB_TOKEN=ghp_... && make serve`. Then re-trigger the run.

**Engine bugs (fixed in PRI-685):**
- Pre-dispatch failures (`checkRequiredSecrets`) did not record a NodeState or emit `node_failed` events, making the failing node invisible in the dashboard. Fixed by adding `recordPreDispatchFailure()` in `execute.go`.
- Parent run failure did not cancel in-flight child runs, leaving them as zombies in `running` status forever. Fixed by calling `cancelChildren()` from the worker failure path in `manager.go`.

**Affected:** Any shell node in a run that declares `secret_keys` in its inputs.

**Found:** 2026-03-12 (run `canyon-nebula-pine`, node `check_delivery_configured` in `deliver` workflow)

---

### debug_fix loop exhausts max iterations; diagnosis points at toil's own code; working tree dirty

**Signature:** A `debug` subworkflow (usually reached via `debug_fix` from `build_component`) fails with `max loop iterations exceeded for debugger (decision=root_cause_confirmed)` and a summary blaming toil's runtime — "stale workflow definition", "workflow snapshot not reloaded", "runtime caching". `git status` in the toil repo shows uncommitted edits to files outside the subject project: `definitions/workflows/*.yaml`, `internal/engine/*.go`, or similar.

**Root cause:** The `debug` subworkflow's `write_code` agent exceeds its intended scope and edits toil's own source tree. Those edits can't affect the running system — definitions are loaded at server startup (see `toil/CLAUDE.md`), workflow snapshots freeze at dispatch under `runs/<id>/workflow.yaml`, and Go changes need rebuild+restart. Each subsequent loop iteration spawns a fresh verifier child run that re-reads the same stale in-memory definition and re-fails identically. After `max_loop_iterations` (5) the run aborts.

The *first-round* diagnosis is often correct (e.g., in run `flint-oak-willow` → debug run `scout-iris-tundra`, the agent correctly identified `test_command: null` producing a literal `"null"` in `TEST_COMMAND`). The problem is the agent applies the fix in the wrong place.

**Fix:**
1. `git -C toil status` — inspect the working tree. Revert any edits outside the subject project: `git checkout <file>`.
2. If any edit happens to be correct (e.g., a defensive verify.yaml guard), review and land it deliberately, not via the debug loop.
3. Re-dispatch the failed run (or retrigger from `debug_fix`) only AFTER restarting toil so the corrected definitions load.
4. The underlying containment issue — agents reaching the orchestrator's source tree — is separate work.

**Affected:** Any run whose `debug` subworkflow has a `write_code` node running `serf` with access to the filesystem outside the worktree (the default). Particularly likely when the failure *genuinely* involves workflow definitions or verifier scripts, because that's when the agent correctly locates the relevant file — and then edits it in the wrong repo.

**Found:** 2026-04-23 (run `flint-oak-willow` → `otter-blaze-fern` → `north-timber-apex` → `scout-iris-tundra`; agent edited `definitions/workflows/verify.yaml` and `internal/engine/engine.go`)

---

### Run header shows "running" while a node shows red/failed — state.json frozen by ENOSPC

**Signature:** Sidebar tree shows the run (and its child runs) with blue pulsing "running" dots, but one or more nodes inside show red "failed" dots with valid error messages. `events.jsonl` contains `node_failed` / `run_failed` events but `GET /runs/<id>` still reports `"status": "running"`. Hallmark: `state.json` mtime is much older than `events.jsonl` mtime (hours apart). The originating error message usually contains `"no space left on device"` and references a `state.json.tmp.<pid>` path.

**Root cause:** Disk filled up while the run was executing. `state.SaveState` writes atomically via tmp-file-then-rename; `open(state.json.tmp.XXXXX)` fails with ENOSPC, so every save attempt since then silently fails. Meanwhile `events.jsonl` keeps working — appending to an already-open fd doesn't need a new block allocation immediately, so the event log records the failure (`node_failed`, `run_failed`) while on-disk `state.json` is frozen at its last successful write (often the first `ensure_repo` completion). The dashboard's per-run status comes from `state.LoadState(state.json)` in `handleRunDetail` (`internal/api/server.go`), while the sidebar's per-node status comes from `events.jsonl` in `extractRunNodeData` (`internal/dashboard/report.go`) — so they diverge.

**Fix:** Free disk space first. Runs that need to be marked failed should be cancelled or re-triggered from the failed node — a server restart alone won't update the stale state.json. The underlying engine bug (SaveState failures don't propagate to in-memory state or surface to the user) is separate work worth tracking.

**Affected:** Any run executing when the disk filled. Child runs show the cascade: `state.json` on the parent and every descendant frozen at its respective last successful write, while `events.jsonl` captures the failure at the exact node that triggered ENOSPC.

**Found:** 2026-04-23 (run `nebula-cirrus-falcon`; grandchild `flint-willow-cirrus` state.json.tmp failed at 10:18 local, parent state.json mtime 08:41, events.jsonl mtime 10:18)

---

### Duplicate child run dispatched after server restart (worktree path collision)

**Signature:** `fatal: '<worktree path>' already exists` from `create_worktree.sh`, surfacing as `node build_component: node create_worktree: shell command exited with code 1`. Hallmark in the events log: two `wave_started` events for the same ForEach orchestrator (e.g., `build_component`) separated by a long gap, each spawning a DIFFERENT child run ID for the same expanded item (`build_one_component::0`). The first child's run state will be `cancelled` with `finished_at` near the second child's failure time.

**Root cause:** `executeSubworkflow` in `internal/engine/execute.go` (function starts line 1902) mutates the parent `NodeState.Status = statusRunning`, `StartedAt`, and `Data["child_run"] = childRunID` via `runState.WithNode`, but never calls `state.SaveState`. It then synchronously calls `engine.ResumeRun(ctx, childRunID)` which can block for an arbitrary duration (the entire child/grandchild chain runs in the same goroutine). If the server is stopped/killed before that chain returns or hits `ErrSubworkflowInProgress` — which is what triggers the first persistence of the child_run pointer in `resume.go:113` — the parent's link to the child is lost. On restart, `manager.Restore()` reads a state.json that has no record of the dispatch, the parent re-runs the orchestrator from scratch, and a brand-new child is spawned. For shell nodes that build deterministic paths from the PARENT's `TOIL_RUN_ID` (worktrees, caches, lock files), the second child collides with the first.

This is an oversight from commit `daa3292` (2026-03-23, "fix: save state before node execution starts") which correctly added `SaveState` to `executeRole`, `executeShellRole`, and `executeHuman` but missed `executeSubworkflow`.

**Fix:** Add `_ = state.SaveState(filepath.Join(runDir, "state.json"), runState)` inside `executeSubworkflow` right after the `runState.WithNode(stateNodeID, ...)` that sets `Data["child_run"] = childRunID` (both the new-child branch around line 1945 and the fresh-dispatch branch around line 1931, before the blocking `ResumeRun` call). The existing re-entry/crash-resume logic at line 1952 (`else` branch that loads the child and returns `ErrSubworkflowInProgress` when the child is still running) already handles reconnection correctly — it just needs the parent's `Data["child_run"]` to survive the restart. `executeSubworkflow` takes `runDir` via the caller's closure (inspect `executeSingle` call site at line 1216) — either thread runDir through or pass it in; currently the function has no direct access to runDir, which is why the SaveState was never added.

**Related but not the bug:** `Manager.Shutdown()` at `internal/orchestrator/manager.go:419` rewrites `running → pending` for all nodes in all in-flight runs. This is intended to let resume re-execute them. It interacts correctly with `executeSubworkflow`'s reconnection path — the re-entry check at line 1915 only fires for `status == statusCompleted` (loop re-entry), not for pending/running, so `Data["child_run"]` is picked up via the else branch. The only thing missing is the initial persistence.

**Affected:** Any subworkflow dispatch (including ForEach bodies that are subworkflow templates) that is in flight when the server restarts. Most visible for workflows where the child creates external-disk state keyed on the parent run ID — `implement_spec` → `build_component` → `build_one_component` → `create_worktree` is the canonical example.

**Found:** 2026-04-21 (run `fjord-ember-forge`; orphaned first child `copper-crest-quest`, collided second child `river-otter-ember`)

---

### Eval reports "passed" but workflow contains a `_loop_exhausted` route / `declare_*_stuck` node

**Status (2026-05-20):** Resolved in the meta-decision failure routing implementation. See `docs/superpowers/specs/2026-05-19-meta-decision-failure-routing-design.md` (v11) and `docs/superpowers/plans/2026-05-20-meta-decision-failure-routing.md`. The signature below is preserved as a diagnostic clue for workflows that miss the migration (new meta-decision edges that forget `failed:` — should be caught by lint at workflow load, but listed here as a backstop).

**Signature:** `eval.json.status: "passed"` AND the run's events.jsonl (or any descendant's) contains a `failure_edge_fired` event OR a `run_completed` event whose `data.has_unresolved_failure == true`. After the 2026-05-20 implementation, a passing eval with `has_unresolved_failure=true` anywhere in the tree should be impossible.

NOTE: A `_loop_exhausted` `node_completed` event alone is NOT a bug signature — it can fire legitimately on edges marked `failed: false` (recovery routings such as the judge in `implement_task.yaml` or planning give-up in `build_component.yaml`). The bug signature is specifically: failure-flagged routing fired (`failure_edge_fired` emitted) but eval still reports passed.

**How to diagnose under the new design:**

```bash
# Check the run's HasUnresolvedFailure flag (now persisted on state.json):
jq '.has_unresolved_failure' < ~/.local/share/toil/runs/<run-id>/state.json

# Find failure_edge_fired events (emitted by the engine when failed:true edges fire):
jq -r 'select(.type=="failure_edge_fired") | .node_id + " | when=" + .data.when + " to=" + .data.to' < ~/.local/share/toil/runs/<run-id>/events.jsonl

# Check the run_completed event's annotation:
jq -r 'select(.type=="run_completed") | "has_unresolved_failure=" + (.data.has_unresolved_failure | tostring)' < ~/.local/share/toil/runs/<run-id>/events.jsonl
```

**What to fix in a workflow that misses the migration:** Every edge whose `when:` is `_loop_exhausted`, `_timeout`, or `_retry_exhausted` must declare `failed:` explicitly (`true` if the routing represents run-level failure, `false` if it's a recovery path that the workflow author considers normal). The lint at `internal/definitions/graph_validation.go` enforces this at workflow load — if your workflow loads cleanly but you suspect a silent pass, look for `failed: false` on an edge that should be `failed: true` (or vice versa).

**Affected:** Any workflow using `_loop_exhausted` / `_timeout` / `_retry_exhausted` edges. Verify with `failure_edge_fired` event grep and the `state.json` flag.

**Found:** 2026-05-19 (runs `crest-onyx-mist` and `fern-solstice-indigo`; see `docs/testing/2026-05-19-eval-pass-findings.md`). **Resolved:** 2026-05-20 (verified in run `stone-crest-fern` — re-ran `v7_loop_exhaustion` eval after implementation; `eval.json.status = "failed"` with `failure_edge_fired` event on `run_e2e_tests` and `has_unresolved_failure: true` on all three runs in the tree).
