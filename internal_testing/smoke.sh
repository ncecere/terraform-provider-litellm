#!/bin/sh
# Isolated smoke test: plan -> apply -> no-drift plan -> destroy.
# Usage: smoke.sh <repo_root> resources <file...> datasources <file...>

set -eu

REPO_ROOT=${1:?usage: smoke.sh <repo_root> resources <file...> datasources <file...>}
shift
REPO_ROOT=$(cd "$REPO_ROOT" && pwd)
INTERNAL_TESTING="$REPO_ROOT/internal_testing"
RESOURCES="$INTERNAL_TESTING/resources"
DATASOURCES="$INTERNAL_TESTING/datasources"
PROVIDER_DIR=${PROVIDER_DIR:-$REPO_ROOT}

if [ ! -f "$PROVIDER_DIR/terraform-provider-litellm" ]; then
  echo "Provider binary not found at $PROVIDER_DIR/terraform-provider-litellm; run 'make build'." >&2
  exit 1
fi
if ! command -v terraform >/dev/null 2>&1; then
  echo "terraform is required for smoke tests." >&2
  exit 1
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required to encode the dev_overrides path safely." >&2
  exit 1
fi

mkdir -p "$INTERNAL_TESTING/.smoke-logs"
SMOKE_DIR=$(mktemp -d "$INTERNAL_TESTING/.smoke.XXXXXX")
SMOKE_LOG="$INTERNAL_TESTING/.smoke-logs/$(date '+%Y%m%d-%H%M%S')-$$.log"
APPLY_STARTED=0
SUCCESS=0

cleanup() {
  status=$?
  trap - EXIT INT TERM HUP
  if [ "$SUCCESS" -eq 1 ]; then
    rm -rf "$SMOKE_DIR"
    exit 0
  fi

  if [ "$APPLY_STARTED" -eq 1 ] && [ -f "$SMOKE_DIR/terraform.tfstate" ]; then
    echo "Attempting best-effort cleanup after failure..." >&3
    (cd "$SMOKE_DIR" && terraform destroy -refresh=false -auto-approve) >>"$SMOKE_LOG" 2>&1 || true
  fi
  echo "Smoke failed; workspace preserved at $SMOKE_DIR" >&3
  echo "See $SMOKE_LOG" >&3
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

trim() { printf '%s\n' "$1" | sed 's/^[,[:space:]]*//;s/[,[:space:]]*$//'; }

expand_arg() {
  _arg=$1
  while [ -n "$_arg" ]; do
    _f=${_arg%%,*}
    _rest=${_arg#*,}
    [ "$_rest" = "$_arg" ] && _rest=
    _arg=$_rest
    _f=$(trim "$_f")
    [ -n "$_f" ] && printf '%s\n' "$_f"
  done
}

exec 3>&1
exec >>"$SMOKE_LOG" 2>&1
cp "$INTERNAL_TESTING/provider.tf" "$INTERNAL_TESTING/variables.tf" "$SMOKE_DIR/"
cp "$INTERNAL_TESTING/terraform.tfvars.example" "$SMOKE_DIR/terraform.tfvars"

provider_dir_hcl=$(python3 -c 'import json, sys; print(json.dumps(sys.argv[1]))' "$PROVIDER_DIR")
cat >"$SMOKE_DIR/terraformrc" <<EOF
provider_installation {
  dev_overrides {
    "ncecere/litellm" = $provider_dir_hcl
  }
  direct {}
}
EOF
# Do not let ambient Terraform flags or provider variables redirect this run.
# In particular, acceptance preflights localhost and must not be overridable by
# TF_CLI_ARGS_plan=-var=litellm_api_base=... or TF_VAR_litellm_api_base.
unset TF_CLI_ARGS TF_CLI_ARGS_plan TF_CLI_ARGS_apply TF_CLI_ARGS_destroy
unset TF_VAR_litellm_api_base TF_VAR_litellm_api_key
unset LITELLM_API_BASE LITELLM_API_KEY
export TF_CLI_CONFIG_FILE="$SMOKE_DIR/terraformrc"
export TF_CLI_ARGS=-no-color
export TF_IN_AUTOMATION=1

RESOURCE_NAMES=
DATASOURCE_NAMES=
DIR=
FOUND=0
MISSING=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    resources) DIR=$RESOURCES; shift ;;
    datasources) DIR=$DATASOURCES; shift ;;
    *)
      for file in $(expand_arg "$1"); do
        if [ -n "$DIR" ] && [ -f "$DIR/$file" ]; then
          name=$(basename "$file")
          if [ -e "$SMOKE_DIR/$name" ]; then
            echo "Duplicate smoke filename: $name" >&3
            exit 1
          fi
          cp "$DIR/$file" "$SMOKE_DIR/$name"
          FOUND=1
          if [ "$DIR" = "$RESOURCES" ]; then
            RESOURCE_NAMES="$RESOURCE_NAMES $name"
          else
            DATASOURCE_NAMES="$DATASOURCE_NAMES $name"
          fi
        else
          if [ -n "$DIR" ]; then
            echo "Requested smoke file not found: $file" >&3
            MISSING=1
          fi
        fi
      done
      shift
      ;;
  esac
done

if [ "$FOUND" -ne 1 ]; then
  echo "No requested files were found under internal_testing." >&3
  exit 1
fi
if [ "$MISSING" -ne 0 ]; then
  echo "Refusing a partial smoke run because one or more requested files are missing." >&3
  exit 1
fi

printf '\n========== Isolated smoke test ==========\n'
[ -n "$RESOURCE_NAMES" ] && echo "Resources:$RESOURCE_NAMES"
[ -n "$DATASOURCE_NAMES" ] && echo "Datasources:$DATASOURCE_NAMES"

cd "$SMOKE_DIR"
echo '=== PLAN ==='
terraform plan -out=tfplan

echo '=== APPLY ==='
APPLY_STARTED=1
terraform apply -auto-approve tfplan

echo '=== NO-DRIFT PLAN ==='
set +e
terraform plan -detailed-exitcode >steady-plan.log 2>&1
plan_status=$?
set -e
cat steady-plan.log
if [ "$plan_status" -ne 0 ]; then
  if [ "$plan_status" -eq 2 ]; then
    echo 'Smoke failed: post-apply plan contains drift.' >&3
  fi
  exit "$plan_status"
fi

echo '=== DESTROY ==='
terraform destroy -auto-approve

state_list=$(terraform state list 2>/dev/null || true)
if [ -n "$state_list" ]; then
  echo "Smoke failed: state is not empty after destroy: $state_list" >&3
  exit 1
fi

SUCCESS=1
printf '\nSmoke passed: plan, apply, no-drift plan, and destroy succeeded.\n' >&3
echo "Results written to $SMOKE_LOG" >&3
