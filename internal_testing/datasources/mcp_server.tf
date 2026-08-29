# data.litellm_mcp_server - Looks up an MCP server by server_id
# Note: server_id must reference an existing MCP server

data "litellm_mcp_server" "lookup" {
  server_id = litellm_mcp_server.minimal.server_id
}

output "ds_mcp_server_name" {
  value = data.litellm_mcp_server.lookup.server_name
}

output "ds_mcp_server_url" {
  value     = data.litellm_mcp_server.lookup.url
  sensitive = true
}

output "ds_mcp_server_transport" {
  value = data.litellm_mcp_server.lookup.transport
}

output "ds_mcp_server_auth_type" {
  value = data.litellm_mcp_server.lookup.auth_type
}

output "ds_mcp_server_created_at" {
  value = data.litellm_mcp_server.lookup.created_at
}

output "ds_mcp_server_available_on_public_internet" {
  value = data.litellm_mcp_server.lookup.available_on_public_internet
}

output "ds_mcp_server_oauth2_flow" {
  value = data.litellm_mcp_server.lookup.oauth2_flow
}

output "ds_mcp_server_instructions" {
  value = data.litellm_mcp_server.lookup.instructions
}
