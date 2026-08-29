# Canonical MCP token-exchange columns. The fixture exercises management
# lifecycle only; its example endpoints are never contacted by the smoke test.
resource "litellm_mcp_server" "token_exchange" {
  server_name = "test_mcp_token_exchange"
  alias       = "mcp_token_exchange"
  description = "Canonical RFC 8693 token-exchange test server"
  url         = "https://example.com/mcp-token-exchange"
  transport   = "http"
  auth_type   = "oauth2_token_exchange"

  issuer                  = "https://identity.example.com"
  token_exchange_endpoint = "https://identity.example.com/oauth2/token"
  audience                = "api://test-mcp"
  subject_token_type      = "urn:ietf:params:oauth:token-type:access_token"
  token_exchange_profile  = "rfc8693"
  oauth_scopes            = ["mcp.read"]

  credentials = {
    client_id                  = "test-token-exchange-client"
    client_secret              = "fake-token-exchange-secret"
    token_endpoint_auth_method = "client_secret_basic"
  }
}

output "mcp_server_token_exchange_id" {
  value = litellm_mcp_server.token_exchange.server_id
}

output "mcp_server_token_exchange_issuer" {
  value     = litellm_mcp_server.token_exchange.issuer
  sensitive = true
}

output "mcp_server_token_exchange_endpoint" {
  value     = litellm_mcp_server.token_exchange.token_exchange_endpoint
  sensitive = true
}

output "mcp_server_token_exchange_profile" {
  value = litellm_mcp_server.token_exchange.token_exchange_profile
}
