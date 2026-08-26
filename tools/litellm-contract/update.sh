#!/bin/sh
set -eu

mode=${1:-update}
repo_root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
upstream_repo=https://github.com/BerriAI/litellm
upstream_commit=d8f71d7bdbd7c9873d98293f83d64c6db72847e6
work=$(mktemp -d "${TMPDIR:-/tmp}/litellm-contract.XXXXXX")
checkout=
cleanup() {
  rm -rf "$work"
  if [ -n "$checkout" ]; then rm -rf "$checkout"; fi
}
trap cleanup EXIT HUP INT TERM

if [ -n "${LITELLM_SOURCE:-}" ]; then
  source_root=$(CDPATH= cd -- "$LITELLM_SOURCE" && pwd)
else
  checkout=$(mktemp -d "${TMPDIR:-/tmp}/litellm-source.XXXXXX")
  git clone --filter=blob:none --no-checkout "$upstream_repo" "$checkout"
  git -C "$checkout" checkout --detach "$upstream_commit"
  source_root=$checkout
fi

uv_bin=${UV:-uv}
(
  cd "$source_root"
  "$uv_bin" sync --frozen --python 3.12.14 --extra proxy --no-dev
  PYTHONHASHSEED=1 .venv/bin/python "$repo_root/tools/litellm-contract/export.py" \
    --source . --openapi-output "$work/openapi.first.json" \
    --supplemental-output "$work/supplemental.first.json"
  PYTHONHASHSEED=777 .venv/bin/python "$repo_root/tools/litellm-contract/export.py" \
    --source . --openapi-output "$work/openapi.second.json" \
    --supplemental-output "$work/supplemental.second.json"
)
cmp "$work/openapi.first.json" "$work/openapi.second.json"
cmp "$work/supplemental.first.json" "$work/supplemental.second.json"

case "$mode" in
  update)
    cp "$work/openapi.first.json" "$repo_root/openapi.json"
    cp "$work/supplemental.first.json" "$repo_root/internal/contract/supplemental-routes.json"
    (cd "$repo_root" && python3 tools/litellm-contract/update_manifest.py)
    ;;
  diff)
    diff -u "$repo_root/openapi.json" "$work/openapi.first.json"
    diff -u "$repo_root/internal/contract/supplemental-routes.json" "$work/supplemental.first.json"
    ;;
  *)
    echo "usage: $0 update|diff" >&2
    exit 2
    ;;
esac
