#!/usr/bin/env bash
set -euo pipefail

# Merge a task branch into its component branch. Concurrent merges
# against the same bare repo serialize via tgwm's per-bare-repo flock.
#
# v1 conflict handling: conflicts fail the task via `exit 1`. The merge
# worktree path is preserved on disk (via --keep-on-conflict) and
# surfaced in stderr so an operator can investigate. build_component's
# `implement_tasks → plan_tasks (some_failed)` edge re-plans via plan_tasks.

TASK_WORKTREE_NAME="${COMPONENT_ID}-${PARENT_RUN_ID}-${TASK_ID}"
COMPONENT_BRANCH="run/${COMPONENT_ID}-${PARENT_RUN_ID}"

MERGE_STDERR=$(mktemp)
if tgwm merge --repo "${REPO}" --target "${COMPONENT_BRANCH}" --keep-on-conflict "${TASK_WORKTREE_NAME}" 2>"${MERGE_STDERR}"; then
  rm -f "${MERGE_STDERR}"
  cat <<JSON
{
  "decision": "merged",
  "message": "Merged task ${TASK_ID} into ${COMPONENT_BRANCH}",
  "data": {},
  "artifacts": []
}
JSON
else
  MERGE_WORKTREE=$(grep -o 'Conflict worktree: [^ ]*' "${MERGE_STDERR}" | cut -d' ' -f3 | head -1 || true)
  echo "Task ${TASK_ID} merge into ${COMPONENT_BRANCH} failed." >&2
  if [[ -n "${MERGE_WORKTREE}" ]]; then
    echo "Conflict worktree preserved at: ${MERGE_WORKTREE}" >&2
  fi
  cat "${MERGE_STDERR}" >&2
  rm -f "${MERGE_STDERR}"
  exit 1
fi
