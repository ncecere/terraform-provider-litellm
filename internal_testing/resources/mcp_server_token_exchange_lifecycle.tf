variable "mcp_token_exchange_phase" {
  type    = string
  default = "configured"

  validation {
    condition     = contains(["configured", "cleared"], var.mcp_token_exchange_phase)
    error_message = "MCP token exchange phase must be configured or cleared."
  }
}

locals {
  mcp_token_exchange_configured = var.mcp_token_exchange_phase == "configured"
}

resource "litellm_mcp_server" "token_exchange_lifecycle" {
  server_name = "test_mcp_token_exchange_lifecycle"
  alias       = local.mcp_token_exchange_configured ? "mcp_token_exchange_lifecycle" : null
  description = local.mcp_token_exchange_configured ? "Canonical token-exchange set and clear lifecycle" : null
  url         = "https://example.com/mcp-token-exchange-lifecycle"
  transport   = "http"
  auth_type   = "oauth2_token_exchange"

  issuer                  = local.mcp_token_exchange_configured ? "https://identity.example.com" : null
  token_exchange_endpoint = local.mcp_token_exchange_configured ? "https://identity.example.com/oauth2/token" : null
  audience                = local.mcp_token_exchange_configured ? "api://lifecycle" : null
  subject_token_type      = local.mcp_token_exchange_configured ? "urn:ietf:params:oauth:token-type:access_token" : null
  token_exchange_profile  = local.mcp_token_exchange_configured ? "rfc8693" : null
  oauth_scopes            = local.mcp_token_exchange_configured ? ["mcp.read"] : null

  credentials = {
    client_id                  = "test-token-exchange-lifecycle"
    client_secret              = "fake-token-exchange-lifecycle-secret"
    token_endpoint_auth_method = "client_secret_basic"
  }
}

output "mcp_token_exchange_lifecycle_id" {
  value = litellm_mcp_server.token_exchange_lifecycle.server_id
}
