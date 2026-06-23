# Debugging Toil and Serf

Practical guide for diagnosing run failures, stuck agents, and configuration issues.

## Quick Triage via API

```bash
# Run overview — status, node states, inputs, parent chain
curl -s http://localhost:8080/runs/<run-id> | python3 -m json.tool

# List recent runs
curl -s http://localhost:8080/runs | python3 -m json.tool

# Cancel a stuck run
curl -s -X POST http://localhost:8080/runs/<run-id>/cancel
```

Check the run status and each node's status/attempts/error fields first. Common statuses:
- `running` — active (check if serf process is alive)
- `failed` — check node error fields
- `cancelled` — manually stopped
- `waiting_for_decision` — needs human approval

## Run Directory Structure

Each run persists to `runs/<run-id>/`:

```
runs/<run-id>/
  events.jsonl     # Append-only event log (primary debugging source)
  state.json       # Current snapshot of run state
  inputs/          # Materialized input files (spec.md, stories/, etc.)
  outputs/         # Node outputs
  workflow/        # Worktree and workspace state
  workflow.yaml    # Frozen copy of the workflow definition used
```

## Reading the Event Log

The event log (`events.jsonl`) is the richest debugging source. Each line is a JSON object with `timestamp`, `type`, `run_id`, `node_id`, and type-specific fields.

### Serf Events

When a serf runner executes, its verbose NDJSON output appears as `node_output` events with `stream: "stderr"`. The `text` field contains a JSON object with a `kind` field:

| Kind | What It Tells You |
|------|-------------------|
| `SESSION_START` | Serf session began |
| `PROMPT_LOADED` | System prompt section loaded (label, size) |
| `USER_INPUT` | The full prompt sent to the model |
| `ASSISTANT_TEXT_START/END` | Model's text output (check `full_text` in END) |
| `TOOL_CALL_START` | Tool invocation (`tool_name`, `arguments_json`) |
| `TOOL_CALL_OUTPUT_DELTA` | Tool result (`delta` field) |
| `STEERING_INJECTED` | Serf injected a steering prompt to correct behavior |
| `ROUND_TIMINGS` | Per-round timing breakdown and round number |

### Extracting the Conversation Flow

This one-liner shows the full tool call sequence and any text output:

```bash
grep 'node_output' runs/<run-id>/events.jsonl | python3 -c "
import sys, json
for line in sys.stdin:
    e = json.loads(line)
    text = e.get('text','')
    if not text: continue
    try:
        inner = json.loads(text)
        kind = inner.get('kind','')
        data = inner.get('data',{})
        if kind == 'TOOL_CALL_START':
            args = data.get('arguments_json','')[:150]
            print(f'  TOOL: {data.get(\"tool_name\",\"?\")}({args})')
        elif kind == 'TOOL_CALL_OUTPUT_DELTA':
            delta = data.get('delta','')[:200]
            if delta.strip():
                print(f'    -> {delta[:200]}')
        elif kind == 'ROUND_TIMINGS':
            r = data.get('round','?')
            total_ms = data.get('total_round_ns',0) / 1e6
            print(f'  --- end round {r} ({total_ms:.0f}ms) ---')
        elif kind == 'ASSISTANT_TEXT_END':
            ft = data.get('full_text','')
            if ft.strip():
                print(f'  SAYS: {ft[:300]}')
        elif kind == 'STEERING_INJECTED':
            print(f'  [STEER: {str(data.get(\"text\",\"\"))[:120]}]')
        elif kind == 'USER_INPUT':
            print(f'=== PROMPT: {str(data.get(\"text\",\"\"))[:200]}')
    except:
        pass
"
```

### Counting Event Kinds

Quick summary of what happened in a run:

```bash
grep 'node_output' runs/<run-id>/events.jsonl | python3 -c "
import sys, json, collections
kinds = collections.Counter()
for line in sys.stdin:
    e = json.loads(line)
    text = e.get('text','')
    if not text: continue
    try:
        inner = json.loads(text)
        kinds[inner.get('kind','')] += 1
    except:
        kinds['<parse_error>'] += 1
for k, v in kinds.most_common():
    print(f'{v:4d} {k}')
"
```

## Common Failure Patterns

### 1. Serf Process Dies Immediately

**Symptom**: Node goes to `failed` with no events or only SESSION_START.

**Check**:
```bash
# Is serf on PATH?
which serf

# Does serf have required env vars?
# The toil server must be started with LLM API keys in its environment.
# Source the .env file before starting:
set -a && source /path/to/serf/.env && set +a
```

**Typical causes**:
- `serf` binary not on PATH (add serf's directory to PATH when starting toil)
- Missing LLM API keys (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, etc.)
- Invalid CLI flags (e.g., removed `--no-auto-verify`)

### 2. Agent Stuck in Read Loop

**Symptom**: Round count climbing, but agent only calls `glob`/`read_file`/`task_list(view)` repeatedly. Zero text output.

**Diagnosis**: Extract the conversation flow (see above). Look for:
- Same tool call repeated 3+ times with identical arguments
- `ASSISTANT_TEXT_END` events with empty `full_text`
- `STEERING_INJECTED` events (serf tries to break loops, but some models ignore steering)

**Typical causes**:
- Model quality — some models (e.g., gpt-5.2-codex) degenerate into tool-call loops
- Insufficient context in the workspace (empty worktree with no scaffold)

**Fix**: Switch to a different model in `definitions/runners/serf.yaml`:
```yaml
args:
  - --model
  - ${SERF_PROVIDER:-openai}/${SERF_MODEL:-gpt-5.4}  # or anthropic/claude-sonnet-4-5-20250514
```
Then restart the toil server.

**Interrogating a stuck session**: You can resume the session with a different model to ask it what went wrong:
```bash
# Find the session ID from events
grep 'SESSION_START' runs/<run-id>/events.jsonl | python3 -c "
import sys, json
line = next(sys.stdin)
inner = json.loads(json.loads(line).get('text',''))
print(inner.get('session_id',''))
"

# Find the state dir (XDG_STATE_HOME/serf/projects/<hash>/)
find ~/.local/state/serf -name "<session-id>.meta.json"

# Resume with a better model and ask what happened
serf --model openai/gpt-5.4 \
  --state-dir <state-dir> \
  --resume-with <session-id> \
  --max-rounds 10 \
  "STOP. Do not use any tools. Just explain: what were you trying to do and why did you get stuck in a loop?"
```

This technique yielded a clear postmortem from gpt-5.4: "I failed to transition from inspection to delivery. I kept checking state rather than producing the deliverable. That was my mistake, not a missing input or an ambiguous spec."

### 3. Worktree Creation Fails

**Symptom**: `create_worktree` node fails with "can't create work tree".

**Diagnosis**:
```bash
# Check the bare repo exists and its origin is valid
ls repos/
git -C repos/<project-id>.git remote get-url origin

# Check the origin path still exists
ls -la <origin-path>
```

**Typical causes**:
- Stale bare repo with origin pointing to deleted temp directory
- Project ID collision (two different local paths mapping to same ID)

**Fix**:
- Remove stale bare repo: `rm -rf repos/<project-id>.git`
- If collision: the `slugFromLocalPath` function now uses full resolved paths (e.g., `private-tmp-sample-app` instead of just `sample-app`)

### 4. Node Timeout

**Symptom**: Node fails after exactly `timeout_sec` (default 1200s = 20min).

**Check**: Look at `ROUND_TIMINGS` events to see if the agent was making progress or stuck.

**Fix**: Increase timeout in runner definition, or investigate why the agent is slow.

### 5. context:fresh Node Loses Prompt on Second Attempt

**Symptom**: A `context: fresh` node on its second or later attempt (attempts > 1) behaves as if it has no role prompt — it receives inputs and output format instructions but zero role-specific guidance. The agent does something completely unrelated to its role.

**Root cause**: `execute.go` used `firstRun = stateNode.Attempts == 0` to decide whether to include the node prompt via `selectPrompts()`. For `context: fresh`, the session is always new (no prior conversation), but `firstRun` was false on attempt 2+. `selectPrompts(false, "", nodePrompt, "", false)` returned `("", "")` — no prompt at all.

**Example**: plan_reviewer (context: fresh, attempt 2) received only raw inputs + output format with zero role instructions. With `spec_slice: "Implement everything described in the spec"` in the inputs, it built the entire calculator app instead of reviewing the plan.

**Fix** (applied 2026-04-17): Changed `selectPrompts` call to use `!resume` instead of `firstRun`. `resolveSession` returns `resume=false` for fresh context (new session every time), so `!resume=true` → prompt included regardless of attempt count. Tests in `context_mode_test.go`.

**Diagnostic**: When investigating unexpected node behavior, interview the serf session (see "Interviewing Serf Sessions" above) and check whether the agent received its role prompt. If the transcript shows the agent acting without role context, check the `selectPrompts` logic for the node's context mode.

**Affected**: Any node with `context: fresh` that runs more than once (loop retries, retry policies).

**Found**: 2026-04-17 (run `timber-jet-banyan`, node `plan_reviewer` in `plan_and_build` workflow)

## Server Management

```bash
# Start with serf on PATH and API keys sourced
set -a && source /path/to/serf/.env && set +a
PATH="/path/to/serf:$PATH" bin/toil serve --addr :8080

# Find running server
ps aux | grep 'bin/toil serve'
lsof -p <pid> | grep cwd   # shows working directory

# Restart (definitions are loaded at startup, not hot-reloaded)
kill $(pgrep -f 'bin/toil serve')
# Then start again as above

# Check server logs
tail -f /tmp/toil-serve.log
```

**Important**: The server must be restarted after changing:
- Runner definitions (`definitions/runners/*.yaml`)
- Workflow definitions (`definitions/workflows/*.yaml`)
- Environment variables (API keys, model overrides)

## Interviewing Serf Sessions

When a node behaves unexpectedly, you can resume its serf session and ask the agent directly what happened and why.

### Finding the session ID

1. Trace the run hierarchy to find the node's run:
   ```
   runs/<run-id>/state.json → nodes.<node>.data.child_run → runs/<child-run>/state.json → ...
   ```
   Subworkflows nest: implement_spec → build_component → implement_task. Follow `child_run` at each level.

2. The target node's `session_id` field in state.json is the serf session ID.

### Session files

Sessions are stored at:
```
~/.local/state/serf/projects/<sha256(workDir)[:16]>/sessions/<session_id>.{meta.json,transcript.jsonl}
```

`meta.json` contains the working directory, model, and config. `transcript.jsonl` contains the full conversation with all tool calls.

### Reading a transcript

```python
import json
with open('...transcript.jsonl') as f:
    for line in f:
        entry = json.loads(line)
        if entry.get('kind') == 'entry':
            turn = entry['turn']
            for block in turn['message']['content']:
                if block['kind'] == 'tool_call':
                    tc = block['tool_call']
                    print(f"{tc['name']}({tc['arguments'][:200]})")
```

### Resuming a session

```bash
cd <any-dir> && \
set -a && source /path/to/serf/.env && set +a && \
serf --model openai/gpt-5.4 \
  --reasoning-effort low \
  --state-dir ~/.local/state/serf/projects/<project-hash> \
  --resume <session_id> \
  "Your interview question here"
```

Key flags:
- `--state-dir` points to the project-level dir containing `sessions/` (NOT sessions dir itself)
- `--resume <id>` resumes with full conversation history
- Working directory doesn't need to match the original (worktrees may be cleaned up)
- Pass a question as the task argument; without one, serf defaults to "Continue where you left off"

### Gotcha: attempt count

A node with `attempts: 2` ran twice — each attempt may have a different session ID. state.json only stores the **last** session ID. To find earlier sessions, check `events.jsonl` for `runner_completed` events, or list all sessions in the project state dir and match by timestamp.

## Navigating the Run Hierarchy

Toil runs form parent-child trees. An `implement_spec` run spawns sub-runs:

```
implement_spec (spruce-velvet-tundra)
  -> scope_components (shell node)
  -> ensure_repo (shell node)
  -> build_component (ForEach, concurrent) [per component]
       -> create_worktree (shell node)
       -> plan_tasks (serf node, outputs structured plan)
       -> review_plan (serf node)
       -> implement_tasks (ForEach, DAG-scheduled by task dependencies)
       -> commit_component (shell node, safety commit for stragglers)
  -> integrate_components (ForEach, sequential by component dependencies) [per component]
       -> merge_branch (shell node)
       -> resolve_conflict (serf node, only on conflict)
       -> export_tree (shell node)
       -> verify_integration (sub-workflow)
```

To trace a failure up the chain:
```bash
# Find parent run
curl -s http://localhost:8080/runs/<run-id> | python3 -c "
import sys, json
data = json.load(sys.stdin)
print(f'Parent: {data.get(\"parent_run\",\"none\")}')
print(f'Workflow: {data[\"workflow_id\"]}')
"
```

## Environment Variable Overrides

The serf runner supports env-var overrides for all model parameters:

| Variable | Default | Purpose |
|----------|---------|---------|
| `SERF_MODEL` | `gpt-5.4` | LLM model ID |
| `SERF_PROVIDER` | `openai` | LLM provider |
| `SERF_AGENT` | `worker` | Serf agent profile |
| `SERF_REASONING_EFFORT` | `medium` | Reasoning effort level |

Set these before starting the toil server to change behavior for all runs.

## Serf Session Limits

Serf has built-in limits that affect run behavior:

| Limit | Default | Purpose |
|-------|---------|---------|
| `MaxToolRoundsPerInput` | 200 | Max tool call rounds before forced stop |
| `MaxTurns` | (varies by agent) | Max user inputs per session |
| Timeout | 1200s (runner config) | Hard timeout from toil side |

If a model is stuck looping, it will burn through all 200 rounds before the turn limit kicks in — or hit the 1200s timeout first. Watch the round count in `ROUND_TIMINGS` events.
