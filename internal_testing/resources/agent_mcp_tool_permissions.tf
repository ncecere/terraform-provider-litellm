# litellm_agent - MCP tool permissions
# Exercises the schema-compatible map(string) JSON bridge against a real MCP server ID.

resource "litellm_agent" "mcp_tool_permissions" {
  agent_name = "test-agent-mcp-tool-permissions"

  agent_card {
    name = "Test Agent MCP Tool Permissions"
    url  = "https://agent.example.com/a2a"
  }

  object_permission {
    mcp_servers       = [litellm_mcp_server.minimal.id]
    mcp_access_groups = []
    models            = []
    agents            = []
    mcp_tool_permissions = {
      (litellm_mcp_server.minimal.id) = jsonencode(["list_tools", "call_tool"])
    }
  }
}

output "agent_mcp_tool_permissions_id" {
  value = litellm_agent.mcp_tool_permissions.id
}
