#!/usr/bin/env bash
set -euo pipefail

# Note: build_component's workflow-level PROJECT_DIR is the source project.
# This node's project_dir INPUT is the worktree path (from create_worktree
# output). We cd to the node-level PROJECT_DIR (the worktree) to commit.
#
# workspace_defaults.path can only use env var expansion, not node outputs,
# so the workspace lands in the source. The cd is required here.
cd "${PROJECT_DIR}"

# Safety commit: ensure all changes are committed before the build phase ends.
# implement_task agents should commit as they go, but this catches stragglers.
git add -A
if git diff --cached --quiet; then
  echo "No changes to commit for ${COMPONENT_ID}"
  exit 0
fi
git commit -m "feat(component): ${COMPONENT_ID}"
git rev-parse --short HEAD
