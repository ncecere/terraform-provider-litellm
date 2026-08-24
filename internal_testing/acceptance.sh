#!/bin/sh
# Explicit, local-only acceptance matrix driven by the isolated smoke harness.

set -eu

if [ "${TF_ACC:-}" != "1" ]; then
  echo "Refusing destructive acceptance tests: set TF_ACC=1." >&2
  exit 1
fi
if [ "${LITELLM_ACCEPTANCE_CONFIRM:-}" != "local-v1.98.0" ]; then
  echo "Set LITELLM_ACCEPTANCE_CONFIRM=local-v1.98.0 to confirm use of the disposable local backend." >&2
  exit 1
fi

REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)
API_BASE=http://localhost:4000

if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required for the acceptance preflight." >&2
  exit 1
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required for the acceptance preflight." >&2
  exit 1
fi
case "$API_BASE" in
  http://localhost:*|http://127.0.0.1:*) ;;
  *) echo "Acceptance tests are restricted to a loopback backend." >&2; exit 1 ;;
esac

version=$(curl --fail --silent --show-error "$API_BASE/openapi.json" |
  python3 -c 'import json, sys; print(json.load(sys.stdin).get("info", {}).get("version", ""))')
if [ "$version" != "1.98.0" ]; then
  echo "Expected disposable LiteLLM v1.98.0 at $API_BASE, found ${version:-unknown}." >&2
  exit 1
fi

run_case() {
  label=$1
  shift
  printf '\n===== ACCEPTANCE: %s =====\n' "$label"
  sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" "$@"
}

# Explicit coverage table. litellm_project is enterprise-only and intentionally
# excluded; every other registered resource has a lifecycle case here.
run_case access_group resources model_minimal.tf,access_group_minimal.tf
run_case agent resources agent_minimal.tf datasources agent.tf,agents_list.tf
run_case budget resources budget_minimal.tf
run_case credential resources credential_minimal.tf
run_case fallback resources fallback_minimal.tf
run_case guardrail resources guardrail_minimal.tf
run_case key resources key_minimal.tf
run_case key_block resources key_minimal.tf,key_block_minimal.tf
run_case mcp_server resources mcp_server_minimal.tf
run_case model resources model_minimal.tf
run_case organization resources organization_minimal.tf
run_case organization_member resources organization_minimal.tf,organization_member_minimal.tf
run_case prompt resources prompt_minimal.tf
run_case search_tool resources search_tool_minimal.tf
run_case tag resources tag_minimal.tf
run_case team resources team_minimal.tf
run_case team_block resources team_minimal.tf,team_block_minimal.tf
run_case team_member resources team_minimal.tf,team_member_minimal.tf
run_case team_member_add resources team_minimal.tf,team_member_add_minimal.tf
run_case unified_access_group resources unified_access_group_minimal.tf datasources unified_access_group.tf,unified_access_groups_list.tf
run_case user resources user_minimal.tf
run_case vector_store resources vector_store_minimal.tf

printf '\nAcceptance passed: 22/23 resources (project is enterprise-only).\n'
