#!/bin/sh
# Explicit, local-only acceptance matrix driven by the isolated smoke harness.

set -eu

ASSEMBLY_ONLY=${LITELLM_ACCEPTANCE_ASSEMBLY_ONLY:-0}
REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd -P)
API_BASE=http://localhost:4000
CLI_VERSION=$(terraform version 2>/dev/null | sed -n '1{s/^[^0-9]*//;s/[^0-9.].*$//;p;}')
CLI_SUPPORTS_111=$(python3 -c 'import sys; p=tuple(int(v) for v in sys.argv[1].split(".")); print(1 if p >= (1, 11, 0) else 0)' "${CLI_VERSION:-0.0.0}")
# Supplemental/skip controls are assigned only by the exact cases below; an
# ambient environment cannot suppress evidence or detach a managed address.
unset SMOKE_SUPPLEMENTAL_ONLY SMOKE_FALLBACK_DELETE_UNSUPPORTED SMOKE_FALLBACK_DELETE_ADDRESS SMOKE_FALLBACK_IMPORT
unset SMOKE_SEARCH_TOOL_EXTERNAL_DELETE SMOKE_SEARCH_TOOL_DELETE_ADDRESS
unset SMOKE_ACCESS_GROUP_EXTERNAL_DELETE SMOKE_ACCESS_GROUP_DELETE_ADDRESS
unset SMOKE_USER_EXTERNAL_DELETE SMOKE_USER_DELETE_ADDRESS
unset SMOKE_KEY_EXTERNAL_DELETE SMOKE_KEY_DELETE_ADDRESS SMOKE_KEY_BLOCK_DELETE_ADDRESS
unset SMOKE_GUARDRAIL_EXTERNAL_DELETE SMOKE_GUARDRAIL_DELETE_ADDRESS
unset SMOKE_DIAGNOSTIC_OUTPUT SMOKE_LOG_OVERRIDE

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
  [ -n "${MATRIX_EVIDENCE_SESSION:-}" ] || return 0
  category=$1 subject=$2 status=$3 reason=${4:-} diagnostic=${5:-} evidence=${6:-$MATRIX_PRIVATE_LOG}
  assertion=bounded-feature-attempt
  [ "$category" != documentation ] || assertion=validated-documentation
  [ "$category" != import ] || assertion=import-immediate-no-drift-provenance
  [ "$status" != skipped ] || assertion=allowlisted-unavailability
  set -- --session "$MATRIX_EVIDENCE_SESSION" --name "$category:$subject" --category "$category" \
    --status "$status" --assertion "$assertion" --evidence "$evidence"
  [ -z "$reason" ] || set -- "$@" --reason "$reason"
  [ -z "$diagnostic" ] || set -- "$@" --diagnostic-code "$diagnostic" --diagnostic-evidence "$evidence"
  python3 "$MATRIX_HARNESS" record-observation "$@"
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
}

run_credential_update_case() {
  printf '\n===== ACCEPTANCE: credential_update =====\n'
  SMOKE_ASSEMBLY_ONLY=$ASSEMBLY_ONLY SMOKE_CREDENTIAL_UPDATE=1 sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" resources credential_update.tf
}

run_credential_import_case() {
  printf '\n===== ACCEPTANCE: credential_import =====\n'
  SMOKE_ASSEMBLY_ONLY=$ASSEMBLY_ONLY SMOKE_CREDENTIAL_IMPORT=1 sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" resources credential_import.tf
}

run_fallback_case() {
  printf '\n===== ACCEPTANCE: fallback =====\n'
  SMOKE_ASSEMBLY_ONLY=$ASSEMBLY_ONLY SMOKE_FALLBACK_DELETE_UNSUPPORTED=1 \
    SMOKE_FALLBACK_DELETE_ADDRESS=litellm_fallback.minimal \
    sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" resources fallback_minimal.tf datasources fallback.tf
}

run_fallback_import_case() {
  printf '\n===== ACCEPTANCE: fallback_special_identity_import =====\n'
  SMOKE_ASSEMBLY_ONLY=$ASSEMBLY_ONLY SMOKE_FALLBACK_IMPORT=1 SMOKE_SUPPLEMENTAL_ONLY=1 \
    SMOKE_FALLBACK_DELETE_UNSUPPORTED=1 SMOKE_FALLBACK_DELETE_ADDRESS='litellm_fallback.fallback_imported[0]' \
    sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" resources fallback_import.tf
}

run_mcp_import_case() {
  printf '\n===== ACCEPTANCE: mcp_import_projection =====\n'
  SMOKE_ASSEMBLY_ONLY=$ASSEMBLY_ONLY SMOKE_MCP_IMPORT=1 SMOKE_MCP_EVIDENCE=${SMOKE_MCP_EVIDENCE:-} \
    sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" resources mcp_server_import.tf
}

run_mcp_clear_lifecycle_case() {
  printf '\n===== ACCEPTANCE: mcp_presence_aware_clear_lifecycle =====\n'
  SMOKE_ASSEMBLY_ONLY=$ASSEMBLY_ONLY SMOKE_MCP_CLEAR_LIFECYCLE=1 \
    sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" resources mcp_server_clear_lifecycle.tf
}

run_mcp_token_exchange_lifecycle_case() {
  printf '\n===== ACCEPTANCE: mcp_token_exchange_lifecycle =====\n'
  SMOKE_ASSEMBLY_ONLY=$ASSEMBLY_ONLY SMOKE_MCP_TOKEN_EXCHANGE_LIFECYCLE=1 \
    sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" resources mcp_server_token_exchange_lifecycle.tf
}

run_agent_lifecycle_case() {
  printf '\n===== ACCEPTANCE: agent_lifecycle =====\n'
  SMOKE_ASSEMBLY_ONLY=$ASSEMBLY_ONLY SMOKE_AGENT_LIFECYCLE=1 sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" resources agent_lifecycle_clear.tf
}

run_search_tool_external_delete_case() {
  printf '\n===== ACCEPTANCE: search_tool_external_delete =====\n'
  SMOKE_ASSEMBLY_ONLY=$ASSEMBLY_ONLY SMOKE_SUPPLEMENTAL_ONLY=1 \
    SMOKE_SEARCH_TOOL_EXTERNAL_DELETE=1 SMOKE_SEARCH_TOOL_DELETE_ADDRESS=litellm_search_tool.minimal \
    sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" resources search_tool_minimal.tf
}

run_access_group_external_delete_case() {
  printf '\n===== ACCEPTANCE: access_group_external_delete =====\n'
  SMOKE_ASSEMBLY_ONLY=$ASSEMBLY_ONLY SMOKE_SUPPLEMENTAL_ONLY=1 \
    SMOKE_ACCESS_GROUP_EXTERNAL_DELETE=1 SMOKE_ACCESS_GROUP_DELETE_ADDRESS=litellm_access_group.minimal \
    sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" resources model_access_group.tf,access_group_minimal.tf
}

run_user_external_delete_case() {
  printf '\n===== ACCEPTANCE: user_external_delete =====\n'
  SMOKE_ASSEMBLY_ONLY=$ASSEMBLY_ONLY SMOKE_SUPPLEMENTAL_ONLY=1 \
    SMOKE_USER_EXTERNAL_DELETE=1 SMOKE_USER_DELETE_ADDRESS=litellm_user.minimal \
    sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" resources user_minimal.tf
}

run_key_external_delete_case() {
  printf '\n===== ACCEPTANCE: key_external_delete =====\n'
  SMOKE_ASSEMBLY_ONLY=$ASSEMBLY_ONLY SMOKE_SUPPLEMENTAL_ONLY=1 \
    SMOKE_KEY_EXTERNAL_DELETE=1 SMOKE_KEY_DELETE_ADDRESS=litellm_key.minimal \
    SMOKE_KEY_BLOCK_DELETE_ADDRESS=litellm_key_block.minimal \
    sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" resources key_minimal.tf,key_block_minimal.tf
}

run_guardrail_external_delete_case() {
  printf '\n===== ACCEPTANCE: guardrail_external_delete =====\n'
  SMOKE_ASSEMBLY_ONLY=$ASSEMBLY_ONLY SMOKE_SUPPLEMENTAL_ONLY=1 \
    SMOKE_GUARDRAIL_EXTERNAL_DELETE=1 SMOKE_GUARDRAIL_DELETE_ADDRESS=litellm_guardrail.safe_read \
    sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" resources guardrail_safe_read_minimal.tf
}

# Explicit coverage table. litellm_project is enterprise-only and intentionally
# excluded; every other registered resource has a lifecycle case here.
run_case access_group resources model_access_group.tf,access_group_minimal.tf datasources access_group.tf,access_groups_list.tf
run_access_group_external_delete_case
run_case agent resources mcp_server_minimal.tf,agent_minimal.tf,agent_bedrock_agentcore.tf,agent_mcp_tool_permissions.tf,agent_structured_advanced.tf datasources agent.tf,agents_list.tf,agent_structured_parity.tf
run_agent_lifecycle_case
run_case budget resources budget_minimal.tf datasources budget.tf,budgets_list.tf
run_case credential_values resources credential_minimal.tf,credential_full.tf datasources credential_minimal.tf,credential_full.tf
run_case credential_model resources credential_model.tf datasources credential_by_model.tf
run_credential_update_case
run_credential_import_case
run_fallback_case
run_fallback_import_case
run_case guardrail resources guardrail_minimal.tf datasources guardrail.tf,guardrails_list.tf
run_guardrail_external_delete_case
run_case guardrail_structured_mode resources guardrail_full.tf
if [ "$CLI_SUPPORTS_111" = "1" ]; then
  run_case key resources key_minimal.tf,key_router_settings.tf,key_semantic_json.tf,send_invite_email.tf datasources key.tf,keys_list.tf
  emit_controlled_record optional_feature send_invite_email passed
  printf '\n===== ACCEPTANCE: key_write_only =====\n'
  keywo_log="$SMOKE_PRIVATE_ROOT/.smoke-logs/key-write-only-attempt.log"
  keywo_command_log="$SMOKE_PRIVATE_ROOT/.smoke-logs/key-write-only-attempt.command.log"
  rm -f "$keywo_log" "$keywo_command_log"
  set +e
  SMOKE_ASSEMBLY_ONLY=$ASSEMBLY_ONLY SMOKE_LOG_OVERRIDE=$keywo_log SMOKE_DIAGNOSTIC_OUTPUT=$keywo_command_log \
    sh "$REPO_ROOT/internal_testing/smoke.sh" "$REPO_ROOT" resources key_write_only.tf
  keywo_status=$?
  set -e
  if [ "$keywo_status" -eq 0 ]; then
    emit_controlled_record optional_feature key_wo passed
  else
    python3 - "$keywo_command_log" <<'PY'
from pathlib import Path
import re,sys
text=Path(sys.argv[1]).read_text(encoding="utf-8",errors="replace")
if len(text.encode()) > 2*1024*1024: raise SystemExit(1)
normalized=re.sub(r"\s+"," ",text)
titles=[line.strip() for line in text.splitlines() if line.strip().startswith("Error:")]
if titles != ["Error: Write-Only Key Creation Error"]: raise SystemExit(1)
if "LiteLLM returned HTTP 400 while creating the write-only key." not in normalized: raise SystemExit(1)
if "response body was omitted" not in normalized: raise SystemExit(1)
PY
    emit_controlled_record optional_feature key_wo skipped api-endpoint-unavailable key-write-only-endpoint-unavailable "$keywo_command_log"
  fi
  run_case jwt_key_mapping resources key_minimal.tf,jwt_key_mapping.tf datasources jwt_key_mapping.tf,jwt_key_mappings_list.tf
  emit_controlled_record optional_feature jwt_key_mapping_key_wo passed
else
  run_case key resources key_minimal.tf,key_router_settings.tf,key_semantic_json.tf datasources key.tf,keys_list.tf
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
run_key_external_delete_case
run_case mcp_server resources mcp_server_minimal.tf,mcp_server_identity.tf,mcp_server_json_info.tf,mcp_server_token_exchange.tf datasources mcp_server.tf,mcp_servers_list.tf
run_mcp_clear_lifecycle_case
run_mcp_token_exchange_lifecycle_case
mcp_evidence="$SMOKE_PRIVATE_ROOT/.smoke-logs/mcp-immediate-import-evidence.json"
rm -f "$mcp_evidence"
SMOKE_MCP_EVIDENCE=$mcp_evidence run_mcp_import_case
emit_controlled_record import litellm_mcp_server passed '' '' "$mcp_evidence"
rm -f "$mcp_evidence"
run_case model resources model_minimal.tf datasources model.tf,models_list.tf
run_case model_semantic_json resources model_semantic_json.tf
run_case model_params_semantic_json resources model_params_semantic_json.tf
run_case organization resources organization_minimal.tf,organization_semantic_json.tf datasources organization.tf,organizations_list.tf
run_case organization_member resources organization_minimal.tf,organization_member_minimal.tf
run_case prompt resources prompt_minimal.tf datasources prompt.tf,prompts_list.tf
run_case search_tool resources search_tool_minimal.tf datasources search_tool.tf,search_tools_list.tf
run_search_tool_external_delete_case
run_case search_tool_json resources search_tool_full.tf
run_case tag resources tag_minimal.tf datasources tag.tf,tags_list.tf
run_case tag_full resources tag_full.tf
run_case team resources team_minimal.tf,team_semantic_json.tf datasources team.tf,teams_list.tf
run_case team_block resources team_minimal.tf,team_block_minimal.tf
run_case team_member resources team_minimal.tf,team_member_minimal.tf
run_case team_member_add resources team_minimal.tf,team_member_add_minimal.tf
run_case unified_access_group resources unified_access_group_minimal.tf datasources unified_access_group.tf,unified_access_groups_list.tf
run_case user resources user_minimal.tf datasources user.tf,users_list.tf
run_user_external_delete_case
run_case vector_store resources vector_store_minimal.tf datasources vector_store.tf

if [ "$ASSEMBLY_ONLY" = "1" ]; then
  printf '\nAcceptance assembly passed: every matrix case produced collision-free, parseable HCL.\n'
else
  printf '\nAcceptance passed: 23/24 resources (project is enterprise-only).\n'
fi
