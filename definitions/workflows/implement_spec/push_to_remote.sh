#!/usr/bin/env bash
set -eu
if ! echo "${PRODUCT_SLUG}" | grep -qE '^[a-zA-Z0-9._-]+$'; then
  echo "ERROR: Invalid PRODUCT_SLUG: ${PRODUCT_SLUG}" >&2
  exit 1
fi
git remote add origin "${REPO_URL}" 2>/dev/null || git remote set-url origin "${REPO_URL}"
BRANCH="toil/${PRODUCT_SLUG}/${RUN_ID}"
git -c credential.helper='!f() { echo "username=x-access-token"; echo "password=${GITHUB_TOKEN}"; }; f' \
  push origin "main:refs/heads/${BRANCH}"
jq -n --arg branch "${BRANCH}" '{decision:"done", data:{branch:$branch}}'
