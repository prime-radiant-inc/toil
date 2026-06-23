# v7 Deploy Drain Runbook

## When to use

Before deploying v7 to an environment that has in-flight runs (state.json
files in `<runs-dir>/<run-id>/`). v7 changes persisted state shapes:

- `JoinState` → `map[string]*JoinNodeState`
- `NodeState` gains `LoopIterations`, `LastRoutingDecision`, `LastRoutingAt`
- `InputRef.Optional` deleted

Pre-v7 `state.json` files cannot be resumed by the v7 engine. Completing or
cancelling them before deploy is the only safe path.

---

## Procedure

### A. Drain (preserve completed work)

This path lets in-flight runs finish before the deploy lands.

**1. Pause new run creation.**

```bash
bin/toil pause
```

The daemon immediately rejects `POST /runs` with HTTP 503 + `Retry-After: 60`.
The `.paused` marker is written to `<runs-dir>/.paused`.

**2. Inspect in-flight runs.**

```bash
bin/toil drain --dry-run
```

Lists every run with status `running` or `paused` with ID, workflow, status,
and started_at. Does not pause the daemon or cancel anything.

**3. Wait for natural completion OR cancel.**

_Wait_ — leave the daemon up; in-flight runs continue to completion. The
drain subcommand will poll until all finish:

```bash
bin/toil drain --wait
```

_Cancel all_ — mark every in-flight run as `cancelled` now:

```bash
bin/toil drain --force-cancel
```

_Interactive_ — `bin/toil drain` with no flags prompts for a choice.

**4. Deploy the new engine and workflow YAML files.**

**5. Resume new run creation.**

```bash
bin/toil resume
```

Removes `<runs-dir>/.paused`. The daemon begins accepting new runs immediately
on the next request.

---

### B. Accepted loss (personal-use environments)

If in-flight runs are not worth preserving:

1. Stop the daemon:
   ```bash
   pkill -f 'bin/toil serve'
   ```

2. (Optional) Delete or archive old run directories — they become inert once
   the daemon is stopped:
   ```bash
   rm -rf ~/.local/share/toil/runs/*
   ```

3. Deploy the new engine + workflow YAML files.

4. Restart the daemon:
   ```bash
   make serve   # or bin/toil serve --addr :8080 --daemon
   ```

Old runs will not resume; new runs start with the v7 state shape.

---

## CLI reference

| Command | Effect |
|---|---|
| `bin/toil pause` | Touch `<runs-dir>/.paused`. Daemon rejects new run creation. |
| `bin/toil resume` | Remove `<runs-dir>/.paused`. Daemon resumes accepting runs. |
| `bin/toil drain --dry-run` | List in-flight runs; do not pause or cancel anything. |
| `bin/toil drain --wait` | Pause + wait for natural completion (polls every 10 s). |
| `bin/toil drain --force-cancel` | Pause + cancel all in-flight runs immediately. |
| `bin/toil drain` | Pause + interactive prompt (w/c/q). |

---

## Notes

**Marker file.** The `.paused` file lives at `<runs-dir>/.paused`. Manual
`touch`/`rm` work identically to `bin/toil pause`/`resume`. The server checks
the marker on every `createRun` call — there is no caching — so removal takes
effect immediately.

**`runs-dir` location.** Defaults to `$XDG_DATA_HOME/toil/runs` (or
`~/.local/share/toil/runs`). Override with `$TOIL_RUNS_DIR`.

**Race window.** The pause check happens after JSON decoding and before any
other work. A request that is already mid-flight (past the check) at the
moment the marker is created will proceed to completion. This is the expected
behavior — the marker governs new run creation, not in-flight work.

**`resume <run_id>` still works.** The existing `bin/toil resume <run_id>`
command (resume a specific paused or waiting run via the API) is unchanged.
Calling `bin/toil resume` with no argument is the daemon-resume operation.

---

## Confirming the state-shape mismatch (sanity check)

After deploying v7, manually attempt to resume one old pre-v7 run:

```bash
bin/toil resume <old-run-id>
```

**Expected:** a clean error indicating the run is in a terminal or incompatible
state. If the engine silently succeeds or produces wrong behavior, the
state-compat story has a gap that needs investigation before serving traffic.
