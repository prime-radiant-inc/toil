#!/usr/bin/env bash
set -euo pipefail

# REPO comes from the implement_spec workflow's ensure_repo node, which is
# the sole source of truth for the bare repo path. No tgwm init here —
# ensure_repo already handled it.

WORKTREE_NAME="${COMPONENT_ID}-${PARENT_RUN_ID}"
tgwm worktree create --repo "${REPO}" "${WORKTREE_NAME}"
