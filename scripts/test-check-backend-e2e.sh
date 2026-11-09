#!/usr/bin/env bash
# Tests scripts/check-backend-e2e.sh with fixed status lists.
set -euo pipefail

check="$(dirname "$0")/check-backend-e2e.sh"
failures=0

expect() { # expect <want exit> <reporter> <statuses json> <case>
  local got=0
  printf '%s' "$3" | "$check" "$2" >/dev/null 2>&1 || got=$?
  if [[ "$got" != "$1" ]]; then
    echo "FAIL: $4: exit $got, want $1"
    failures=$((failures + 1))
  fi
}

ok='{"context":"simpleota/backend-e2e","state":"success","creator":{"login":"reporter[bot]"}}'
failed='{"context":"simpleota/backend-e2e","state":"failure","creator":{"login":"reporter[bot]"}}'
pending='{"context":"simpleota/backend-e2e","state":"pending","creator":{"login":"reporter[bot]"}}'
forged='{"context":"simpleota/backend-e2e","state":"success","creator":{"login":"someone"}}'
other='{"context":"ci/other","state":"success","creator":{"login":"reporter[bot]"}}'

expect 0 "reporter[bot]" "[$ok]" "success"
expect 0 "reporter[bot]" "[$forged,$ok]" "a newer status from someone else is ignored"
expect 1 "reporter[bot]" "[]" "no status"
expect 1 "reporter[bot]" "[$other]" "only another context"
expect 1 "reporter[bot]" "[$forged]" "success only from someone else"
expect 1 "reporter[bot]" "[$failed,$ok]" "the newest result failed"
expect 1 "reporter[bot]" "[$pending,$ok]" "the newest result is pending"
expect 1 "" "[$ok]" "no reporter configured"

if [[ "$failures" -gt 0 ]]; then
  exit 1
fi
echo "check-backend-e2e: all cases pass"
