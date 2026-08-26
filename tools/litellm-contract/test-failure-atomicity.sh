#!/bin/sh
set -eu
repo_root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
installer="$repo_root/tools/litellm-contract/install-artifacts.sh"
files='openapi.json internal/contract/supplemental-routes.json internal/contract/manifest.json internal/contractapi/testdata/provider-operations.golden.json'
work=$(mktemp -d "${TMPDIR:-/tmp}/contract-install-test.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM

mode_of() {
  stat -f '%Lp' "$1" 2>/dev/null || stat -c '%a' "$1"
}
prepare() {
  root=$1
  stage=$2
  rm -rf "$root" "$stage"
  for relative in $files; do
    mkdir -p "$root/$(dirname "$relative")" "$stage/$(dirname "$relative")"
    printf 'old:%s\n' "$relative" > "$root/$relative"
    printf 'new:%s\n' "$relative" > "$stage/$relative"
    chmod 640 "$root/$relative"
    chmod 600 "$stage/$relative"
  done
}
assert_state() {
  root=$1
  state=$2
  for relative in $files; do
    expected="$state:$relative"
    actual=$(tr -d '\n' < "$root/$relative")
    if [ "$actual" != "$expected" ]; then
      echo "mixed artifact set: $relative is $actual, expected $expected" >&2
      exit 1
    fi
    if [ "$(mode_of "$root/$relative")" != 640 ]; then
      echo "artifact permission changed: $relative" >&2
      exit 1
    fi
  done
  if ls -d "$root"/.contract-artifact-backup.* >/dev/null 2>&1; then
    echo 'artifact installer left a backup directory' >&2
    exit 1
  fi
  for relative in $files; do
    for residue in "$root/$relative".contract-new.* "$root/$relative".contract-rollback.*; do
      if [ -e "$residue" ]; then
        echo "artifact installer left staging file $residue" >&2
        exit 1
      fi
    done
  done
}

for stage_number in 1 2 3 4; do
  root="$work/failure-$stage_number/root"
  stage="$work/failure-$stage_number/stage"
  prepare "$root" "$stage"
  set +e
  CONTRACT_TEST_FAIL_AFTER_REPLACE=$stage_number sh "$installer" "$root" "$stage"
  status=$?
  set -e
  if [ "$status" -ne $((90 + stage_number)) ]; then
    echo "failure stage $stage_number returned $status" >&2
    exit 1
  fi
  assert_state "$root" old

done

for stage_number in 1 2 3 4; do
  root="$work/interrupt-$stage_number/root"
  stage="$work/interrupt-$stage_number/stage"
  prepare "$root" "$stage"
  set +e
  CONTRACT_TEST_INTERRUPT_AFTER_REPLACE=$stage_number sh "$installer" "$root" "$stage"
  status=$?
  set -e
  if [ "$status" -ne 143 ]; then
    echo "interrupt stage $stage_number returned $status" >&2
    exit 1
  fi
  assert_state "$root" old

done

root="$work/success/root"
stage="$work/success/stage"
prepare "$root" "$stage"
sh "$installer" "$root" "$stage"
assert_state "$root" new
printf '%s\n' 'artifact install rollback passed every replacement failure, interruption, and success stage'
