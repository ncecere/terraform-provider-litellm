#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
  echo 'usage: install-artifacts.sh REPOSITORY_ROOT STAGING_ROOT' >&2
  exit 2
fi
repo_root=$1
stage=$2
destinations='openapi.json internal/contract/supplemental-routes.json internal/contract/manifest.json internal/contractapi/testdata/provider-operations.golden.json'
backup="$repo_root/.contract-artifact-backup.$$"
lock="$repo_root/.contract-artifact-writer.lock"
lock_candidate=
lock_token=
committed=0

release_owned_lock() {
  if [ -f "$lock" ] && [ "$(cat "$lock" 2>/dev/null || true)" = "$lock_token" ]; then
    rm -f "$lock"
  fi
  if [ -n "$lock_candidate" ]; then
    rm -f "$lock_candidate"
  fi
}

rollback_and_cleanup() {
  status=$?
  trap - EXIT HUP INT TERM
  if [ "$committed" -ne 1 ] && [ -d "$backup" ]; then
    for relative in $destinations; do
      saved="$backup/$relative"
      destination="$repo_root/$relative"
      if [ -f "$saved" ]; then
        rollback="$destination.contract-rollback.$$"
        cp -p "$saved" "$rollback"
        mv -f "$rollback" "$destination"
      fi
    done
  fi
  for relative in $destinations; do
    rm -f "$repo_root/$relative.contract-new.$$" "$repo_root/$relative.contract-rollback.$$"
  done
  rm -rf "$backup"
  release_owned_lock
  exit "$status"
}
trap rollback_and_cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

# A same-filesystem hard link is the portable atomic lock acquisition primitive.
# mktemp makes the ownership token unique even after PID reuse, preventing a
# losing or stale process from removing another writer's lock.
lock_candidate=$(mktemp "$repo_root/.contract-artifact-writer.candidate.XXXXXX")
lock_token="candidate=$(basename "$lock_candidate") pid=$$"
printf '%s\n' "$lock_token" > "$lock_candidate"
if ! ln "$lock_candidate" "$lock" 2>/dev/null; then
  rm -f "$lock_candidate"
  echo "another contract artifact writer holds $lock; inspect and remove a stale lock only after proving no writer is active" >&2
  exit 75
fi
rm -f "$lock_candidate"

if [ -n "${CONTRACT_TEST_LOCK_READY_FILE:-}" ]; then
  : > "$CONTRACT_TEST_LOCK_READY_FILE"
  if [ -n "${CONTRACT_TEST_WAIT_FILE:-}" ]; then
    while [ -e "$CONTRACT_TEST_WAIT_FILE" ]; do sleep 0.05; done
  fi
fi

mkdir -p "$backup"
for relative in $destinations; do
  source_file="$stage/$relative"
  destination="$repo_root/$relative"
  if [ ! -f "$source_file" ] || [ ! -f "$destination" ]; then
    echo "artifact install input is missing: $relative" >&2
    exit 1
  fi
  mkdir -p "$backup/$(dirname "$relative")"
  cp -p "$destination" "$backup/$relative"
  # Seed from the destination so replacing its contents retains its mode.
  cp -p "$destination" "$destination.contract-new.$$"
  cp "$source_file" "$destination.contract-new.$$"
done

stage_number=0
for relative in $destinations; do
  stage_number=$((stage_number + 1))
  mv -f "$repo_root/$relative.contract-new.$$" "$repo_root/$relative"
  if [ "${CONTRACT_TEST_WAIT_AFTER_REPLACE:-0}" = "$stage_number" ]; then
    if [ -n "${CONTRACT_TEST_REPLACE_READY_FILE:-}" ]; then
      : > "$CONTRACT_TEST_REPLACE_READY_FILE"
    fi
    if [ -n "${CONTRACT_TEST_WAIT_FILE:-}" ]; then
      while [ -e "$CONTRACT_TEST_WAIT_FILE" ]; do sleep 0.05; done
    fi
  fi
  if [ "${CONTRACT_TEST_FAIL_AFTER_REPLACE:-0}" = "$stage_number" ]; then
    echo "injected failure after artifact replacement $stage_number" >&2
    exit $((90 + stage_number))
  fi
  if [ "${CONTRACT_TEST_INTERRUPT_AFTER_REPLACE:-0}" = "$stage_number" ]; then
    kill -TERM "$$"
  fi
done
committed=1
