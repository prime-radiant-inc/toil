#!/usr/bin/env bash
set -euo pipefail

# Atomically remove the task worktree directory and delete the task
# branch. Runs only on success paths (after merge). Failed task
# worktrees are preserved so they can be investigated — the parent
# build_component's some_failed edge re-plans via plan_tasks without
# touching the task tree.

TASK_WORKTREE_NAME="${COMPONENT_ID}-${PARENT_RUN_ID}-${TASK_ID}"

tgwm worktree destroy --repo "${REPO}" "${TASK_WORKTREE_NAME}"

echo "Destroyed task worktree ${TASK_WORKTREE_NAME}"
