#!/usr/bin/env bash
set -euo pipefail

# Remove the component worktree directory now that the component branch
# is merged into the default branch and the build artifacts are about
# to be exported. The branch survives in the bare repo as the record
# of what was built.
#
# Lives in its own node so the responsibility is visible: future
# refactors of export_tree.sh won't accidentally drop the cleanup
# and leak component worktrees indefinitely.

WORKTREE_NAME="${COMPONENT_ID}-${PARENT_RUN_ID}"
tgwm cleanup --repo "${REPO}" "${WORKTREE_NAME}" >&2
echo "Cleaned up component worktree ${WORKTREE_NAME}"
