#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)

# Both destructive targets fail closed without their independent confirmations.
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

# Import cleanup is a hard state-rm guarantee in both implementation and tests.
grep -q '"$CLI" state rm' "$SCRIPT_DIR/run.sh"
if grep -E 'CLEANUP_MODE" = import.*destroy|run_import.*terraform destroy' "$SCRIPT_DIR/run.sh" >/dev/null 2>&1; then
  echo 'import path contains a destroy operation' >&2
  exit 1
fi
if grep -E '(cp|source|\.) .*dev\.env' "$SCRIPT_DIR/run.sh" >/dev/null 2>&1; then
  echo 'runner attempts to read or copy dev.env' >&2
  exit 1
fi
