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
set_stage_state() {
  stage=$1
  state=$2
  for relative in $files; do
    printf '%s:%s\n' "$state" "$relative" > "$stage/$relative"
  done
}
wait_for_file() {
  marker=$1
  count=0
  while [ ! -f "$marker" ]; do
    count=$((count + 1))
    if [ "$count" -gt 400 ]; then
      echo "timed out waiting for installer marker $marker" >&2
      exit 1
    fi
    sleep 0.025
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
  if [ -e "$root/.contract-artifact-writer.lock" ]; then
    echo 'artifact installer left its writer lock' >&2
    exit 1
  fi
  if ls "$root"/.contract-artifact-writer.candidate.* >/dev/null 2>&1; then
    echo 'artifact installer left a lock candidate' >&2
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
expect_lock_conflict() {
  root=$1
  stage=$2
  set +e
  sh "$installer" "$root" "$stage"
  status=$?
  set -e
  if [ "$status" -ne 75 ]; then
    echo "concurrent writer returned $status, expected 75" >&2
    exit 1
  fi
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

root="$work/input-error/root"
stage="$work/input-error/stage"
prepare "$root" "$stage"
rm -f "$stage/internal/contract/manifest.json"
set +e
sh "$installer" "$root" "$stage"
status=$?
set -e
if [ "$status" -ne 1 ]; then
  echo "missing-input writer returned $status" >&2
  exit 1
fi
assert_state "$root" old

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

# A successful writer holding the lock before backup excludes a second writer.
root="$work/concurrent-success/root"
stage_a="$work/concurrent-success/stage-a"
stage_b="$work/concurrent-success/stage-b"
prepare "$root" "$stage_a"
cp -R "$stage_a" "$stage_b"
set_stage_state "$stage_a" new-a
set_stage_state "$stage_b" new-b
wait_file="$work/concurrent-success/wait"
ready_file="$work/concurrent-success/ready"
: > "$wait_file"
CONTRACT_TEST_LOCK_READY_FILE="$ready_file" CONTRACT_TEST_WAIT_FILE="$wait_file" sh "$installer" "$root" "$stage_a" &
first_pid=$!
wait_for_file "$ready_file"
expect_lock_conflict "$root" "$stage_b"
rm -f "$wait_file"
wait "$first_pid"
assert_state "$root" new-a

# Failure while a partially replaced set is protected cannot interleave with a
# successful writer; rollback finishes before the next writer may commit.
root="$work/concurrent-failure/root"
stage_a="$work/concurrent-failure/stage-a"
stage_b="$work/concurrent-failure/stage-b"
prepare "$root" "$stage_a"
cp -R "$stage_a" "$stage_b"
set_stage_state "$stage_a" new-a
set_stage_state "$stage_b" new-b
wait_file="$work/concurrent-failure/wait"
ready_file="$work/concurrent-failure/ready"
: > "$wait_file"
CONTRACT_TEST_WAIT_AFTER_REPLACE=2 CONTRACT_TEST_REPLACE_READY_FILE="$ready_file" CONTRACT_TEST_WAIT_FILE="$wait_file" CONTRACT_TEST_FAIL_AFTER_REPLACE=2 sh "$installer" "$root" "$stage_a" &
first_pid=$!
wait_for_file "$ready_file"
expect_lock_conflict "$root" "$stage_b"
rm -f "$wait_file"
set +e
wait "$first_pid"
status=$?
set -e
if [ "$status" -ne 92 ]; then
  echo "concurrent failing writer returned $status" >&2
  exit 1
fi
assert_state "$root" old
sh "$installer" "$root" "$stage_b"
assert_state "$root" new-b

# A caught signal during a protected partial replacement rolls back and releases
# the writer lock; a waiting/retrying writer can then install a complete set.
root="$work/concurrent-signal/root"
stage_a="$work/concurrent-signal/stage-a"
stage_b="$work/concurrent-signal/stage-b"
prepare "$root" "$stage_a"
cp -R "$stage_a" "$stage_b"
set_stage_state "$stage_a" new-a
set_stage_state "$stage_b" new-b
wait_file="$work/concurrent-signal/wait"
ready_file="$work/concurrent-signal/ready"
: > "$wait_file"
CONTRACT_TEST_WAIT_AFTER_REPLACE=2 CONTRACT_TEST_REPLACE_READY_FILE="$ready_file" CONTRACT_TEST_WAIT_FILE="$wait_file" sh "$installer" "$root" "$stage_a" &
first_pid=$!
wait_for_file "$ready_file"
expect_lock_conflict "$root" "$stage_b"
kill -TERM "$first_pid"
rm -f "$wait_file"
set +e
wait "$first_pid"
status=$?
set -e
if [ "$status" -ne 143 ]; then
  echo "signaled concurrent writer returned $status" >&2
  exit 1
fi
assert_state "$root" old
sh "$installer" "$root" "$stage_b"
assert_state "$root" new-b

# An ownerless lock is never guessed stale or removed automatically. This fails
# safely without backup or replacement; manual recovery is explicit.
root="$work/stale-lock/root"
stage="$work/stale-lock/stage"
prepare "$root" "$stage"
printf '%s\n' 'pid=definitely-not-active' > "$root/.contract-artifact-writer.lock"
expect_lock_conflict "$root" "$stage"
for relative in $files; do
  actual=$(tr -d '\n' < "$root/$relative")
  if [ "$actual" != "old:$relative" ]; then
    echo "stale-lock failure modified $relative" >&2
    exit 1
  fi
done
if [ "$(cat "$root/.contract-artifact-writer.lock")" != 'pid=definitely-not-active' ]; then
  echo 'installer modified an unowned stale lock' >&2
  exit 1
fi
rm -f "$root/.contract-artifact-writer.lock"
sh "$installer" "$root" "$stage"
assert_state "$root" new

root="$work/success/root"
stage="$work/success/stage"
prepare "$root" "$stage"
sh "$installer" "$root" "$stage"
assert_state "$root" new
printf '%s\n' 'artifact install rollback and exclusive-writer locking passed failure, interruption, concurrency, stale-lock, and success cases'
