# litellm_mcp_server - Minimal
# Only required attributes

resource "litellm_mcp_server" "minimal" {
  server_name = "test_mcp_minimal"
  url         = "https://example.com/mcp"
  transport   = "sse"
}

output "mcp_server_minimal_id" {
  value = litellm_mcp_server.minimal.id
}
