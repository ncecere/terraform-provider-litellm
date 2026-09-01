# litellm_mcp_toolset - Populated and empty definitions

resource "litellm_mcp_toolset" "populated" {
  toolset_name = "test-mcp-toolset-populated"
  description  = "Acceptance lifecycle toolset"

  tools = [
    {
      server_id = litellm_mcp_server.minimal.id
      tool_name = "test_tool"
    }
  ]
}

resource "litellm_mcp_toolset" "empty" {
  toolset_name = "test-mcp-toolset-empty"
}

resource "litellm_team" "mcp_toolset" {
  team_alias      = "test-mcp-toolset-team"
  mcp_toolset_ids = [litellm_mcp_toolset.populated.toolset_id]
}

resource "litellm_key" "mcp_toolset" {
  key_alias       = "test-mcp-toolset-key"
  team_id         = litellm_team.mcp_toolset.id
  mcp_toolset_ids = [litellm_mcp_toolset.populated.toolset_id]
}

output "mcp_toolset_populated_id" {
  value = litellm_mcp_toolset.populated.toolset_id
}

output "mcp_toolset_empty_id" {
  value = litellm_mcp_toolset.empty.toolset_id
}
