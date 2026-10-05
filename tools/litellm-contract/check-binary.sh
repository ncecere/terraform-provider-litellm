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
  79645770fedc7ec2627e6468d31062f20f82aecc \
  4d02833751421f29facee660c303f6b02cf0282c2709e93c7ad5898bbc1c38de \
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
