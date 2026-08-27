# data.litellm_mcp_servers - Lists all MCP servers

data "litellm_mcp_servers" "all" {
  # Keep combined resource/data-source smoke runs deterministic: inventory is
  # read after the fixture server exists, so the no-drift plan sees the same set.
  depends_on = [litellm_mcp_server.minimal]
}

output "ds_mcp_servers_list" {
  value     = data.litellm_mcp_servers.all
  sensitive = true
}
