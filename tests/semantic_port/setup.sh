#!/usr/bin/env bash
set -euo pipefail

# Checkpoint: e3c3c34 — "fix: make codex subprocess stream limit configurable"
# The 12 commits after it include a mix of:
#   - 5 code fixes (exercise the implement/port path)
#   - 6 docs/trivial commits (exercise the acknowledge/skip path)
#   - 1 release commit (exercise triage logic)
CHECKPOINT="e3c3c34"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

OAG_DIR="${OAG_DIR:-/tmp/openai-agents-go}"
UPSTREAM_DIR="${UPSTREAM_DIR:-/tmp/oap-upstream}"
# Set OAG_REPO to your Go port fixture repo (the codebase semantic_port ports into).
OAG_REPO="${OAG_REPO:-git@github.com:YOUR_ORG/your-go-port-fixture.git}"

echo "=== Cloning/resetting Go port fixture ==="
if [ ! -d "$OAG_DIR" ]; then
  git clone "$OAG_REPO" "$OAG_DIR"
else
  cd "$OAG_DIR"
  git checkout main
  git reset --hard origin/main
  git clean -fd
fi

echo "=== Cloning/resetting upstream ==="
if [ ! -d "$UPSTREAM_DIR" ]; then
  git clone https://github.com/openai/openai-agents-python "$UPSTREAM_DIR"
fi
cd "$UPSTREAM_DIR"
git checkout "$CHECKPOINT"

echo "=== Building toil + semantic_port ==="
cd "$REPO_ROOT"
make build

echo "=== Ready ==="
echo "OAG_DIR=$OAG_DIR"
echo "UPSTREAM_DIR=$UPSTREAM_DIR"
echo ""
echo "Run eval with:"
echo "  OAG_DIR=$OAG_DIR UPSTREAM_DIR=$UPSTREAM_DIR go run ./cmd/toil eval tests/eval/semantic_port.yaml"
