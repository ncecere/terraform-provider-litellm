# litellm_mcp_servers Data Source

Retrieves a list of all LiteLLM MCP (Model Context Protocol) servers.

## Example Usage

### Minimal Example

```hcl
data "litellm_mcp_servers" "all" {}
```

### Full Example

```hcl
data "litellm_mcp_servers" "all" {}

output "mcp_server_count" {
  value = length(data.litellm_mcp_servers.all.mcp_servers)
}

output "server_names" {
  value = [for s in data.litellm_mcp_servers.all.mcp_servers : s.server_name]
}

# Find HTTP transport servers
locals {
  http_servers = [
    for s in data.litellm_mcp_servers.all.mcp_servers : s
    if s.transport == "http"
  ]
}

output "http_server_names" {
  value = [for s in local.http_servers : s.server_name]
}

# Find unhealthy servers
locals {
  unhealthy_servers = [
    for s in data.litellm_mcp_servers.all.mcp_servers : s
    if s.status != "healthy"
  ]
}

output "unhealthy_server_count" {
  value = length(local.unhealthy_servers)
}
```

## Argument Reference

This data source has no required arguments.

## Attribute Reference

The following attributes are exported:

* `id` - Placeholder identifier.
* `mcp_servers` - List of MCP server objects, each containing:
  * `server_id` - The unique identifier.
  * `server_name` - The server name.
  * `alias` - Server alias.
  * `description` - Server description.
  * `url` - Server URL.
  * `transport` - Transport type (http, sse, stdio).
  * `spec_version` - MCP specification version.
  * `auth_type` - Authentication type.
  * `status` - Current status.
  * `allow_all_keys` - Whether all keys are allowed.
  * `created_at` - Creation timestamp.
  * `updated_at` - Last update timestamp.
