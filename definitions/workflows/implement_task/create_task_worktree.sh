#!/usr/bin/env bash
set -euo pipefail

# Per-task worktree creation. Each implement_task iteration gets its
# own worktree on its own branch so concurrent tasks don't share a
# `.git` or build cache. The branch is based on the component branch,
# so it starts with every dependency's committed work already in place.
#
# Concurrent creates against the same bare repo serialize internally
# via tgwm's per-bare-repo flock — no shell-level locking needed.

TASK_WORKTREE_NAME="${COMPONENT_ID}-${PARENT_RUN_ID}-${TASK_ID}"
COMPONENT_BRANCH="run/${COMPONENT_ID}-${PARENT_RUN_ID}"

# --force ensures re-plans succeed even when a previous attempt with
# the same task id left a worktree+branch behind. The plan_tasks prompt
# nudges toward fresh task IDs on re-plan (so the previous attempt's
# branch is preserved for inspection), but --force is the safety
# net for cases where plan_tasks reuses an ID anyway.
tgwm worktree create --repo "${REPO}" --base "${COMPONENT_BRANCH}" --force "${TASK_WORKTREE_NAME}"
