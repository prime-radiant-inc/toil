#!/usr/bin/env bash
set -eu
command -v node >/dev/null 2>&1 || { echo "node is required to scope components" >&2; exit 1; }
node "$TOIL_WORKFLOW_SCRIPT_DIR/scope_components.js"
