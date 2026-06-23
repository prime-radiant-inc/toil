#!/usr/bin/env bash
set -euo pipefail

# Push the merged default branch back to the source repo so the user's
# checkout reflects the new state, then materialize a fresh export for
# downstream verification.
#
# Component-worktree cleanup has its own node (cleanup_component_worktree)
# so this script's responsibility is just push + export.

tgwm push --repo "${REPO}" "${PROJECT_DIR}" >&2

EXPORT_DIR="${TOIL_CURRENT_WORKFLOW_DIR}/export"
tgwm export --repo "${REPO}" "$EXPORT_DIR" >&2

echo "$EXPORT_DIR"
