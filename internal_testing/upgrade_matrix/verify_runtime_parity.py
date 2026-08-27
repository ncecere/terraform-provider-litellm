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
            comparison == REVIEWED_ISSUE212_BASE
            and changed_paths == REVIEWED_ISSUE212_PATHS
            and digest == REVIEWED_ISSUE212_RUNTIME_DIFF_SHA256
        ):
            reviewed = "issue212"
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
