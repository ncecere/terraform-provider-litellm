#!/bin/sh
set -eu
repo_root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/litellm-provider-binary.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM

if go list -deps "$repo_root" | grep -q '/internal/contractapi$'; then
  echo 'provider runtime unexpectedly imports contract verifier' >&2
  exit 1
fi
(
  cd "$repo_root"
  go build -o "$work/provider" .
)
for marker in \
  d8f71d7bdbd7c9873d98293f83d64c6db72847e6 \
  a7cc57875c67de85bbae0f82b834f31fc9d0c029073ef29e0883787a31a985e8 \
  '/v1/mcp/server/{server_id}/oauth-user-credential/status' \
  'include_in_schema=false upstream route used by organization updates' \
  'Credential collection inventory is durable' \
  'required lazy feature metadata missing'
do
  if LC_ALL=C grep -a -F "$marker" "$work/provider" >/dev/null; then
    echo 'provider binary contains API contract tooling/artifact payload' >&2
    exit 1
  fi
done
