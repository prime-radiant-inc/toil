#!/usr/bin/env bash
set -euo pipefail

WORKTREE_NAME="${COMPONENT_ID}-${PARENT_RUN_ID}"

# Attempt merge with --keep-on-conflict so the temp worktree survives
# for the conflict resolver if needed.
#
# Flags MUST come before the positional <name>: Go's stdlib flag parser
# stops at the first non-flag argument, so `tgwm merge NAME --flag` would
# silently ignore --flag.
MERGE_STDERR=$(mktemp)
if tgwm merge --repo "${REPO}" --keep-on-conflict "${WORKTREE_NAME}" 2>"$MERGE_STDERR"; then
  rm -f "$MERGE_STDERR"
  cat <<ENDJSON
{
  "decision": "merged",
  "message": "Clean merge of ${WORKTREE_NAME}",
  "data": {},
  "artifacts": []
}
ENDJSON
else
  # Check if this is a merge conflict or a different tgwm error.
  MERGE_WORKTREE=$(grep -o 'Conflict worktree: [^ ]*' "$MERGE_STDERR" | cut -d' ' -f3 | head -1 || true)
  if [[ -z "$MERGE_WORKTREE" ]]; then
    # Not a conflict — surface the actual tgwm error so the node fails usefully.
    cat "$MERGE_STDERR" >&2
    rm -f "$MERGE_STDERR"
    exit 1
  fi
  rm -f "$MERGE_STDERR"
  cat <<ENDJSON
{
  "decision": "conflict",
  "message": "Merge conflict in ${WORKTREE_NAME}. Resolve in ${MERGE_WORKTREE}.",
  "data": {"merge_worktree": "${MERGE_WORKTREE}"},
  "artifacts": []
}
ENDJSON
fi
