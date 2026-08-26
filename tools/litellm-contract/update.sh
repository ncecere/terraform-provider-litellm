#!/bin/sh
set -eu

mode=${1:-update}
repo_root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
upstream_repo=https://github.com/BerriAI/litellm
upstream_commit=d8f71d7bdbd7c9873d98293f83d64c6db72847e6
required_uv='uv 0.12.6'
work=$(mktemp -d "${TMPDIR:-/tmp}/litellm-contract.XXXXXX")
stage="$work/stage"
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
actual_uv=$($uv_bin --version)
actual_uv_release=$(printf '%s\n' "$actual_uv" | awk '{print $1 " " $2}')
if [ "$actual_uv_release" != "$required_uv" ]; then
  echo "contract export requires exactly $required_uv; found $actual_uv" >&2
  exit 1
fi

mkdir -p "$stage/internal/contract" "$stage/internal/contractapi/testdata" "$stage/internal"
ln -s "$repo_root/internal/provider" "$stage/internal/provider"
cp "$repo_root/internal/contract/manifest.json" "$stage/internal/contract/manifest.json"
cp "$repo_root/internal/contract/reviewed-pins.json" "$stage/internal/contract/reviewed-pins.json"
cp "$repo_root/internal/contract/reviewed-operation-classification.json" "$stage/internal/contract/reviewed-operation-classification.json"

(
  cd "$source_root"
  "$uv_bin" sync --frozen --python 3.12.14 --extra proxy --no-dev
  .venv/bin/python "$repo_root/tools/litellm-contract/test_export.py"
  PYTHONHASHSEED=1 .venv/bin/python "$repo_root/tools/litellm-contract/export.py" \
    --source . --openapi-output "$work/openapi.first.json" \
    --supplemental-output "$work/supplemental.first.json"
  PYTHONHASHSEED=777 .venv/bin/python "$repo_root/tools/litellm-contract/export.py" \
    --source . --openapi-output "$work/openapi.second.json" \
    --supplemental-output "$work/supplemental.second.json"
)
cmp "$work/openapi.first.json" "$work/openapi.second.json"
cmp "$work/supplemental.first.json" "$work/supplemental.second.json"
cp "$work/openapi.first.json" "$stage/openapi.json"
cp "$work/supplemental.first.json" "$stage/internal/contract/supplemental-routes.json"

python3 "$repo_root/tools/litellm-contract/update_manifest.py" --root "$stage" --tool-root "$repo_root"

# Reviewed files are inputs, never generator outputs.
cmp "$repo_root/internal/contract/reviewed-pins.json" "$stage/internal/contract/reviewed-pins.json"
cmp "$repo_root/internal/contract/reviewed-operation-classification.json" "$stage/internal/contract/reviewed-operation-classification.json"

case "$mode" in
  update)
    (
      cd "$repo_root"
      go run ./internal/cmd/contract-check -root "$stage"
    )
    if [ "${CONTRACT_TEST_FAIL_BEFORE_REPLACE:-0}" = "1" ]; then
      echo 'injected failure before artifact replacement' >&2
      exit 97
    fi
    sh "$repo_root/tools/litellm-contract/install-artifacts.sh" "$repo_root" "$stage"
    ;;
  diff)
    diff_status=0
    for relative in \
      openapi.json \
      internal/contract/supplemental-routes.json \
      internal/contract/manifest.json \
      internal/contractapi/testdata/provider-operations.golden.json \
      internal/contract/reviewed-pins.json \
      internal/contract/reviewed-operation-classification.json
    do
      diff -u "$repo_root/$relative" "$stage/$relative" || diff_status=1
    done
    printf '%s\n' 'staged generated artifact hashes:'
    for relative in openapi.json internal/contract/supplemental-routes.json internal/contract/manifest.json internal/contractapi/testdata/provider-operations.golden.json
    do
      shasum -a 256 "$stage/$relative"
    done
    verify_status=0
    (
      cd "$repo_root"
      go run ./internal/cmd/contract-check -root "$stage"
    ) || verify_status=$?
    if [ "$diff_status" -ne 0 ] || [ "$verify_status" -ne 0 ]; then
      exit 1
    fi
    ;;
  *)
    echo "usage: $0 update|diff" >&2
    exit 2
    ;;
esac
