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

* `id` - Stable inventory identifier, always `mcp_servers`.
* `mcp_servers` - List of MCP server objects, each containing:
  * `server_id` - The unique identifier.
  * `server_name` - The server name.
  * `alias` - Server alias.
  * `description` - Server description.
  * `url` - (Sensitive) Server URL, when configured.
  * `spec_path` - (Sensitive) LiteLLM-local path or HTTP(S) URL of an OpenAPI specification, when configured.
  * `transport` - Transport type (http, sse, stdio).
  * `spec_version` - Deprecated compatibility field; LiteLLM v1.98 does not return an MCP specification version.
  * `auth_type` - Authentication type.
  * `mcp_access_groups` - Access groups associated with the server.
  * `mcp_info_json` - Sensitive canonical complete MCP info JSON object, or null when the parent is omitted or masked. Present non-object values reject the read.
  * `allowed_tools` - Allowed tool names.
  * `command` - (Sensitive) Stdio command.
  * `args` - (Sensitive) Stdio arguments.
  * `env` - (Sensitive) Stdio environment map.
  * `extra_headers` - Extra forwarded header names.
  * `static_headers` - (Sensitive) Static request headers.
  * `authorization_url` - (Sensitive) OAuth authorization URL.
  * `token_url` - (Sensitive) OAuth token URL.
  * `registration_url` - (Sensitive) OAuth registration URL.
  * `status` - Current status.
  * `allow_all_keys` - Whether all keys are allowed.
  * `available_on_public_internet` - LiteLLM's public-internet classification hint, not a complete reverse-proxy security boundary.
  * `oauth2_flow` - Stored OAuth2 flow (`client_credentials` or `authorization_code`).
  * `instructions` - Server instructions, including an explicit empty string.
  * `tool_name_to_display_name` - Tool display-name overrides.
  * `tool_name_to_description` - Tool description overrides.
  * `delegate_auth_to_upstream` - Whether clients authenticate directly with an upstream OAuth2 server.
  * `oauth_passthrough` - Whether upstream OAuth discovery and challenges are passed through.
  * `dcr_bridge` - Whether LiteLLM's dynamic-client-registration bridge is enabled; null and false remain distinct.
  * `is_byok` - Whether the server enables LiteLLM's per-user BYOK workflow.
  * `byok_description` - BYOK setup instructions.
  * `byok_api_key_help_url` - Informational BYOK API-key help URL.
  * `source_url` - Informational source URL.
  * `timeout` - Positive finite timeout in seconds, or null for LiteLLM's runtime default.
  * `max_concurrent_requests` - Exact outbound request limit. LiteLLM treats null and non-positive values as unlimited.
  * `created_at` - Creation timestamp.
  * `updated_at` - Last update timestamp.

Results are sorted by unique `server_id`; a duplicate, malformed identity, malformed later collection/map, or non-null `credentials` member rejects the complete read before state is published. The list endpoint is read exactly once: the provider does not make per-item singular or health requests. Audit identities, health enrichment, credentials (including write-only OAuth scopes and per-user BYOK secrets), decrypted `env_vars`, role-masked canonical issuer/token-exchange fields, and `upstream_resource` are intentionally unavailable in list items.

LiteLLM may fabricate empty access-group, tool, argument, extra-header, and environment collections during role sanitization or registry-table construction. Those ambiguous empties are projected as null, while nonempty values are retained. `static_headers = {}` is authoritative because the sanitizer uses null for that field. The exact restricted `mcp_info = {"is_public": true}` sentinel becomes null; `{}` remains known canonical JSON. Explicit `allow_all_keys = false` remains known false.

Because each item contains sensitive endpoint, command, environment, header, and OAuth fields, outputs that retain those fields—or the complete item/list—must declare `sensitive = true`. Existing outputs newly derived from those attributes must opt into sensitivity.
