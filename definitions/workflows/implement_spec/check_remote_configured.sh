#!/usr/bin/env bash
set -eu
if [ -z "${REPO_URL:-}" ] || [ "${REPO_URL}" = "null" ]; then
  jq -n '{decision:"skip", message:"No remote configured, skipping push", data:{}}'
else
  jq -n --arg url "${REPO_URL}" '{decision:"push", message:("Pushing to " + $url), data:{}}'
fi
