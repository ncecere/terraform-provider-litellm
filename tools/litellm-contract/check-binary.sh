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
  ea7ca16dbc3904d9e59a9505f379a2ca3091821f \
  96a10b8673cd9da46d99619f87def30ce272b8bdb6d3e95fd568e69dcde1071f \
  '/v1/mcp/server/{server_id}/oauth-user-credential/status' \
  '/management/v1/users/bulk_delete' \
  'Credential collection inventory is durable' \
  'required lazy feature metadata missing'
do
  if LC_ALL=C grep -a -F "$marker" "$work/provider" >/dev/null; then
    echo 'provider binary contains API contract tooling/artifact payload' >&2
    exit 1
  fi
done
