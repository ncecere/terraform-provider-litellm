# litellm_mcp_server - heterogeneous MCP info JSON lifecycle

resource "litellm_mcp_server" "json_info" {
  server_name = "test_mcp_json_info"
  url         = "https://example.com/mcp-json"
  transport   = "sse"

  mcp_info_json = jsonencode({
    access_control = true
    owner = {
      team = "platform"
      tier = 2
    }
    exact_integer = 9007199254740993
    nested_null   = null
    capabilities  = ["search", { streaming = false }]
  })
}

resource "litellm_mcp_server" "json_selective" {
  server_name = "test_mcp_json_selective"
  url         = "https://example.com/mcp-selective"
  transport   = "sse"

  mcp_info_overrides_json = jsonencode({
    access_control = false
    owner = {
      team = "security"
    }
  })
  mcp_info_clear_paths = ["/obsolete"]
}

output "mcp_server_json_info_id" {
  value = litellm_mcp_server.json_info.id
}

output "mcp_server_json_selective_id" {
  value = litellm_mcp_server.json_selective.id
}
