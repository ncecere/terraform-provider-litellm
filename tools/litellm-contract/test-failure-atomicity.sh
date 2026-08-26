#!/bin/sh
set -eu
repo_root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
if [ -z "${LITELLM_SOURCE:-}" ]; then
  echo 'LITELLM_SOURCE is required for the failure-atomicity reproduction test' >&2
  exit 2
fi
files='openapi.json internal/contract/supplemental-routes.json internal/contract/manifest.json internal/contractapi/testdata/provider-operations.golden.json'
before=$(mktemp "${TMPDIR:-/tmp}/contract-before.XXXXXX")
after=$(mktemp "${TMPDIR:-/tmp}/contract-after.XXXXXX")
trap 'rm -f "$before" "$after"' EXIT HUP INT TERM
(
  cd "$repo_root"
  for file in $files; do shasum -a 256 "$file"; done
) > "$before"
set +e
CONTRACT_TEST_FAIL_BEFORE_REPLACE=1 sh "$repo_root/tools/litellm-contract/update.sh" update
status=$?
set -e
if [ "$status" -ne 97 ]; then
  echo "expected injected update failure 97, got $status" >&2
  exit 1
fi
(
  cd "$repo_root"
  for file in $files; do shasum -a 256 "$file"; done
) > "$after"
cmp "$before" "$after"
echo 'contract update failure left all generated artifacts untouched'
