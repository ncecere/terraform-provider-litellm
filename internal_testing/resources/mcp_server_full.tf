# litellm_mcp_server - Full
# All attributes populated

resource "litellm_mcp_server" "full" {
  server_name    = "test-mcp-full"
  alias          = "mcp-full"
  description    = "Full test MCP server"
  url            = "https://example.com/mcp-full"
  transport      = "sse"
  spec_version   = "2024-11-05"
  auth_type      = "bearer"
  allow_all_keys = true

  mcp_access_groups = ["test-access-group-full"]
  allowed_tools     = ["tool1", "tool2"]
  args              = []

  env = {
    "ENV_VAR" = "test-value"
  }

  credentials = {
    "token" = "fake-bearer-token"
  }

  extra_headers = {
    "X-Custom-Header" = "custom-value"
  }

  static_headers = {
    "X-Static" = "static-value"
  }

  mcp_info {
    server_name = "Full MCP Server"
    description = "A fully configured MCP server for testing"
    logo_url    = "https://example.com/logo.png"

    mcp_server_cost_info {
      default_cost_per_query = 0.01

      tool_name_to_cost_per_query = {
        "tool1" = 0.02
        "tool2" = 0.005
      }
    }
  }
}

output "mcp_server_full_id" {
  value = litellm_mcp_server.full.id
}

output "mcp_server_full_created_at" {
  value = litellm_mcp_server.full.created_at
}
