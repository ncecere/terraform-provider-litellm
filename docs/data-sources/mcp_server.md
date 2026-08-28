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
  sensitive = true
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
* `url` - (Sensitive) Server URL, when configured. URL-less stdio and spec-path-only servers return null.
* `spec_path` - (Sensitive) LiteLLM-local path or HTTP(S) URL of an OpenAPI specification, when configured.
* `transport` - Transport type (http, sse, stdio).
* `spec_version` - Deprecated compatibility field; LiteLLM v1.98 does not return it.
* `auth_type` - Authentication type.
* `mcp_info_json` - Sensitive canonical complete MCP info JSON object. It is null when LiteLLM omits or masks the parent. A present non-object response is rejected.
* `mcp_access_groups` - List of access groups.
* `command` - (Sensitive) Command for stdio transport.
* `args` - (Sensitive) Command arguments.
* `env` - (Sensitive) Environment variables.
* `allowed_tools` - List of allowed tools.
* `extra_headers` - Extra header names list.
* `static_headers` - (Sensitive) Static headers map.
* `authorization_url` - (Sensitive) OAuth authorization URL.
* `token_url` - (Sensitive) OAuth token URL.
* `registration_url` - (Sensitive) OAuth registration URL.
* `allow_all_keys` - Whether all keys are allowed.
* `status` - Current status.
* `last_health_check` - Last health check timestamp.
* `health_check_error` - Health check error message.
* `created_at` - Creation timestamp.
* `created_by` - Creator user ID.
* `updated_at` - Last update timestamp.
* `updated_by` - Last updater user ID.
* `upstream_resource` - Non-sensitive RFC 8707 upstream resource indicator. This is the only credential member exposed, and only when LiteLLM returns exactly that one nonempty string member in its redacted `credentials` object.

LiteLLM role sanitizers can replace access groups, allowed tools, command arguments, extra header names, and environment maps with empty collections. The data source treats those ambiguous empty values as null; nonempty values remain known. An empty `static_headers` object remains authoritative because LiteLLM masks that field with null instead. The exact restricted `mcp_info = {"is_public": true}` sentinel is also projected as null, while `{}` remains known canonical JSON.

Because `url`, `spec_path`, `command`, `args`, `env`, `static_headers`, and the OAuth endpoint attributes are sensitive, existing outputs derived from them must declare `sensitive = true`.
