#!/usr/bin/env python3
"""Fail closed unless provider runtime is unchanged from the selected event base."""
from __future__ import annotations

import argparse
import hashlib
import subprocess
import sys

RUNTIME_PATHS = ("main.go", "internal/provider")
ZERO_SHA = "0" * 40
REVIEWED_ISSUE210_BASES = {
    "a5cca7a1a9e416e72c40de6a5aa8c8bdd63a7701",
    "be91657f738b5764aa09db4e38c69dcc09683198",
    "d728ea5d8bb8d0fc5c6648e8d1e1b1d2f929601c",
    "e3e5aef247903f0a44c178aa1d40a5781df096ec",
}
REVIEWED_ISSUE210_PATHS = (
    "internal/provider/agent_ownership_pending_protocol_test.go",
    "internal/provider/resource_agent.go",
    "internal/provider/resource_agent_lifecycle.go",
    "internal/provider/resource_fallback.go",
    "internal/provider/resource_fallback_test.go",
)
REVIEWED_ISSUE210_RUNTIME_DIFF_SHA256 = (
    "c0ba020349961e46c4b01518901c868f1e4c434f51030aba8f9784590663b313"
)
REVIEWED_ISSUE217_BASE = "79ec45fcefe647e6ccdee66858a12fcca7bfe20a"
REVIEWED_ISSUE217_PATHS = (
    "internal/provider/numeric_import_ownership_test.go",
    "internal/provider/numeric_import_protocol_additional_test.go",
    "internal/provider/read_test.go",
    "internal/provider/resource_team.go",
    "internal/provider/resource_team_test.go",
    "internal/provider/team_response.go",
    "internal/provider/team_response_protocol_test.go",
    "internal/provider/team_response_test.go",
)
REVIEWED_ISSUE217_RUNTIME_DIFF_SHA256 = (
    "d051caea606b1760b389b14ae74a2305508700a92e4a08e3ffb978deb16b0517"
)
REVIEWED_ISSUE213_BASE = "33bc7ff74e910c1c5054808907fb614702f701ec"
REVIEWED_ISSUE213_PATHS = (
    "internal/provider/api_numbers.go",
    "internal/provider/api_numbers_test.go",
    "internal/provider/datasource_mcp_server.go",
    "internal/provider/datasource_mcp_servers_list.go",
    "internal/provider/mcp_info_create_recovery_protocol_test.go",
    "internal/provider/mcp_info_json.go",
    "internal/provider/mcp_info_json_test.go",
    "internal/provider/mcp_info_lifecycle.go",
    "internal/provider/mcp_info_plan.go",
    "internal/provider/mcp_info_provenance.go",
    "internal/provider/mcp_info_provenance_test.go",
    "internal/provider/mcp_info_stage2_protocol_test.go",
    "internal/provider/mcp_info_stage2_test.go",
    "internal/provider/mcp_info_stage3_protocol_additional_test.go",
    "internal/provider/mcp_info_stage3_test.go",
    "internal/provider/mcp_info_state_upgrade_protocol_test.go",
    "internal/provider/mcp_server_import_projection_protocol_test.go",
    "internal/provider/mcp_server_lifecycle_protocol_test.go",
    "internal/provider/numeric_map_validation_test.go",
    "internal/provider/resource_mcp_server.go",
    "internal/provider/resource_mcp_server_test.go",
)
REVIEWED_ISSUE213_RUNTIME_DIFF_SHA256 = (
    "e921feaf70385ea8ee2d3928f052a80b439139cb31bf72a5e2bba48026935819"
)
REVIEWED_ISSUE222_BASE = "6296b878bd183d62a9b2b12cde8f1109eff5f37c"
REVIEWED_ISSUE222_PATHS = (
    "internal/provider/datasource_access_group.go",
    "internal/provider/datasource_access_groups_list.go",
    "internal/provider/datasource_agents_list.go",
    "internal/provider/datasource_budget.go",
    "internal/provider/datasource_budgets_list.go",
    "internal/provider/datasource_completion_protocol_test.go",
    "internal/provider/datasource_guardrails_list.go",
    "internal/provider/datasource_key.go",
    "internal/provider/datasource_keys_list.go",
    "internal/provider/datasource_list_presence_protocol_test.go",
    "internal/provider/datasource_mcp_presence_protocol_test.go",
    "internal/provider/datasource_mcp_presence_test.go",
    "internal/provider/datasource_mcp_server.go",
    "internal/provider/datasource_mcp_servers_list.go",
    "internal/provider/datasource_model.go",
    "internal/provider/datasource_models_list.go",
    "internal/provider/datasource_organizations_list.go",
    "internal/provider/datasource_presence.go",
    "internal/provider/datasource_presence_review_regression_test.go",
    "internal/provider/datasource_presence_test.go",
    "internal/provider/datasource_projects_list.go",
    "internal/provider/datasource_prompts_list.go",
    "internal/provider/datasource_search_tool.go",
    "internal/provider/datasource_search_tools_list.go",
    "internal/provider/datasource_singular_presence_protocol_test.go",
    "internal/provider/datasource_tags_list.go",
    "internal/provider/datasource_team.go",
    "internal/provider/datasource_team_presence_protocol_test.go",
    "internal/provider/datasource_team_presence_test.go",
    "internal/provider/datasource_teams_list.go",
    "internal/provider/datasource_unified_access_groups_list.go",
    "internal/provider/datasource_user.go",
    "internal/provider/datasource_users_list.go",
    "internal/provider/jwt_key_mapping_api.go",
    "internal/provider/list_helpers.go",
)
REVIEWED_ISSUE222_RUNTIME_DIFF_SHA256 = (
    "4842dbb53f7277f4e20a09a35e2f12d5c419c6bdd7d177af5eb623649c6db34c"
)
REVIEWED_ISSUE202_PHASE2_BASE = "5f1b3a9c5889f552bd5227aed724e85b853d3db1"
REVIEWED_ISSUE202_PHASE2_PATHS = (
    "internal/provider/jwt_key_mapping_api.go",
    "internal/provider/jwt_key_mapping_safe_read_protocol_test.go",
    "internal/provider/jwt_key_mapping_safe_read_test.go",
    "internal/provider/safe_read_retry.go",
)
REVIEWED_ISSUE202_PHASE2_RUNTIME_DIFF_SHA256 = (
    "76315bef756afdc23bc2aaf69c46bb18b4dfc7b8eaa1696667a2233ee9a7b63e"
)
REVIEWED_ISSUE202_SEARCH_TOOL_BASE = "c958a0e594ba39eb2e689692160a905964f96ef4"
REVIEWED_ISSUE202_SEARCH_TOOL_PATHS = (
    "internal/provider/datasource_search_tool.go",
    "internal/provider/resource_search_tool.go",
    "internal/provider/search_tool_safe_read_protocol_test.go",
    "internal/provider/search_tool_safe_read_test.go",
)
REVIEWED_ISSUE202_SEARCH_TOOL_RUNTIME_DIFF_SHA256 = (
    "07fc4c980e17501188b0d9195e67ec80af1a881dc65432e16447f15d1dca1f3b"
)
REVIEWED_ISSUE212_BASE = "de485aa3c038c055c81a518b1104c2d075116742"
REVIEWED_ISSUE212_PATHS = (
    "internal/provider/mcp_audit_blockers_protocol_test.go",
    "internal/provider/mcp_field_lifecycle.go",
    "internal/provider/mcp_field_ownership.go",
    "internal/provider/mcp_field_ownership_test.go",
    "internal/provider/mcp_info_create_recovery_protocol_test.go",
    "internal/provider/mcp_info_stage2_test.go",
    "internal/provider/mcp_info_state_upgrade_protocol_test.go",
    "internal/provider/mcp_server_lifecycle_protocol_test.go",
    "internal/provider/mcp_update_completion_protocol_test.go",
    "internal/provider/resource_mcp_server.go",
    "internal/provider/resource_mcp_server_test.go",
)
REVIEWED_ISSUE212_RUNTIME_DIFF_SHA256 = (
    "8f4188f5a9ce9fac933d23c0744c9cb988b3792b9a0f0cd194520a096fc39608"
)
REVIEWED_ISSUE214_BASE = "00a48113bf575b17a30c6150db2e945f5947ac5a"
REVIEWED_ISSUE214_PATHS = (
    "internal/provider/mcp_audit_blockers_protocol_test.go",
    "internal/provider/mcp_field_lifecycle.go",
    "internal/provider/mcp_identity_v198_protocol_test.go",
    "internal/provider/mcp_info_create_recovery_protocol_test.go",
    "internal/provider/mcp_server_lifecycle_protocol_test.go",
    "internal/provider/mcp_update_completion_protocol_test.go",
    "internal/provider/resource_mcp_server.go",
)
REVIEWED_ISSUE214_RUNTIME_DIFF_SHA256 = (
    "765751cedc125b6d52c6995ba5fa7ebef5ef14331e00bc36fddc95f22cc33421"
)
REVIEWED_ISSUE224_BASE = "92a474ce027fddf362b844100f020adb278428b6"
REVIEWED_ISSUE224_PATHS = (
    "internal/provider/agent_collection_block_projection_protocol_test.go",
    "internal/provider/agent_mcp_tool_permissions_test.go",
    "internal/provider/agent_patch.go",
    "internal/provider/agent_structured.go",
    "internal/provider/agent_structured_test.go",
    "internal/provider/build_request_test.go",
    "internal/provider/collection_conversion.go",
    "internal/provider/collection_conversion_audit_test.go",
    "internal/provider/collection_conversion_test.go",
    "internal/provider/datasource_agent.go",
    "internal/provider/datasource_agents_list.go",
    "internal/provider/datasource_fallback.go",
    "internal/provider/datasource_organization.go",
    "internal/provider/datasource_tag.go",
    "internal/provider/datasource_tags_list.go",
    "internal/provider/datasource_unified_access_group.go",
    "internal/provider/invite_email_test.go",
    "internal/provider/numeric_map_validation_test.go",
    "internal/provider/request_enum_validators_test.go",
    "internal/provider/resource_agent.go",
    "internal/provider/resource_agent_lifecycle.go",
    "internal/provider/resource_agent_lifecycle_test.go",
    "internal/provider/resource_agent_test.go",
    "internal/provider/resource_fallback.go",
    "internal/provider/resource_fallback_test.go",
    "internal/provider/resource_key.go",
    "internal/provider/resource_mcp_server.go",
    "internal/provider/resource_model.go",
    "internal/provider/resource_organization.go",
    "internal/provider/resource_organization_member.go",
    "internal/provider/resource_tag.go",
    "internal/provider/resource_team.go",
    "internal/provider/resource_team_member_add.go",
    "internal/provider/resource_team_member_add_test.go",
    "internal/provider/resource_unified_access_group.go",
    "internal/provider/resource_unified_access_group_test.go",
    "internal/provider/resource_user.go",
    "internal/provider/resource_user_test.go",
    "internal/provider/resource_vector_store.go",
    "internal/provider/strict_collection_request_builders_test.go",
    "internal/provider/strict_collection_response_stage4_test.go",
    "internal/provider/strict_collection_stage3_test.go",
    "internal/provider/strict_collection_team_mcp_test.go",
    "internal/provider/team_response.go",
    "internal/provider/team_response_test.go",
    "internal/provider/vector_store_contract_test.go",
    "internal/provider/vector_store_helpers.go",
)
REVIEWED_ISSUE224_RUNTIME_DIFF_SHA256 = (
    "f2136661a605f837a1502520a3fc09d86cf34bf10164d677bb05264dbd422772"
)
REVIEWED_ISSUE215_BASE = "7b2781d1ffb4525ba97b6d9d4cf95b6ce133ee72"
REVIEWED_ISSUE215_PATHS = (
    "internal/provider/datasource_mcp_presence_protocol_test.go",
    "internal/provider/datasource_mcp_presence_test.go",
    "internal/provider/datasource_mcp_server.go",
    "internal/provider/datasource_mcp_servers_list.go",
    "internal/provider/mcp_info_stage2_test.go",
    "internal/provider/mcp_issue215_parity_protocol_test.go",
    "internal/provider/mcp_issue215_parity_test.go",
    "internal/provider/mcp_server_lifecycle_protocol_test.go",
    "internal/provider/mcp_update_completion_protocol_test.go",
    "internal/provider/request_enum_validators_test.go",
    "internal/provider/resource_mcp_server.go",
)
REVIEWED_ISSUE215_RUNTIME_DIFF_SHA256 = (
    "46f7d769f98533da902b553d1373f7be3c78af4832502116408d21145e6d4967"
)
REVIEWED_ISSUE218_FOUNDATION_BASE = "fcbdad4998d29a733afa5e03ad41494dbd32a6d0"
REVIEWED_ISSUE218_FOUNDATION_PATHS = (
    "internal/provider/semantic_dictionary.go",
    "internal/provider/semantic_dictionary_test.go",
)
REVIEWED_ISSUE218_FOUNDATION_RUNTIME_DIFF_SHA256 = (
    "d34da29f71dbd37f954425152567e1c72ec2129a2c96b3679ecf3df60938d356"
)
REVIEWED_ISSUE218_MODEL_BASE = "8c8ce955aef16764848294ddcbc8e99124f3ff43"
REVIEWED_ISSUE218_MODEL_PATHS = (
    "internal/provider/resource_model.go",
    "internal/provider/resource_model_semantic_dictionary.go",
    "internal/provider/resource_model_semantic_dictionary_protocol_test.go",
    "internal/provider/resource_model_semantic_dictionary_test.go",
)
REVIEWED_ISSUE218_MODEL_RUNTIME_DIFF_SHA256 = (
    "011fbc3e50d04d18f708ee83fae0e7395802d5d394a1e2be35f1af1ba9e8994b"
)
REVIEWED_ISSUE218_MODEL_PARAMS_BASE = "bf437d1a827ce789324d0e124555191dbafe52bf"
REVIEWED_ISSUE218_MODEL_PARAMS_PATHS = (
    "internal/provider/resource_model.go",
    "internal/provider/resource_model_litellm_params_dictionary.go",
    "internal/provider/resource_model_litellm_params_dictionary_protocol_test.go",
    "internal/provider/resource_model_litellm_params_dictionary_test.go",
    "internal/provider/resource_model_semantic_dictionary.go",
)
REVIEWED_ISSUE218_MODEL_PARAMS_RUNTIME_DIFF_SHA256 = (
    "7f13de72ccc3c8953d2bcc1c9d282e5ebbd69c5fea00fa1f59115405efdbea13"
)
REVIEWED_ISSUE218_KEY_BASE = "a3e347039f5a1a24a133ba5f8e5c416d574517e4"
REVIEWED_ISSUE218_KEY_PATHS = (
    "internal/provider/resource_key.go",
    "internal/provider/resource_key_semantic_dictionary.go",
    "internal/provider/resource_key_semantic_dictionary_protocol_test.go",
    "internal/provider/resource_key_semantic_dictionary_test.go",
)
REVIEWED_ISSUE218_KEY_RUNTIME_DIFF_SHA256 = (
    "66c163dbc6140c04270f8acfa9658c23522194b39f850beaae032cf61b183cd2"
)
REVIEWED_ISSUE218_ORGANIZATION_BASE = "c9c585008a52b4938241654cec2eac26f617e929"
REVIEWED_ISSUE218_ORGANIZATION_PATHS = (
    "internal/provider/organization_project_budget_authority_test.go",
    "internal/provider/organization_project_budget_safety_test.go",
    "internal/provider/resource_organization.go",
    "internal/provider/resource_organization_semantic_dictionary.go",
    "internal/provider/resource_organization_semantic_dictionary_protocol_test.go",
    "internal/provider/resource_organization_semantic_dictionary_test.go",
)
REVIEWED_ISSUE218_ORGANIZATION_RUNTIME_DIFF_SHA256 = (
    "ecee569303fea90c085584c8f15a9a928cb93ae7c10cd7c38b0f0f30c46dfff3"
)
REVIEWED_ISSUE218_PROJECT_BASE = "240836e5bfb1d071071c7404a3af8d24f8afb89f"
REVIEWED_ISSUE218_PROJECT_PATHS = (
    "internal/provider/organization_project_budget_lifecycle_protocol_test.go",
    "internal/provider/organization_project_budget_safety_test.go",
    "internal/provider/read_test.go",
    "internal/provider/resource_organization_semantic_dictionary.go",
    "internal/provider/resource_project.go",
    "internal/provider/resource_project_semantic_dictionary.go",
    "internal/provider/resource_project_semantic_dictionary_protocol_test.go",
    "internal/provider/resource_project_semantic_dictionary_test.go",
    "internal/provider/resource_project_semantic_issue218_protocol_test.go",
    "internal/provider/semantic_dictionary.go",
)
REVIEWED_ISSUE218_PROJECT_RUNTIME_DIFF_SHA256 = (
    "8a42a363b7410ebea6cdd4e7a3b550a062e10d02b88ec4126c2b570cb9d3873c"
)
REVIEWED_ISSUE218_TEAM_BASE = "4731b3909cba66ef796d64b4abe2e4f41f390d89"
REVIEWED_ISSUE218_TEAM_PATHS = (
    "internal/provider/resource_team.go",
    "internal/provider/resource_team_semantic_dictionary.go",
    "internal/provider/resource_team_semantic_dictionary_protocol_test.go",
    "internal/provider/resource_team_semantic_dictionary_test.go",
    "internal/provider/team_response.go",
)
REVIEWED_ISSUE218_TEAM_RUNTIME_DIFF_SHA256 = (
    "d63312ad12f026c12b3b865ae17ee74bacce960dfe7bae7ed72a2b75a32b68ad"
)
REVIEWED_ISSUE202_ACCESS_GROUP_BASE = "7ae6611f4cc83be6d9b1b3756faf2bcbbb77ccc9"
REVIEWED_ISSUE202_ACCESS_GROUP_PATHS = (
    "internal/provider/access_group_safe_read_protocol_test.go",
    "internal/provider/access_group_safe_read_test.go",
    "internal/provider/datasource_access_group.go",
    "internal/provider/resource_access_group.go",
)
REVIEWED_ISSUE202_ACCESS_GROUP_RUNTIME_DIFF_SHA256 = (
    "fce5c422ec5dcd9859337d8a2376193d4f6f8b0337f397a2d82bfbf7c4e391ae"
)
REVIEWED_ISSUE202_USER_BASE = "3e25ecfdf57276c2ea96cfacf21c5d0ec917990d"
REVIEWED_ISSUE202_USER_PATHS = (
    "internal/provider/datasource_user.go",
    "internal/provider/numeric_import_ownership_test.go",
    "internal/provider/numeric_import_protocol_additional_test.go",
    "internal/provider/resource_user.go",
    "internal/provider/user_safe_read_protocol_test.go",
    "internal/provider/user_safe_read_test.go",
)
REVIEWED_ISSUE202_USER_RUNTIME_DIFF_SHA256 = (
    "915a0810f0f8e3e93e50656dc943172a6df430a53e7a9b0ae3379c03384e54c1"
)
REVIEWED_ISSUE202_KEY_BASE = "85a5c1c713ddbd057e3794d53065915cb4557a06"
REVIEWED_ISSUE202_KEY_PATHS = (
    "internal/provider/datasource_key.go",
    "internal/provider/key_hash_validator.go",
    "internal/provider/key_safe_read_protocol_test.go",
    "internal/provider/key_safe_read_test.go",
    "internal/provider/resource_key.go",
    "internal/provider/resource_key_block.go",
    "internal/provider/resource_key_block_lifecycle_test.go",
    "internal/provider/resource_key_numeric_regression_test.go",
)
REVIEWED_ISSUE202_KEY_RUNTIME_DIFF_SHA256 = (
    "aa39212b05c2cf723d1fa0ace27bb22f2fd0af45e21fe734bfd0405863985cf5"
)
REVIEWED_ISSUE202_GUARDRAIL_BASE = "c1dec96bd3e11134da598c3b0fa0ea5589da05a9"
REVIEWED_ISSUE202_GUARDRAIL_PATHS = (
    "internal/provider/datasource_guardrail.go",
    "internal/provider/guardrail_contract_test.go",
    "internal/provider/guardrail_helpers.go",
    "internal/provider/guardrail_protocol_test.go",
    "internal/provider/guardrail_safe_read_protocol_test.go",
    "internal/provider/guardrail_safe_read_test.go",
    "internal/provider/read_test.go",
    "internal/provider/resource_guardrail.go",
    "internal/provider/resource_guardrail_test.go",
    "internal/provider/semantic_json_protocol_test.go",
)
REVIEWED_ISSUE202_GUARDRAIL_RUNTIME_DIFF_SHA256 = (
    "e944aef251fa4e4d42cf9318487dc64fb59049019ee1fd7eccf17baab0e17849"
)
REVIEWED_ISSUE202_PROMPT_BASE = "df8239fbd98fb3d695b71713893593c44b525607"
REVIEWED_ISSUE202_PROMPT_PATHS = (
    "internal/provider/prompt_helpers.go",
    "internal/provider/prompt_safe_read_protocol_test.go",
    "internal/provider/prompt_safe_read_test.go",
    "internal/provider/read_test.go",
    "internal/provider/resource_prompt.go",
)
REVIEWED_ISSUE202_PROMPT_RUNTIME_DIFF_SHA256 = (
    "9aea51dac8c907033bff9288d357c6f3b211a6708f60ec67904ba73b73a1a264"
)
REVIEWED_ISSUE178_BASE = "18ab40c133385a50bc1b892835646dc05ef1c59f"
REVIEWED_ISSUE178_PATHS = (
    "internal/provider/datasource_mcp_server.go",
    "internal/provider/datasource_mcp_servers_list.go",
    "internal/provider/mcp_audit_blockers_protocol_test.go",
    "internal/provider/mcp_field_lifecycle.go",
    "internal/provider/mcp_field_ownership.go",
    "internal/provider/mcp_field_ownership_test.go",
    "internal/provider/mcp_info_stage2_test.go",
    "internal/provider/mcp_issue178_fields_test.go",
    "internal/provider/mcp_issue215_parity_test.go",
    "internal/provider/mcp_update_completion_protocol_test.go",
    "internal/provider/resource_mcp_server.go",
    "internal/provider/resource_mcp_server_test.go",
)
REVIEWED_ISSUE178_RUNTIME_DIFF_SHA256 = (
    "764b1c378826095cb1acaca7e6e58997c54df72f3be7fa3dcca3414651d906f0"
)
REVIEWED_ISSUE208_SAFE_BASE = "9edfaa5282dce203978e9abb6d22157cd2ca78cc"
REVIEWED_ISSUE208_SAFE_PATHS = (
    "internal/provider/datasource_mcp_server.go",
    "internal/provider/datasource_mcp_servers_list.go",
    "internal/provider/mcp_audit_blockers_protocol_test.go",
    "internal/provider/mcp_field_lifecycle.go",
    "internal/provider/mcp_field_ownership.go",
    "internal/provider/mcp_field_ownership_test.go",
    "internal/provider/mcp_info_stage2_test.go",
    "internal/provider/mcp_issue178_fields_test.go",
    "internal/provider/mcp_issue208_safe_fields_test.go",
    "internal/provider/mcp_issue215_parity_protocol_test.go",
    "internal/provider/mcp_issue215_parity_test.go",
    "internal/provider/resource_mcp_server.go",
    "internal/provider/resource_mcp_server_test.go",
)
REVIEWED_ISSUE208_SAFE_RUNTIME_DIFF_SHA256 = (
    "c5548cc5d15d29a1b75671a5bbbcabee89a9303d07ca748e3fd6125555450abd"
)
REVIEWED_ISSUE208_ENV_VARS_BASE = "a4b00b6a77d627912a341b2883f1e13a02e09211"
REVIEWED_ISSUE208_ENV_VARS_PATHS = (
    "internal/provider/mcp_audit_blockers_protocol_test.go",
    "internal/provider/mcp_env_vars.go",
    "internal/provider/mcp_field_lifecycle.go",
    "internal/provider/mcp_field_ownership.go",
    "internal/provider/mcp_field_ownership_test.go",
    "internal/provider/mcp_info_stage2_test.go",
    "internal/provider/mcp_issue178_fields_test.go",
    "internal/provider/mcp_issue208_env_vars_test.go",
    "internal/provider/mcp_issue208_safe_fields_test.go",
    "internal/provider/mcp_issue215_parity_test.go",
    "internal/provider/resource_mcp_server.go",
    "internal/provider/resource_mcp_server_test.go",
)
REVIEWED_ISSUE208_ENV_VARS_RUNTIME_DIFF_SHA256 = (
    "778c12a202498e35a5919d11674497d8dcd447ca02c4b2c609c07b527ca16d31"
)
REVIEWED_ISSUE208_TOKEN_EXCHANGE_BASE = "0cd60df590b5aad6cf93629db292bf21a242b5b9"
REVIEWED_ISSUE208_TOKEN_EXCHANGE_PATHS = (
    "internal/provider/mcp_audit_blockers_protocol_test.go",
    "internal/provider/mcp_field_lifecycle.go",
    "internal/provider/mcp_field_ownership.go",
    "internal/provider/mcp_field_ownership_test.go",
    "internal/provider/mcp_info_stage2_test.go",
    "internal/provider/mcp_issue178_fields_test.go",
    "internal/provider/mcp_issue208_env_vars_test.go",
    "internal/provider/mcp_issue208_safe_fields_test.go",
    "internal/provider/mcp_issue208_token_exchange_protocol_test.go",
    "internal/provider/mcp_issue208_token_exchange_test.go",
    "internal/provider/mcp_issue215_parity_test.go",
    "internal/provider/mcp_server_lifecycle_protocol_test.go",
    "internal/provider/mcp_update_completion_protocol_test.go",
    "internal/provider/resource_mcp_server.go",
)
REVIEWED_ISSUE208_TOKEN_EXCHANGE_RUNTIME_DIFF_SHA256 = (
    "50e347c4db02128671bab10b0611592a88180dba2cf84bc176ccb994815d8b09"
)

REVIEWED_ISSUE207_TOOLSETS_BASE = "f4f369df2180805961d475866296a06c64f3a1c5"
REVIEWED_ISSUE207_TOOLSETS_PATHS = (
    "internal/provider/mcp_toolset_assignment_protocol_test.go",
    "internal/provider/mcp_toolset_assignments.go",
    "internal/provider/mcp_toolset_assignments_test.go",
    "internal/provider/mcp_toolset_lifecycle_protocol_test.go",
    "internal/provider/provider.go",
    "internal/provider/provider_smoke_test.go",
    "internal/provider/resource_key.go",
    "internal/provider/resource_key_semantic_dictionary.go",
    "internal/provider/resource_key_semantic_dictionary_test.go",
    "internal/provider/resource_mcp_toolset.go",
    "internal/provider/resource_mcp_toolset_test.go",
    "internal/provider/resource_team.go",
    "internal/provider/resource_team_semantic_dictionary.go",
    "internal/provider/resource_team_semantic_dictionary_protocol_test.go",
    "internal/provider/resource_team_semantic_dictionary_test.go",
    "internal/provider/team_response.go",
)
REVIEWED_ISSUE207_TOOLSETS_RUNTIME_DIFF_SHA256 = (
    "584b1463dfcfb418d0e5a117a1bb647261fdfa97438ce41ff09fb2da96574ef5"
)

REVIEWED_V198_RELEASE_BASE = "82b1d0ecc2ca413c39a4254c99c7f950268ea7c7"
REVIEWED_V198_RELEASE_PATHS = (
    'internal/provider/access_group_safe_read_protocol_test.go',
    'internal/provider/access_group_safe_read_test.go',
    'internal/provider/agent_block_schema_compatibility_test.go',
    'internal/provider/agent_clear_overlay_protocol_test.go',
    'internal/provider/agent_collection_block_projection_protocol_test.go',
    'internal/provider/agent_mcp_tool_permissions.go',
    'internal/provider/agent_mcp_tool_permissions_protocol_test.go',
    'internal/provider/agent_mcp_tool_permissions_test.go',
    'internal/provider/agent_ownership_pending_protocol_test.go',
    'internal/provider/agent_patch.go',
    'internal/provider/agent_structured.go',
    'internal/provider/agent_structured_test.go',
    'internal/provider/api_number_state.go',
    'internal/provider/api_numbers.go',
    'internal/provider/api_numbers_test.go',
    'internal/provider/budget_table_state.go',
    'internal/provider/build_request_test.go',
    'internal/provider/client.go',
    'internal/provider/client_test.go',
    'internal/provider/collection_conversion.go',
    'internal/provider/collection_conversion_audit_test.go',
    'internal/provider/collection_conversion_test.go',
    'internal/provider/credential_json.go',
    'internal/provider/datasource_access_group.go',
    'internal/provider/datasource_access_groups_list.go',
    'internal/provider/datasource_agent.go',
    'internal/provider/datasource_agents_list.go',
    'internal/provider/datasource_budget.go',
    'internal/provider/datasource_budgets_list.go',
    'internal/provider/datasource_completion_protocol_test.go',
    'internal/provider/datasource_credential.go',
    'internal/provider/datasource_fallback.go',
    'internal/provider/datasource_fallback_test.go',
    'internal/provider/datasource_guardrail.go',
    'internal/provider/datasource_guardrails_list.go',
    'internal/provider/datasource_jwt_key_mapping.go',
    'internal/provider/datasource_jwt_key_mappings_list.go',
    'internal/provider/datasource_key.go',
    'internal/provider/datasource_keys_list.go',
    'internal/provider/datasource_list_pagination_test.go',
    'internal/provider/datasource_list_presence_protocol_test.go',
    'internal/provider/datasource_mcp_presence_protocol_test.go',
    'internal/provider/datasource_mcp_presence_test.go',
    'internal/provider/datasource_mcp_server.go',
    'internal/provider/datasource_mcp_servers_list.go',
    'internal/provider/datasource_model.go',
    'internal/provider/datasource_models_list.go',
    'internal/provider/datasource_organization.go',
    'internal/provider/datasource_organizations_list.go',
    'internal/provider/datasource_presence.go',
    'internal/provider/datasource_presence_review_regression_test.go',
    'internal/provider/datasource_presence_test.go',
    'internal/provider/datasource_project.go',
    'internal/provider/datasource_projects_list.go',
    'internal/provider/datasource_prompt.go',
    'internal/provider/datasource_prompts_list.go',
    'internal/provider/datasource_prompts_list_test.go',
    'internal/provider/datasource_search_tool.go',
    'internal/provider/datasource_search_tool_test.go',
    'internal/provider/datasource_search_tools_list.go',
    'internal/provider/datasource_singular_presence_protocol_test.go',
    'internal/provider/datasource_tag.go',
    'internal/provider/datasource_tags_list.go',
    'internal/provider/datasource_team.go',
    'internal/provider/datasource_team_presence_protocol_test.go',
    'internal/provider/datasource_team_presence_test.go',
    'internal/provider/datasource_team_test.go',
    'internal/provider/datasource_teams_list.go',
    'internal/provider/datasource_unified_access_group.go',
    'internal/provider/datasource_unified_access_groups_list.go',
    'internal/provider/datasource_user.go',
    'internal/provider/datasource_users_list.go',
    'internal/provider/datasource_vector_store.go',
    'internal/provider/endpoint_helpers.go',
    'internal/provider/endpoint_helpers_test.go',
    'internal/provider/fallback_protocol_test.go',
    'internal/provider/guardrail_contract_test.go',
    'internal/provider/guardrail_helpers.go',
    'internal/provider/guardrail_mode_validator.go',
    'internal/provider/guardrail_protocol_test.go',
    'internal/provider/guardrail_safe_read_protocol_test.go',
    'internal/provider/guardrail_safe_read_test.go',
    'internal/provider/helpers_test.go',
    'internal/provider/http_classification.go',
    'internal/provider/http_classification_hold_test.go',
    'internal/provider/http_classification_latest_hold_test.go',
    'internal/provider/http_classification_regression_test.go',
    'internal/provider/http_classification_test.go',
    'internal/provider/http_diagnostics.go',
    'internal/provider/import_read_protocol_test.go',
    'internal/provider/import_read_validation.go',
    'internal/provider/invite_email.go',
    'internal/provider/invite_email_test.go',
    'internal/provider/json_request_precision_test.go',
    'internal/provider/jwt_key_mapping_adversarial_protocol_test.go',
    'internal/provider/jwt_key_mapping_api.go',
    'internal/provider/jwt_key_mapping_create_failure_protocol_test.go',
    'internal/provider/jwt_key_mapping_import_protocol_test.go',
    'internal/provider/jwt_key_mapping_protocol_test.go',
    'internal/provider/jwt_key_mapping_safe_read_protocol_test.go',
    'internal/provider/jwt_key_mapping_safe_read_test.go',
    'internal/provider/jwt_key_mapping_test.go',
    'internal/provider/key_hash_validator.go',
    'internal/provider/key_safe_read_protocol_test.go',
    'internal/provider/key_safe_read_test.go',
    'internal/provider/list_helpers.go',
    'internal/provider/list_helpers_test.go',
    'internal/provider/list_number_precision_test.go',
    'internal/provider/mcp_audit_blockers_protocol_test.go',
    'internal/provider/mcp_cost_leaf_preservation_protocol_test.go',
    'internal/provider/mcp_env_vars.go',
    'internal/provider/mcp_field_lifecycle.go',
    'internal/provider/mcp_field_ownership.go',
    'internal/provider/mcp_field_ownership_test.go',
    'internal/provider/mcp_identity_v198_protocol_test.go',
    'internal/provider/mcp_info_corruption_protocol_test.go',
    'internal/provider/mcp_info_create_recovery_protocol_test.go',
    'internal/provider/mcp_info_json.go',
    'internal/provider/mcp_info_json_test.go',
    'internal/provider/mcp_info_lifecycle.go',
    'internal/provider/mcp_info_plan.go',
    'internal/provider/mcp_info_provenance.go',
    'internal/provider/mcp_info_provenance_test.go',
    'internal/provider/mcp_info_stage2_protocol_test.go',
    'internal/provider/mcp_info_stage2_test.go',
    'internal/provider/mcp_info_stage3_protocol_additional_test.go',
    'internal/provider/mcp_info_stage3_test.go',
    'internal/provider/mcp_info_state_upgrade_protocol_test.go',
    'internal/provider/mcp_issue178_fields_test.go',
    'internal/provider/mcp_issue208_env_vars_test.go',
    'internal/provider/mcp_issue208_safe_fields_test.go',
    'internal/provider/mcp_issue208_token_exchange_protocol_test.go',
    'internal/provider/mcp_issue208_token_exchange_test.go',
    'internal/provider/mcp_issue215_parity_protocol_test.go',
    'internal/provider/mcp_issue215_parity_test.go',
    'internal/provider/mcp_server_datasource_protocol_test.go',
    'internal/provider/mcp_server_import_projection_protocol_test.go',
    'internal/provider/mcp_server_lifecycle_protocol_test.go',
    'internal/provider/mcp_server_phantom_protocol_test.go',
    'internal/provider/mcp_update_completion_protocol_test.go',
    'internal/provider/metadata_helpers.go',
    'internal/provider/metadata_helpers_test.go',
    'internal/provider/numeric_import_ownership_test.go',
    'internal/provider/numeric_import_protocol_additional_test.go',
    'internal/provider/numeric_import_protocol_test.go',
    'internal/provider/numeric_map_validation_test.go',
    'internal/provider/numeric_unconfigured_ownership_test.go',
    'internal/provider/organization_project_budget_authority_test.go',
    'internal/provider/organization_project_budget_lifecycle_protocol_test.go',
    'internal/provider/organization_project_budget_plan.go',
    'internal/provider/organization_project_budget_safety_test.go',
    'internal/provider/organization_project_import_ownership_protocol_test.go',
    'internal/provider/prompt_contract_test.go',
    'internal/provider/prompt_helpers.go',
    'internal/provider/prompt_protocol_test.go',
    'internal/provider/prompt_safe_read_protocol_test.go',
    'internal/provider/prompt_safe_read_test.go',
    'internal/provider/provider.go',
    'internal/provider/provider_smoke_test.go',
    'internal/provider/read_test.go',
    'internal/provider/request_enum_validators_test.go',
    'internal/provider/request_json.go',
    'internal/provider/request_numeric_maps.go',
    'internal/provider/resource_access_group.go',
    'internal/provider/resource_access_group_test.go',
    'internal/provider/resource_agent.go',
    'internal/provider/resource_agent_lifecycle.go',
    'internal/provider/resource_agent_lifecycle_test.go',
    'internal/provider/resource_agent_test.go',
    'internal/provider/resource_budget.go',
    'internal/provider/resource_credential.go',
    'internal/provider/resource_credential_test.go',
    'internal/provider/resource_fallback.go',
    'internal/provider/resource_fallback_test.go',
    'internal/provider/resource_guardrail.go',
    'internal/provider/resource_guardrail_test.go',
    'internal/provider/resource_jwt_key_mapping.go',
    'internal/provider/resource_key.go',
    'internal/provider/resource_key_block.go',
    'internal/provider/resource_key_block_lifecycle_test.go',
    'internal/provider/resource_key_block_test.go',
    'internal/provider/resource_key_numeric_regression_test.go',
    'internal/provider/resource_key_router_settings.go',
    'internal/provider/resource_key_router_settings_test.go',
    'internal/provider/resource_key_semantic_dictionary.go',
    'internal/provider/resource_key_semantic_dictionary_protocol_test.go',
    'internal/provider/resource_key_semantic_dictionary_test.go',
    'internal/provider/resource_key_test.go',
    'internal/provider/resource_mcp_server.go',
    'internal/provider/resource_mcp_server_test.go',
    'internal/provider/resource_model.go',
    'internal/provider/resource_model_clear_protocol_test.go',
    'internal/provider/resource_model_clear_test.go',
    'internal/provider/resource_model_info_validator.go',
    'internal/provider/resource_model_litellm_params_dictionary.go',
    'internal/provider/resource_model_litellm_params_dictionary_protocol_test.go',
    'internal/provider/resource_model_litellm_params_dictionary_test.go',
    'internal/provider/resource_model_plan_modifier.go',
    'internal/provider/resource_model_plan_modifier_test.go',
    'internal/provider/resource_model_semantic_dictionary.go',
    'internal/provider/resource_model_semantic_dictionary_protocol_test.go',
    'internal/provider/resource_model_semantic_dictionary_test.go',
    'internal/provider/resource_model_test.go',
    'internal/provider/resource_model_update_consistency_test.go',
    'internal/provider/resource_organization.go',
    'internal/provider/resource_organization_member.go',
    'internal/provider/resource_organization_member_test.go',
    'internal/provider/resource_organization_numeric_test.go',
    'internal/provider/resource_organization_semantic_dictionary.go',
    'internal/provider/resource_organization_semantic_dictionary_protocol_test.go',
    'internal/provider/resource_organization_semantic_dictionary_test.go',
    'internal/provider/resource_project.go',
    'internal/provider/resource_project_semantic_dictionary.go',
    'internal/provider/resource_project_semantic_dictionary_protocol_test.go',
    'internal/provider/resource_project_semantic_dictionary_test.go',
    'internal/provider/resource_project_semantic_issue218_protocol_test.go',
    'internal/provider/resource_project_test.go',
    'internal/provider/resource_prompt.go',
    'internal/provider/resource_search_tool.go',
    'internal/provider/resource_tag.go',
    'internal/provider/resource_team.go',
    'internal/provider/resource_team_block.go',
    'internal/provider/resource_team_limit_type_lifecycle_test.go',
    'internal/provider/resource_team_member.go',
    'internal/provider/resource_team_member_add.go',
    'internal/provider/resource_team_member_add_test.go',
    'internal/provider/resource_team_member_test.go',
    'internal/provider/resource_team_semantic_dictionary.go',
    'internal/provider/resource_team_semantic_dictionary_protocol_test.go',
    'internal/provider/resource_team_semantic_dictionary_test.go',
    'internal/provider/resource_team_test.go',
    'internal/provider/resource_unified_access_group.go',
    'internal/provider/resource_unified_access_group_test.go',
    'internal/provider/resource_user.go',
    'internal/provider/resource_user_test.go',
    'internal/provider/resource_vector_store.go',
    'internal/provider/safe_read_retry.go',
    'internal/provider/search_tool_safe_read_protocol_test.go',
    'internal/provider/search_tool_safe_read_test.go',
    'internal/provider/semantic_dictionary.go',
    'internal/provider/semantic_dictionary_test.go',
    'internal/provider/semantic_json.go',
    'internal/provider/semantic_json_lifecycle_protocol_test.go',
    'internal/provider/semantic_json_protocol_test.go',
    'internal/provider/semantic_json_test.go',
    'internal/provider/strict_collection_request_builders_test.go',
    'internal/provider/strict_collection_response_stage4_test.go',
    'internal/provider/strict_collection_stage3_test.go',
    'internal/provider/strict_collection_team_mcp_test.go',
    'internal/provider/tag_budget_helpers.go',
    'internal/provider/tag_budget_protocol_test.go',
    'internal/provider/tag_budget_table_test.go',
    'internal/provider/team_response.go',
    'internal/provider/team_response_protocol_test.go',
    'internal/provider/team_response_test.go',
    'internal/provider/testdata/agent-schema-origin-issues.golden.json',
    'internal/provider/user_safe_read_protocol_test.go',
    'internal/provider/user_safe_read_test.go',
    'internal/provider/vector_store_contract_test.go',
    'internal/provider/vector_store_helpers.go',
    'internal/provider/vector_store_protocol_test.go',
    'main.go',
)
REVIEWED_V198_RELEASE_RUNTIME_DIFF_SHA256 = (
    "306e6f0d86dc5949599126161e68db41f2e289a12906a414f3335f871aabdccb"
)


def git(*args: str) -> str:
    proc = subprocess.run(
        ["git", *args], stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
        stderr=subprocess.PIPE, text=True, check=False, timeout=30,
    )
    if proc.returncode:
        raise RuntimeError("required git runtime-parity operation failed")
    return proc.stdout.strip()


def git_bytes(*args: str) -> bytes:
    proc = subprocess.run(
        ["git", *args], stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
        stderr=subprocess.PIPE, check=False, timeout=30,
    )
    if proc.returncode:
        raise RuntimeError("required git runtime-parity operation failed")
    return proc.stdout


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--event", required=True, choices=("pull_request", "workflow_dispatch", "push", "release"))
    parser.add_argument("--base", required=True)
    parser.add_argument("--head", required=True)
    args = parser.parse_args()
    if not args.base or args.base == ZERO_SHA or not args.head or args.head == ZERO_SHA:
        raise RuntimeError("event did not provide a usable base and head SHA")
    base = git("rev-parse", "--verify", f"{args.base}^{{commit}}")
    head = git("rev-parse", "--verify", f"{args.head}^{{commit}}")
    merge_base = git("merge-base", base, head)
    if not merge_base:
        raise RuntimeError("event base and candidate have no merge base")
    # PRs are bound to the event's immutable base SHA. Other events supply a
    # checked event predecessor/default-branch commit and are bound to its
    # actual merge-base. Never substitute a repository-hardcoded SHA.
    comparison = base if args.event == "pull_request" else merge_base
    changed = git("diff", "--name-only", comparison, head, "--", *RUNTIME_PATHS)
    if changed:
        changed_paths = tuple(changed.splitlines())
        patch = git_bytes(
            "diff", "--binary", comparison, head, "--", *RUNTIME_PATHS
        )
        digest = hashlib.sha256(patch).hexdigest()
        reviewed = None
        if (
            comparison in REVIEWED_ISSUE210_BASES
            and changed_paths == REVIEWED_ISSUE210_PATHS
            and digest == REVIEWED_ISSUE210_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue210"
        elif (
            comparison == REVIEWED_ISSUE217_BASE
            and changed_paths == REVIEWED_ISSUE217_PATHS
            and digest == REVIEWED_ISSUE217_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue217"
        elif (
            comparison == REVIEWED_ISSUE213_BASE
            and changed_paths == REVIEWED_ISSUE213_PATHS
            and digest == REVIEWED_ISSUE213_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue213"
        elif (
            comparison == REVIEWED_ISSUE222_BASE
            and changed_paths == REVIEWED_ISSUE222_PATHS
            and digest == REVIEWED_ISSUE222_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue222"
        elif (
            comparison == REVIEWED_ISSUE202_PHASE2_BASE
            and changed_paths == REVIEWED_ISSUE202_PHASE2_PATHS
            and digest == REVIEWED_ISSUE202_PHASE2_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue202-phase2"
        elif (
            comparison == REVIEWED_ISSUE202_SEARCH_TOOL_BASE
            and changed_paths == REVIEWED_ISSUE202_SEARCH_TOOL_PATHS
            and digest == REVIEWED_ISSUE202_SEARCH_TOOL_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue202-search-tool"
        elif (
            comparison == REVIEWED_ISSUE212_BASE
            and changed_paths == REVIEWED_ISSUE212_PATHS
            and digest == REVIEWED_ISSUE212_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue212"
        elif (
            comparison == REVIEWED_ISSUE214_BASE
            and changed_paths == REVIEWED_ISSUE214_PATHS
            and digest == REVIEWED_ISSUE214_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue214"
        elif (
            comparison == REVIEWED_ISSUE224_BASE
            and changed_paths == REVIEWED_ISSUE224_PATHS
            and digest == REVIEWED_ISSUE224_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue224"
        elif (
            comparison == REVIEWED_ISSUE215_BASE
            and changed_paths == REVIEWED_ISSUE215_PATHS
            and digest == REVIEWED_ISSUE215_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue215"
        elif (
            comparison == REVIEWED_ISSUE218_FOUNDATION_BASE
            and changed_paths == REVIEWED_ISSUE218_FOUNDATION_PATHS
            and digest == REVIEWED_ISSUE218_FOUNDATION_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue218-foundation"
        elif (
            comparison == REVIEWED_ISSUE218_MODEL_BASE
            and changed_paths == REVIEWED_ISSUE218_MODEL_PATHS
            and digest == REVIEWED_ISSUE218_MODEL_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue218-model"
        elif (
            comparison == REVIEWED_ISSUE218_MODEL_PARAMS_BASE
            and changed_paths == REVIEWED_ISSUE218_MODEL_PARAMS_PATHS
            and digest == REVIEWED_ISSUE218_MODEL_PARAMS_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue218-model-params"
        elif (
            comparison == REVIEWED_ISSUE218_KEY_BASE
            and changed_paths == REVIEWED_ISSUE218_KEY_PATHS
            and digest == REVIEWED_ISSUE218_KEY_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue218-key"
        elif (
            comparison == REVIEWED_ISSUE218_ORGANIZATION_BASE
            and changed_paths == REVIEWED_ISSUE218_ORGANIZATION_PATHS
            and digest == REVIEWED_ISSUE218_ORGANIZATION_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue218-organization"
        elif (
            comparison == REVIEWED_ISSUE218_PROJECT_BASE
            and changed_paths == REVIEWED_ISSUE218_PROJECT_PATHS
            and digest == REVIEWED_ISSUE218_PROJECT_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue218-project"
        elif (
            comparison == REVIEWED_ISSUE218_TEAM_BASE
            and changed_paths == REVIEWED_ISSUE218_TEAM_PATHS
            and digest == REVIEWED_ISSUE218_TEAM_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue218-team"
        elif (
            comparison == REVIEWED_ISSUE202_ACCESS_GROUP_BASE
            and changed_paths == REVIEWED_ISSUE202_ACCESS_GROUP_PATHS
            and digest == REVIEWED_ISSUE202_ACCESS_GROUP_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue202-access-group"
        elif (
            comparison == REVIEWED_ISSUE202_USER_BASE
            and changed_paths == REVIEWED_ISSUE202_USER_PATHS
            and digest == REVIEWED_ISSUE202_USER_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue202-user"
        elif (
            comparison == REVIEWED_ISSUE202_KEY_BASE
            and changed_paths == REVIEWED_ISSUE202_KEY_PATHS
            and digest == REVIEWED_ISSUE202_KEY_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue202-key"
        elif (
            comparison == REVIEWED_ISSUE202_GUARDRAIL_BASE
            and changed_paths == REVIEWED_ISSUE202_GUARDRAIL_PATHS
            and digest == REVIEWED_ISSUE202_GUARDRAIL_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue202-guardrail"
        elif (
            comparison == REVIEWED_ISSUE202_PROMPT_BASE
            and changed_paths == REVIEWED_ISSUE202_PROMPT_PATHS
            and digest == REVIEWED_ISSUE202_PROMPT_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue202-prompt"
        elif (
            comparison == REVIEWED_ISSUE178_BASE
            and changed_paths == REVIEWED_ISSUE178_PATHS
            and digest == REVIEWED_ISSUE178_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue178"
        elif (
            comparison == REVIEWED_ISSUE208_SAFE_BASE
            and changed_paths == REVIEWED_ISSUE208_SAFE_PATHS
            and digest == REVIEWED_ISSUE208_SAFE_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue208-safe"
        elif (
            comparison == REVIEWED_ISSUE208_ENV_VARS_BASE
            and changed_paths == REVIEWED_ISSUE208_ENV_VARS_PATHS
            and digest == REVIEWED_ISSUE208_ENV_VARS_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue208-env-vars"
        elif (
            comparison == REVIEWED_ISSUE208_TOKEN_EXCHANGE_BASE
            and changed_paths == REVIEWED_ISSUE208_TOKEN_EXCHANGE_PATHS
            and digest == REVIEWED_ISSUE208_TOKEN_EXCHANGE_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue208-token-exchange"
        elif (
            comparison == REVIEWED_ISSUE207_TOOLSETS_BASE
            and changed_paths == REVIEWED_ISSUE207_TOOLSETS_PATHS
            and digest == REVIEWED_ISSUE207_TOOLSETS_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue207-toolsets"
        elif (
            comparison == REVIEWED_V198_RELEASE_BASE
            and changed_paths == REVIEWED_V198_RELEASE_PATHS
            and digest == REVIEWED_V198_RELEASE_RUNTIME_DIFF_SHA256
        ):
            reviewed = "v1.98-release"
        if reviewed is not None:
            print(
                f"Provider runtime parity verified: reviewed={reviewed} "
                f"base={base} merge_base={merge_base} head={head}"
            )
            return 0
        print("Provider runtime differs from the actual event base:", file=sys.stderr)
        for path in changed.splitlines():
            print(path, file=sys.stderr)
        return 1
    print(f"Provider runtime parity verified: event={args.event} base={base} merge_base={merge_base} head={head}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (RuntimeError, subprocess.SubprocessError) as error:
        print(f"Runtime parity failed: {error}", file=sys.stderr)
        raise SystemExit(1)
