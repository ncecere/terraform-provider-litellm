# Phased, loopback-only v1.98 MCP clear acceptance fixture.
# The acceptance harness applies the default set phase, then the cleared phase,
# repeats refresh/no-drift checks, removes state, imports, and repeats them.

variable "mcp_clear_phase" {
  type    = string
  default = "set"

  validation {
    condition     = contains(["set", "cleared"], var.mcp_clear_phase)
    error_message = "MCP clear phase must be set or cleared."
  }
}

locals {
  mcp_clear_set = var.mcp_clear_phase == "set"
}

resource "litellm_mcp_server" "clear_lifecycle" {
  server_name = "test_mcp_clear_lifecycle"
  url         = "https://example.com/mcp-clear-lifecycle"
  transport   = "http"
  auth_type   = "true_passthrough"

  alias             = local.mcp_clear_set ? "clear_alias" : null
  description       = local.mcp_clear_set ? "clear description" : null
  command           = local.mcp_clear_set ? "node" : null
  authorization_url = local.mcp_clear_set ? "https://auth.example.com/authorize" : null
  token_url         = local.mcp_clear_set ? "https://auth.example.com/token" : null
  registration_url  = local.mcp_clear_set ? "https://auth.example.com/register" : null
  mcp_access_groups = local.mcp_clear_set ? ["clear-group"] : null
  args              = local.mcp_clear_set ? ["server.js"] : null
  env               = local.mcp_clear_set ? { CLEAR_ENV = "set" } : null
  env_vars = local.mcp_clear_set ? [
    {
      name        = "CLEAR_GLOBAL"
      value       = "clear-value"
      scope       = "global"
      description = "Clear test credential"
    },
    {
      name        = "CLEAR_USER"
      scope       = "user"
      description = "Per-user clear test credential"
    }
  ] : null
  allowed_tools                = local.mcp_clear_set ? ["clear_tool"] : null
  extra_headers                = local.mcp_clear_set ? ["X-Clear"] : null
  static_headers               = local.mcp_clear_set ? { "X-Clear-Static" = "set" } : null
  allow_all_keys               = local.mcp_clear_set ? true : null
  oauth_scopes                 = local.mcp_clear_set ? ["clear.scope"] : null
  available_on_public_internet = local.mcp_clear_set ? false : null
  oauth2_flow                  = local.mcp_clear_set ? "authorization_code" : null
  instructions                 = local.mcp_clear_set ? "" : null
  tool_name_to_display_name    = local.mcp_clear_set ? { clear_tool = "Clear_Tool" } : null
  tool_name_to_description     = local.mcp_clear_set ? { clear_tool = "Clear test tool" } : null
  delegate_auth_to_upstream    = local.mcp_clear_set ? false : null
  oauth_passthrough            = local.mcp_clear_set ? false : null
  dcr_bridge                   = local.mcp_clear_set ? true : null
  is_byok                      = local.mcp_clear_set ? true : null
  byok_description             = local.mcp_clear_set ? ["Clear test setup"] : null
  byok_api_key_help_url        = local.mcp_clear_set ? "https://example.com/help" : null
  source_url                   = local.mcp_clear_set ? "https://example.com/source" : null
  timeout                      = local.mcp_clear_set ? 12.5 : null
  max_concurrent_requests      = local.mcp_clear_set ? 4 : null
}

output "mcp_clear_lifecycle_id" {
  value = litellm_mcp_server.clear_lifecycle.id
}
