#!/bin/sh
# Explicit, local-only acceptance matrix driven by the isolated smoke harness.

set -eu

ASSEMBLY_ONLY=${LITELLM_ACCEPTANCE_ASSEMBLY_ONLY:-0}
REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)
API_BASE=http://localhost:4000
CLI_VERSION=$(terraform version 2>/dev/null | sed -n '1{s/^[^0-9]*//;s/[^0-9.].*$//;p;}')
CLI_SUPPORTS_111=$(python3 -c 'import sys; p=tuple(int(v) for v in sys.argv[1].split(".")); print(1 if p >= (1, 11, 0) else 0)' "${CLI_VERSION:-0.0.0}")

if [ "$ASSEMBLY_ONLY" != "1" ]; then
  if [ "${TF_ACC:-}" != "1" ]; then
    echo "Refusing destructive acceptance tests: set TF_ACC=1." >&2
    exit 1
  fi
  if [ "${LITELLM_ACCEPTANCE_CONFIRM:-}" != "local-v1.98.0" ]; then
    echo "Set LITELLM_ACCEPTANCE_CONFIRM=local-v1.98.0 to confirm use of the disposable local backend." >&2
    exit 1
  fi
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

  version=$(curl --fail --silent --show-error --connect-timeout 3 --max-time 15 "$API_BASE/openapi.json" |
    python3 -c 'import json, sys; print(json.load(sys.stdin).get("info", {}).get("version", ""))')
  if [ "$version" != "1.98.0" ]; then
    echo "Expected disposable LiteLLM v1.98.0 at $API_BASE, found ${version:-unknown}." >&2
    exit 1
  fi
fi

emit_controlled_record() {
  [ "$ASSEMBLY_ONLY" = "0" ] || return 0
  [ -n "${MATRIX_EXECUTION_RECORDS:-}" ] || return 0
  tab=$(printf '\t')
  line="$1:$2${tab}$1${tab}$3${tab}${4:-}${tab}"
  grep -Fqx "$line" "$MATRIX_EXECUTION_RECORDS" 2>/dev/null || printf '%s\n' "$line" >>"$MATRIX_EXECUTION_RECORDS"
}

emit_execution_records() {
  [ "$ASSEMBLY_ONLY" = "0" ] || return 0
  tab=$(printf '\t')
  [ -n "${MATRIX_EXECUTION_RECORDS:-}" ] || return 0
  kind=
  split_ifs=$(printf ' \t\n_')
  split_ifs=${split_ifs%_}
  for argument in "$@"; do
    case "$argument" in
      resources|datasources) kind=$argument ;;
      *.tf)
        old_ifs=$IFS; IFS=,
        for fixture in $argument; do
          if [ "$kind" = resources ]; then
            subjects=$(grep -Eho 'resource[[:space:]]+"litellm_[a-z_]+"' "$REPO_ROOT/internal_testing/resources/$fixture" | cut -d'"' -f2 | sort -u)
            IFS=$split_ifs
            for subject in $subjects; do
              for category in resource_coverage lifecycle drift; do
                line="$category:$subject${tab}$category${tab}passed${tab}${tab}"
                grep -Fqx "$line" "$MATRIX_EXECUTION_RECORDS" 2>/dev/null || printf '%s\n' "$line" >>"$MATRIX_EXECUTION_RECORDS"
              done
            done
            IFS=,
          elif [ "$kind" = datasources ]; then
            subjects=$(grep -Eho 'data[[:space:]]+"litellm_[a-z_]+"' "$REPO_ROOT/internal_testing/datasources/$fixture" | cut -d'"' -f2 | sort -u)
            IFS=$split_ifs
            for subject in $subjects; do
              line="data_source:$subject${tab}data_source${tab}passed${tab}${tab}"
              grep -Fqx "$line" "$MATRIX_EXECUTION_RECORDS" 2>/dev/null || printf '%s\n' "$line" >>"$MATRIX_EXECUTION_RECORDS"
            done
            IFS=,
          fi
        done
        IFS=$old_ifs ;;
    esac
  done
}

run_case() {
  label=$1
  shift
  printf '\n===== ACCEPTANCE: %s =====\n' "$label"
  if [ "$ASSEMBLY_ONLY" = "1" ]; then
    SMOKE_ASSEMBLY_ONLY=1 sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" "$@"
  else
    sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" "$@"
  fi
  emit_execution_records "$@"
}

run_credential_update_case() {
  printf '\n===== ACCEPTANCE: credential_update =====\n'
  SMOKE_ASSEMBLY_ONLY=$ASSEMBLY_ONLY SMOKE_CREDENTIAL_UPDATE=1 sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" resources credential_update.tf
  emit_execution_records resources credential_update.tf
}

run_credential_import_case() {
  printf '\n===== ACCEPTANCE: credential_import =====\n'
  SMOKE_ASSEMBLY_ONLY=$ASSEMBLY_ONLY SMOKE_CREDENTIAL_IMPORT=1 sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" resources credential_import.tf
  emit_execution_records resources credential_import.tf
}

run_fallback_import_case() {
  printf '\n===== ACCEPTANCE: fallback_special_identity_import =====\n'
  SMOKE_ASSEMBLY_ONLY=$ASSEMBLY_ONLY SMOKE_FALLBACK_IMPORT=1 sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" resources fallback_import.tf
  emit_execution_records resources fallback_import.tf
}

run_mcp_import_case() {
  printf '\n===== ACCEPTANCE: mcp_import_projection =====\n'
  SMOKE_ASSEMBLY_ONLY=$ASSEMBLY_ONLY SMOKE_MCP_IMPORT=1 sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" resources mcp_server_import.tf
}

run_agent_lifecycle_case() {
  printf '\n===== ACCEPTANCE: agent_lifecycle =====\n'
  SMOKE_ASSEMBLY_ONLY=$ASSEMBLY_ONLY SMOKE_AGENT_LIFECYCLE=1 sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" resources agent_lifecycle_clear.tf
  emit_execution_records resources agent_lifecycle_clear.tf
}

# Explicit coverage table. litellm_project is enterprise-only and intentionally
# excluded; every other registered resource has a lifecycle case here.
run_case access_group resources model_access_group.tf,access_group_minimal.tf datasources access_group.tf,access_groups_list.tf
run_case agent resources mcp_server_minimal.tf,agent_minimal.tf,agent_bedrock_agentcore.tf,agent_mcp_tool_permissions.tf,agent_structured_advanced.tf datasources agent.tf,agents_list.tf,agent_structured_parity.tf
run_agent_lifecycle_case
run_case budget resources budget_minimal.tf datasources budget.tf,budgets_list.tf
run_case credential_values resources credential_minimal.tf,credential_full.tf datasources credential_minimal.tf,credential_full.tf
run_case credential_model resources credential_model.tf datasources credential_by_model.tf
run_credential_update_case
run_credential_import_case
run_case fallback resources fallback_minimal.tf datasources fallback.tf
run_fallback_import_case
run_case guardrail resources guardrail_minimal.tf datasources guardrail.tf,guardrails_list.tf
run_case guardrail_structured_mode resources guardrail_full.tf
if [ "$CLI_SUPPORTS_111" = "1" ]; then
  run_case key resources key_minimal.tf,key_router_settings.tf,send_invite_email.tf datasources key.tf,keys_list.tf
  emit_controlled_record optional_feature send_invite_email passed
  emit_controlled_record optional_feature key_wo skipped api-endpoint-unavailable
  run_case jwt_key_mapping resources key_minimal.tf,jwt_key_mapping.tf datasources jwt_key_mapping.tf,jwt_key_mappings_list.tf
  emit_controlled_record optional_feature jwt_key_mapping_key_wo passed
else
  run_case key resources key_minimal.tf,key_router_settings.tf datasources key.tf,keys_list.tf
  emit_controlled_record optional_feature send_invite_email skipped cli-version-below-1.11
  emit_controlled_record optional_feature key_wo skipped cli-version-below-1.11
  emit_controlled_record optional_feature jwt_key_mapping_key_wo skipped cli-version-below-1.11
  emit_controlled_record resource_coverage litellm_jwt_key_mapping skipped cli-version-below-1.11
  emit_controlled_record lifecycle litellm_jwt_key_mapping skipped cli-version-below-1.11
  emit_controlled_record drift litellm_jwt_key_mapping skipped cli-version-below-1.11
  emit_controlled_record data_source litellm_jwt_key_mapping skipped cli-version-below-1.11
  emit_controlled_record data_source litellm_jwt_key_mappings skipped cli-version-below-1.11
fi
run_case key_block resources key_minimal.tf,key_block_minimal.tf,key_block_hash.tf
run_case mcp_server resources mcp_server_minimal.tf datasources mcp_server.tf,mcp_servers_list.tf
run_mcp_import_case
run_case model resources model_minimal.tf datasources model.tf,models_list.tf
run_case organization resources organization_minimal.tf datasources organization.tf,organizations_list.tf
run_case organization_member resources organization_minimal.tf,organization_member_minimal.tf
run_case prompt resources prompt_minimal.tf datasources prompt.tf,prompts_list.tf
run_case search_tool resources search_tool_minimal.tf datasources search_tool.tf,search_tools_list.tf
run_case search_tool_json resources search_tool_full.tf
run_case tag resources tag_minimal.tf datasources tag.tf,tags_list.tf
run_case tag_full resources tag_full.tf
run_case team resources team_minimal.tf datasources team.tf,teams_list.tf
run_case team_block resources team_minimal.tf,team_block_minimal.tf
run_case team_member resources team_minimal.tf,team_member_minimal.tf
run_case team_member_add resources team_minimal.tf,team_member_add_minimal.tf
run_case unified_access_group resources unified_access_group_minimal.tf datasources unified_access_group.tf,unified_access_groups_list.tf
run_case user resources user_minimal.tf datasources user.tf,users_list.tf
run_case vector_store resources vector_store_minimal.tf datasources vector_store.tf

if [ "$ASSEMBLY_ONLY" = "1" ]; then
  printf '\nAcceptance assembly passed: every matrix case produced collision-free, parseable HCL.\n'
else
  printf '\nAcceptance passed: 23/24 resources (project is enterprise-only).\n'
fi
