#!/usr/bin/env bash
# Decide whether a commit may be released: its newest "simpleota/backend-e2e"
# status from the reporter must be "success".
#
#   gh api repos/OWNER/REPO/commits/SHA/statuses | scripts/check-backend-e2e.sh REPORTER
#
# The statuses come on stdin, newest first, as the GitHub API lists them.
# REPORTER is the login of the account that reports backend e2e results.
# Statuses from anyone else are ignored. With no reporter, no status, or a
# newest status that is not "success", it exits 1.
set -euo pipefail

CONTEXT="simpleota/backend-e2e"
reporter="${1:-}"
if [[ -z "$reporter" ]]; then
  echo "check-backend-e2e: no reporter is configured, so no result can be trusted" >&2
  exit 1
fi

state="$(jq -r --arg c "$CONTEXT" --arg r "$reporter" \
  'map(select(.context == $c and .creator.login == $r)) | first | .state // "missing"')"
if [[ "$state" != "success" ]]; then
  echo "check-backend-e2e: $CONTEXT from $reporter is $state, not success" >&2
  exit 1
fi
echo "check-backend-e2e: $CONTEXT from $reporter is success"
