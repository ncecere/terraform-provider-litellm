# litellm_mcp_server - Full
# All attributes populated

resource "litellm_mcp_server" "full" {
  server_name                  = "test_mcp_full"
  alias                        = "mcp_full"
  description                  = "Full test MCP server"
  url                          = "https://example.com/mcp-full"
  transport                    = "sse"
  auth_type                    = "oauth2"
  oauth2_flow                  = "authorization_code"
  oauth_scopes                 = ["mcp.read", "mcp.write"]
  available_on_public_internet = false
  instructions                 = ""
  allow_all_keys               = true

  mcp_access_groups = ["test-access-group-full"]
  allowed_tools     = ["tool1", "tool2"]
  args              = []

  env = {
    "ENV_VAR" = "test-value"
  }

  authorization_url = "https://auth.example.com/oauth/authorize"
  token_url         = "https://auth.example.com/oauth/token"
  registration_url  = "https://auth.example.com/oauth/register"

  credentials = {
    "client_id"     = "test-client"
    "client_secret" = "fake-client-secret"
  }

  tool_name_to_display_name = {
    "tool1" = "Tool_One"
    "tool2" = "Tool_Two"
  }

  tool_name_to_description = {
    "tool1" = "First test tool"
    "tool2" = "Second test tool"
  }

  extra_headers = ["X-Custom-Header"]

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

output "mcp_server_full_updated_at" {
  value = litellm_mcp_server.full.updated_at
}

output "mcp_server_full_updated_by" {
  value = litellm_mcp_server.full.updated_by
}

output "mcp_server_full_oauth_scopes" {
  value     = litellm_mcp_server.full.oauth_scopes
  sensitive = true
}

output "mcp_server_full_oauth2_flow" {
  value = litellm_mcp_server.full.oauth2_flow
}
