#!/bin/sh
# Run the disposable test stack under a checkout-specific Compose project name.

set -eu

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/.." && pwd)
project_id=$(printf '%s' "$REPO_ROOT" | cksum | awk '{print $1}')
project_name="litellm-provider-$project_id"

cd "$SCRIPT_DIR"
exec docker compose -p "$project_name" "$@"
