#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)

if env -u TF_ACC -u LITELLM_ACCEPTANCE_CONFIRM python3 "$SCRIPT_DIR/harness.py" preflight local >/dev/null 2>&1; then
  echo 'local preflight unexpectedly succeeded' >&2
  exit 1
fi
if env -u TF_ACC -u LITELLM_REMOTE_ACCEPTANCE_CONFIRM -u LITELLM_TEST_NAMESPACE python3 "$SCRIPT_DIR/harness.py" preflight remote >/dev/null 2>&1; then
  echo 'remote preflight unexpectedly succeeded' >&2
  exit 1
fi

TF_ACC=1 LITELLM_ACCEPTANCE_CONFIRM=local-v1.98.0 \
  python3 "$SCRIPT_DIR/harness.py" preflight local >/dev/null
TF_ACC=1 LITELLM_REMOTE_ACCEPTANCE_CONFIRM=dev-disposable-objects-only \
LITELLM_TEST_NAMESPACE=issue210-abcdef12 \
  python3 "$SCRIPT_DIR/harness.py" preflight remote >/dev/null

# Importer detaches only the imported target; producer retains and destroys all
# owned objects. Failure injection must use the controlled endpoint proxy.
grep -q 'run_cli state rm "$address"' "$SCRIPT_DIR/run.sh"
grep -q 'producer-owned import cleanup' "$SCRIPT_DIR/run.sh"
grep -q 'fault_proxy.py' "$SCRIPT_DIR/run.sh"
if grep -q 'litellm_api_base=http://127.0.0.1:1' "$SCRIPT_DIR/run.sh"; then
  echo 'failure recovery uses a dead base URL instead of the controlled proxy' >&2
  exit 1
fi
if grep -E '(cp|source|\.) .*dev\.env' "$SCRIPT_DIR/run.sh" >/dev/null 2>&1; then
  echo 'runner attempts to read or copy dev.env' >&2
  exit 1
fi
if grep -q 'LITELLM_REMOTE_ACCEPTANCE_CONFIRM' "$REPO_ROOT/.github/workflows/upgrade-matrix.yml"; then
  echo 'workflow unexpectedly enables a remote mutation lane' >&2
  exit 1
fi
if grep -q 'report-records' "$SCRIPT_DIR/run.sh"; then
  echo 'runner still accepts forgeable manual report records' >&2
  exit 1
fi
if grep -q 'e2a7e6d' "$REPO_ROOT/.github/workflows/upgrade-matrix.yml"; then
  echo 'workflow still uses the stale runtime comparison SHA' >&2
  exit 1
fi

# The rebased #209/#254 base must pass, while an actual provider runtime edit
# relative to that exact base must fail.
base=$(git -C "$REPO_ROOT" rev-parse origin/issues)
head=$(git -C "$REPO_ROOT" rev-parse HEAD)
(cd "$REPO_ROOT" && python3 "$SCRIPT_DIR/verify_runtime_parity.py" \
  --event pull_request --base "$base" --head "$head") >/dev/null
(cd "$REPO_ROOT" && python3 "$SCRIPT_DIR/verify_runtime_parity.py" \
  --event workflow_dispatch --base "$base" --head "$head") >/dev/null
(cd "$REPO_ROOT" && python3 "$SCRIPT_DIR/verify_runtime_parity.py" \
  --event release --base "$base" --head "$head") >/dev/null
if (cd "$REPO_ROOT" && python3 "$SCRIPT_DIR/verify_runtime_parity.py" \
  --event release --base 0000000000000000000000000000000000000000 --head "$head") >/dev/null 2>&1; then
  echo 'release runtime parity did not fail closed without a base' >&2
  exit 1
fi
worktree=$(mktemp -d "${TMPDIR:-/tmp}/issue210-parity.XXXXXX")
rmdir "$worktree"
cleanup_worktree() { git -C "$REPO_ROOT" worktree remove --force "$worktree" >/dev/null 2>&1 || true; }
trap cleanup_worktree EXIT INT TERM HUP
git -C "$REPO_ROOT" worktree add --detach "$worktree" "$head" >/dev/null
printf '\n// adversarial runtime edit\n' >>"$worktree/main.go"
git -C "$worktree" add main.go
git -C "$worktree" -c user.name=issue210 -c user.email=issue210@example.invalid commit -m adversarial-runtime-edit >/dev/null
if (cd "$worktree" && python3 "$SCRIPT_DIR/verify_runtime_parity.py" \
  --event pull_request --base "$base" --head HEAD) >/dev/null 2>&1; then
  echo 'actual provider edit unexpectedly passed runtime parity' >&2
  exit 1
fi
cleanup_worktree
trap - EXIT INT TERM HUP
