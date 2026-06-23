#!/usr/bin/env bash
set -euo pipefail

# After conflict resolution, the temp merge worktree is still registered
# with the bare repo. Remove it so subsequent merges can check out the
# target branch without "already checked out" errors.
if [ -n "${MERGE_WORKTREE:-}" ] && [ -d "${MERGE_WORKTREE}" ]; then
  git -C "${REPO}" worktree remove --force "${MERGE_WORKTREE}" 2>/dev/null || true
  rm -rf "$(dirname "${MERGE_WORKTREE}")" 2>/dev/null || true
fi
