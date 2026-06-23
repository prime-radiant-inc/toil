#!/usr/bin/env bash
set -eu
if ! echo "${PRODUCT_SLUG}" | grep -qE '^[a-zA-Z0-9._-]+$'; then
  echo "ERROR: Invalid PRODUCT_SLUG: ${PRODUCT_SLUG}" >&2
  exit 1
fi
BRANCH="toil/${PRODUCT_SLUG}/${RUN_ID}"
OWNER_REPO=$(echo "${REPO_URL}" | sed 's|https://github.com/||' | sed 's|\.git$||')
curl -s -X DELETE \
  -H "Authorization: token ${GITHUB_TOKEN}" \
  "https://api.github.com/repos/${OWNER_REPO}/git/refs/heads/${BRANCH}" || true
