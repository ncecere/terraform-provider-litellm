#!/bin/sh
# Smoke test: run plan -> apply -> destroy in internal_testing/.smoke/ with all requested files in the same state.
# Usage: smoke.sh <repo_root> resources <file...> datasources <file...>
# At least one of resources or datasources files is required. Requires: make local, make build.

set -e

REPO_ROOT="${1:?usage: smoke.sh <repo_root> resources <file...> datasources <file...>}"
shift
INTERNAL_TESTING="${REPO_ROOT}/internal_testing"
RESOURCES="${INTERNAL_TESTING}/resources"
DATASOURCES="${INTERNAL_TESTING}/datasources"
SMOKE_DIR="${INTERNAL_TESTING}/.smoke"
SMOKE_LOG="${SMOKE_DIR}/smoke.log"

trim() { echo "$1" | sed 's/^[,[:space:]]*//;s/[,[:space:]]*$//'; }

# Expand one arg: split on comma, trim each token (supports "a,b" or " ,a, b ")
expand_arg() {
  _arg="$1"
  while [ -n "$_arg" ]; do
    _f="${_arg%%,*}"
    _arg="${_arg#*,}"
    [ "$_f" = "$_arg" ] && _arg=""   # no comma left: consumed last token, exit after this one
    _f=$(trim "$_f")
    [ -n "$_f" ] && echo "$_f"
  done
}

cd "$REPO_ROOT"
mkdir -p "$SMOKE_DIR"
exec 3>&1
exec >"$SMOKE_LOG" 2>&1
cp "${INTERNAL_TESTING}/provider.tf" "${INTERNAL_TESTING}/variables.tf" "${SMOKE_DIR}/"
cp "${INTERNAL_TESTING}/terraform.tfvars.example" "${SMOKE_DIR}/terraform.tfvars"
cp "${INTERNAL_TESTING}/terraformrc" "${SMOKE_DIR}/"
export TF_CLI_CONFIG_FILE="${SMOKE_DIR}/terraformrc"
export TF_CLI_ARGS="-no-color"

# Collect and copy all requested files into .smoke/ (one run, shared state)
COPY_LIST=""
RESOURCE_NAMES=""
DATASOURCE_NAMES=""
DIR=
while [ $# -gt 0 ]; do
  case "$1" in
    resources)  DIR="$RESOURCES"; shift ;;
    datasources) DIR="$DATASOURCES"; shift ;;
    *)
      for f in $(expand_arg "$1"); do
        if [ -n "$DIR" ] && [ -f "${DIR}/${f}" ]; then
          name="$(basename "$f")"
          cp "${DIR}/${f}" "${SMOKE_DIR}/${name}"
          COPY_LIST="${COPY_LIST} ${SMOKE_DIR}/${name}"
          if [ "$DIR" = "$RESOURCES" ]; then
            RESOURCE_NAMES="${RESOURCE_NAMES} ${name}"
          else
            DATASOURCE_NAMES="${DATASOURCE_NAMES} ${name}"
          fi
        else
          if [ -n "$DIR" ]; then
            _path="${DIR#${REPO_ROOT}/}/${f}"
            echo "Warning: file not found: ${_path}" >&3
          fi
        fi
      done
      shift
      ;;
  esac
done

[ -n "$COPY_LIST" ] || {
  echo "No files found; all requested files were missing. Check paths under internal_testing/resources/ and internal_testing/datasources/." >&3
  echo "Usage: smoke.sh <repo_root> resources <file...> datasources <file...>" >&3
  exit 1
}

echo ""
echo "========== Smoke (all files in same state) =========="
[ -n "$RESOURCE_NAMES" ] && echo "Resources:${RESOURCE_NAMES}"
[ -n "$DATASOURCE_NAMES" ] && echo "Datasources:${DATASOURCE_NAMES}"
echo ""
echo "================================================================================"
echo "PLAN"
echo "================================================================================"
(cd "$SMOKE_DIR" && terraform plan -out=tfplan) || {
  rm -f ${COPY_LIST} "${SMOKE_DIR}/terraform.tfstate" "${SMOKE_DIR}/terraform.tfstate.backup" "${SMOKE_DIR}/tfplan"
  echo "See $SMOKE_LOG" >&3; echo "FAILED: plan"; exit 1
}

echo ""
echo "================================================================================"
echo "APPLY"
echo "================================================================================"
(cd "$SMOKE_DIR" && terraform apply -auto-approve tfplan) || {
  rm -f ${COPY_LIST} "${SMOKE_DIR}/terraform.tfstate" "${SMOKE_DIR}/terraform.tfstate.backup" "${SMOKE_DIR}/tfplan"
  echo "See $SMOKE_LOG" >&3; echo "FAILED: apply"; exit 1
}

echo ""
echo "================================================================================"
echo "DESTROY"
echo "================================================================================"
(cd "$SMOKE_DIR" && terraform destroy -auto-approve) || {
  rm -f ${COPY_LIST} "${SMOKE_DIR}/terraform.tfstate" "${SMOKE_DIR}/terraform.tfstate.backup" "${SMOKE_DIR}/tfplan"
  echo "See $SMOKE_LOG" >&3; echo "FAILED: destroy"; exit 1
}

state_list=$(cd "$SMOKE_DIR" && terraform state list 2>/dev/null || true)
if [ -n "$state_list" ]; then
  echo "FAILED: state not empty after destroy"
  rm -f ${COPY_LIST} "${SMOKE_DIR}/terraform.tfstate" "${SMOKE_DIR}/terraform.tfstate.backup" "${SMOKE_DIR}/tfplan"
  echo "See $SMOKE_LOG" >&3; exit 1
fi

rm -f ${COPY_LIST} "${SMOKE_DIR}/terraform.tfstate" "${SMOKE_DIR}/terraform.tfstate.backup" "${SMOKE_DIR}/tfplan"
echo ""
echo "================================================================================"
echo "SUMMARY"
echo "================================================================================"
echo "Smoke passed: all requested files in one plan/apply/destroy"
echo "Results written to $SMOKE_LOG" >&3
