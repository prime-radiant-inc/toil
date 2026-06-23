#!/usr/bin/env bash
set -eu

# Shell runs in PROJECT_DIR (workspace_defaults.path: ${PROJECT_DIR}).
# The engine creates the directory if it doesn't exist.
if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  if [ -n "${REPO_URL:-}" ] && [ "${REPO_URL}" != "null" ]; then
    # Clone the existing GitHub repo. Fail fast if it doesn't exist.
    git -c credential.helper='!f() { echo "username=x-access-token"; echo "password=${GITHUB_TOKEN}"; }; f' \
      clone "${REPO_URL}" . || { echo "ERROR: Failed to clone ${REPO_URL} — does the repository exist?" >&2; exit 1; }
  else
    git init -b main >/dev/null 2>&1 || git init >/dev/null 2>&1
  fi
  git config user.name >/dev/null 2>&1 || git config user.name "${TOIL_GIT_AUTHOR_NAME:-Toil Local Bot}"
  git config user.email >/dev/null 2>&1 || git config user.email "${TOIL_GIT_AUTHOR_EMAIL:-toil-local@example.com}"
fi
# Allow pushes to the checked-out branch so tgwm can sync built code back.
git config receive.denyCurrentBranch updateInstead

# Auto-heal common interrupted git operations from prior runs.
if [ -f .git/MERGE_HEAD ]; then
  echo "Repository has an in-progress merge; aborting it." >&2
  git merge --abort >/dev/null 2>&1 || { echo "Failed to abort in-progress merge." >&2; exit 1; }
fi
if [ -f .git/CHERRY_PICK_HEAD ]; then
  echo "Repository has an in-progress cherry-pick; aborting it." >&2
  git cherry-pick --abort >/dev/null 2>&1 || { echo "Failed to abort in-progress cherry-pick." >&2; exit 1; }
fi
if [ -f .git/REVERT_HEAD ]; then
  echo "Repository has an in-progress revert; aborting it." >&2
  git revert --abort >/dev/null 2>&1 || { echo "Failed to abort in-progress revert." >&2; exit 1; }
fi
if [ -d .git/rebase-apply ] || [ -d .git/rebase-merge ]; then
  echo "Repository has an in-progress rebase; aborting it." >&2
  git rebase --abort >/dev/null 2>&1 || { echo "Failed to abort in-progress rebase." >&2; exit 1; }
fi

if ! git rev-parse --verify HEAD >/dev/null 2>&1; then
  touch .gitignore
  add_ignore() { grep -qxF "$1" .gitignore || printf '%s\n' "$1" >> .gitignore; }
  add_ignore ".toil/"
  add_ignore ".venv/"
  add_ignore "venv/"
  add_ignore "__pycache__/"
  add_ignore "*.pyc"
  add_ignore ".pytest_cache/"
  add_ignore "*.egg-info/"
  add_ignore "data/posts.json"
  add_ignore ".DS_Store"

  git add -A
  if git diff --cached --quiet; then
    git commit --allow-empty -m "Initialize repository for Toil workflow" >/dev/null
  else
    git commit -m "Initialize repository for Toil workflow" >/dev/null
  fi
fi

# Initialize the bare repo once at the top of implement_spec. tgwm init is
# idempotent (fetches if exists), and capturing the path here lets
# every downstream subworkflow consume bare_repo as a structured input
# instead of re-resolving it from PROJECT_DIR via slug guessing.
REPO=$(tgwm init --source "${PROJECT_DIR}")

cat <<JSON
{
  "decision": "ready",
  "message": "Repository ready",
  "data": {"bare_repo": "${REPO}"},
  "artifacts": []
}
JSON
