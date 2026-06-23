#!/usr/bin/env bash
set -eu
if ! echo "${PRODUCT_SLUG}" | grep -qE '^[a-zA-Z0-9._-]+$'; then
  echo "ERROR: Invalid PRODUCT_SLUG: ${PRODUCT_SLUG}" >&2
  exit 1
fi

BRANCH="toil/${PRODUCT_SLUG}/${RUN_ID}"
# Extract owner/repo from URL (e.g., https://github.com/acme/my-app)
OWNER_REPO=$(echo "${REPO_URL}" | sed 's|https://github.com/||' | sed 's|\.git$||')

# Validate OWNER_REPO format (must be exactly owner/repo)
if ! echo "${OWNER_REPO}" | grep -qE '^[a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+$'; then
  echo "ERROR: Invalid repo URL format: ${REPO_URL}" >&2
  exit 1
fi

# Helper: check HTTP status, fail on non-2xx
check_status() {
  local status="$1" context="$2" body="$3"
  if [ "${status}" -lt 200 ] || [ "${status}" -ge 300 ]; then
    echo "ERROR: ${context} returned HTTP ${status}" >&2
    echo "${body}" >&2
    return 1
  fi
}

# Check if remote main exists (needed for first-run handling)
MAIN_STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: token ${GITHUB_TOKEN}" \
  "https://api.github.com/repos/${OWNER_REPO}/branches/main")

if [ "${MAIN_STATUS}" = "404" ]; then
  # First run: no main branch yet. Push branch directly as main.
  git -c credential.helper='!f() { echo "username=x-access-token"; echo "password=${GITHUB_TOKEN}"; }; f' \
    push origin "main:refs/heads/main"
  MERGE_SHA=$(git rev-parse HEAD)

  # Delete the delivery branch (main now has the same content)
  curl -s -X DELETE \
    -H "Authorization: token ${GITHUB_TOKEN}" \
    "https://api.github.com/repos/${OWNER_REPO}/git/refs/heads/${BRANCH}" || true

  jq -n --arg sha "${MERGE_SHA}" \
    '{decision:"done", data:{pr_url:"", merge_commit_sha:$sha}}'

elif [ "${MERGE_MODE}" = "pr" ]; then
  # Create PR via GitHub API — use jq to build JSON safely
  PR_BODY=$(jq -n \
    --arg title "Toil delivery: ${PRODUCT_SLUG} (${RUN_ID})" \
    --arg head "${BRANCH}" \
    --arg base "main" \
    --arg body "Automated delivery from Toil run ${RUN_ID}" \
    '{title: $title, head: $head, base: $base, body: $body}')

  RESPONSE=$(mktemp)
  PR_STATUS=$(curl -s -w "%{http_code}" -o "${RESPONSE}" \
    -X POST \
    -H "Authorization: token ${GITHUB_TOKEN}" \
    -H "Accept: application/vnd.github+json" \
    "https://api.github.com/repos/${OWNER_REPO}/pulls" \
    -d "${PR_BODY}")
  PR_RESULT=$(cat "${RESPONSE}")
  rm -f "${RESPONSE}"

  check_status "${PR_STATUS}" "Create PR" "${PR_RESULT}"

  PR_URL=$(echo "${PR_RESULT}" | jq -r '.html_url')
  jq -n --arg url "${PR_URL}" \
    '{decision:"done", data:{pr_url:$url, merge_commit_sha:""}}'

else
  # Auto-merge via GitHub merges API — use jq to build JSON safely
  MERGE_BODY=$(jq -n \
    --arg base "main" \
    --arg head "${BRANCH}" \
    --arg msg "Merge ${BRANCH}" \
    '{base: $base, head: $head, commit_message: $msg}')

  RESPONSE=$(mktemp)
  MERGE_STATUS=$(curl -s -w "%{http_code}" -o "${RESPONSE}" \
    -X POST \
    -H "Authorization: token ${GITHUB_TOKEN}" \
    -H "Accept: application/vnd.github+json" \
    "https://api.github.com/repos/${OWNER_REPO}/merges" \
    -d "${MERGE_BODY}")
  MERGE_RESULT=$(cat "${RESPONSE}")
  rm -f "${RESPONSE}"

  # 409 = merge conflict. Roll back the branch and fail with clear error.
  if [ "${MERGE_STATUS}" = "409" ]; then
    echo "ERROR: Merge conflict — cannot auto-merge ${BRANCH} into main." >&2
    echo "Rolling back: deleting remote branch ${BRANCH}" >&2
    curl -s -X DELETE \
      -H "Authorization: token ${GITHUB_TOKEN}" \
      "https://api.github.com/repos/${OWNER_REPO}/git/refs/heads/${BRANCH}" || true
    echo "The branch has been cleaned up. Resolve conflicts manually or adjust the code and re-run." >&2
    exit 1
  fi

  check_status "${MERGE_STATUS}" "Merge branch" "${MERGE_RESULT}"

  MERGE_SHA=$(echo "${MERGE_RESULT}" | jq -r '.sha')

  # Verify merge SHA is valid before claiming success
  if [ -z "${MERGE_SHA}" ] || [ "${MERGE_SHA}" = "null" ]; then
    echo "ERROR: Merge returned success but no SHA in response" >&2
    echo "${MERGE_RESULT}" >&2
    exit 1
  fi

  # Delete the delivery branch
  curl -s -X DELETE \
    -H "Authorization: token ${GITHUB_TOKEN}" \
    "https://api.github.com/repos/${OWNER_REPO}/git/refs/heads/${BRANCH}" || true

  jq -n --arg sha "${MERGE_SHA}" \
    '{decision:"done", data:{pr_url:"", merge_commit_sha:$sha}}'
fi
