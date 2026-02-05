# litellm_mcp_server Data Source

Retrieves information about a specific LiteLLM MCP (Model Context Protocol) server.

## Example Usage

### Minimal Example

```hcl
data "litellm_mcp_server" "existing" {
  server_id = "server-xxxxxxxxxxxx"
}
```

### Full Example

```hcl
data "litellm_mcp_server" "github" {
  server_id = var.github_mcp_server_id
}

output "mcp_server_info" {
  value = {
    name      = data.litellm_mcp_server.github.server_name
    url       = data.litellm_mcp_server.github.url
    transport = data.litellm_mcp_server.github.transport
    status    = data.litellm_mcp_server.github.status
  }
}

# Check health status
output "is_healthy" {
  value = data.litellm_mcp_server.github.health_check_error == ""
}
```

## Argument Reference

The following arguments are supported:

* `server_id` - (Required) The unique identifier of the MCP server to retrieve.

## Attribute Reference

The following attributes are exported:

* `id` - The unique identifier of the MCP server.
* `server_id` - The server ID.
* `server_name` - The server name.
* `alias` - Server alias.
* `description` - Server description.
* `url` - Server URL.
* `transport` - Transport type (http, sse, stdio).
* `spec_version` - MCP specification version.
* `auth_type` - Authentication type.
* `mcp_access_groups` - List of access groups.
* `command` - Command for stdio transport.
* `args` - Command arguments.
* `env` - Environment variables.
* `allowed_tools` - List of allowed tools.
* `extra_headers` - Extra headers map.
* `static_headers` - Static headers map.
* `authorization_url` - OAuth authorization URL.
* `token_url` - OAuth token URL.
* `registration_url` - OAuth registration URL.
* `allow_all_keys` - Whether all keys are allowed.
* `status` - Current status.
* `last_health_check` - Last health check timestamp.
* `health_check_error` - Health check error message.
* `created_at` - Creation timestamp.
* `created_by` - Creator user ID.
* `updated_at` - Last update timestamp.
* `updated_by` - Last updater user ID.
