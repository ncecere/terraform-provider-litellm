# Disposable pinned-v1.98 identity, normalization, and fallback coverage.
resource "litellm_mcp_server" "custom_identity" {
  server_id = "terraform_acceptance.custom_identity"
  alias     = "terraform acceptance alias"
  transport = "http"
  url       = "https://example.com/mcp/custom-identity"
}

resource "litellm_mcp_server" "unnamed" {
  transport = "http"
  url       = "https://example.com/mcp/unnamed"
}

output "mcp_custom_identity_id" {
  value = litellm_mcp_server.custom_identity.id
}

output "mcp_custom_identity_alias" {
  value = litellm_mcp_server.custom_identity.alias
}

output "mcp_unnamed_id" {
  value = litellm_mcp_server.unnamed.id
}
