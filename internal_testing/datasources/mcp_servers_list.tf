# data.litellm_mcp_servers - Lists all MCP servers

data "litellm_mcp_servers" "all" {
}

output "ds_mcp_servers_list" {
  value = data.litellm_mcp_servers.all
}
