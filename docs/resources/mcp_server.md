# litellm_mcp_server (Resource)

Manages MCP (Model Context Protocol) server configurations in LiteLLM. MCP servers allow LLM models to access external tools and data sources through a standardized protocol.

> **Note:** Server names and aliases **cannot contain hyphens** (`-`). The LiteLLM API rejects them. Use underscores (`_`) instead.

## Example Usage

### Minimal Configuration

```hcl
resource "litellm_mcp_server" "minimal" {
  server_name = "my_mcp_server"
  url         = "https://example.com/mcp"
  transport   = "sse"
}
```

### Full Configuration

```hcl
resource "litellm_mcp_server" "full" {
  server_name    = "github_mcp_server"
  alias          = "github_mcp"
  description    = "GitHub MCP server"
  url            = "https://api.github.com/mcp"
  transport      = "sse"
  spec_version   = "2024-11-05"
  auth_type      = "none"
  allow_all_keys = true

  mcp_access_groups = ["dev_team"]
  allowed_tools     = ["tool1", "tool2"]
  args              = []

  env = {
    "ENV_VAR" = "value"
  }

  credentials = {
    "token" = "my-token"
  }

  static_headers = {
    "X-Static" = "static-value"
  }

  mcp_info {
    server_name = "GitHub MCP Server"
    description = "Repository operations"
    logo_url    = "https://example.com/logo.png"

    mcp_server_cost_info {
      default_cost_per_query = 0.01

      tool_name_to_cost_per_query = {
        "tool1" = 0.02
        "tool2" = 0.005
      }
    }
  }
}
```

### Authenticated Server

```hcl
resource "litellm_mcp_server" "authenticated" {
  server_name = "private_mcp_server"
  url         = "https://private.example.com/mcp"
  transport   = "http"
  auth_type   = "bearer_token"

  credentials = {
    "auth_value" = "my-secret-token"
  }
}
```

### Stdio Transport

```hcl
resource "litellm_mcp_server" "stdio_server" {
  server_name = "local_dev_tools"
  url         = "stdio://local"
  transport   = "stdio"
  command     = "python3"
  args        = ["/opt/mcp-servers/dev-tools/server.py", "--verbose"]

  env = {
    "PYTHONPATH" = "/opt/mcp-servers/dev-tools"
    "DEBUG"      = "true"
  }
}
```

### OAuth2 Configuration

```hcl
resource "litellm_mcp_server" "oauth_server" {
  server_name = "oauth_protected_server"
  url         = "https://api.example.com/mcp"
  transport   = "http"
  auth_type   = "oauth2"

  authorization_url = "https://auth.example.com/oauth/authorize"
  token_url         = "https://auth.example.com/oauth/token"
  registration_url  = "https://auth.example.com/oauth/register"

  credentials = {
    "client_id"     = var.oauth_client_id
    "client_secret" = var.oauth_client_secret
  }

  extra_headers = {
    "X-API-Version" = "2024-01"
  }

  static_headers = {
    "Accept" = "application/json"
  }

  allow_all_keys = false
  allowed_tools  = ["read_data", "write_data", "query"]
}
```

## Argument Reference

The following arguments are supported:

### Required

- `server_name` - (String) The name of the MCP server. **Must not contain hyphens.**
- `url` - (String) The URL endpoint of the MCP server.
- `transport` - (String) The transport protocol. Must be one of: `http`, `sse`, `stdio`.

### Optional

- `alias` - (String) An alias for the server. **Must not contain hyphens.**
- `description` - (String) A human-readable description of the MCP server.
- `spec_version` - (String) The MCP specification version. Defaults to `"2024-11-05"`.
- `auth_type` - (String) The authentication type. Defaults to `"none"`. Supported values: `none`, `bearer_token`, `bearer`, `basic`, `api_key`, `authorization`, `oauth2`. When using a value other than `"none"`, the API requires credentials to be provided.
- `mcp_access_groups` - (List of String) Access groups that are allowed to use this MCP server.
- `command` - (String) Command to execute for `stdio` transport.
- `args` - (List of String) Arguments to pass to the command for `stdio` transport.
- `env` - (Map of String) Environment variables to set when running the MCP server.
- `credentials` - (Map of String, Sensitive) Credentials for authenticating with the MCP server. This attribute is marked as sensitive and will not be displayed in plan output.
- `allowed_tools` - (List of String) List of tool names that are allowed to be used from this server.
- `extra_headers` - (Map of String) Additional HTTP headers to include in requests.
- `static_headers` - (Map of String) Static HTTP headers that are always included in requests.
- `authorization_url` - (String) OAuth2 authorization URL (used with `oauth2` auth type).
- `token_url` - (String) OAuth2 token URL (used with `oauth2` auth type).
- `registration_url` - (String) OAuth2 dynamic client registration URL (used with `oauth2` auth type).
- `allow_all_keys` - (Bool) Whether all API keys are allowed to access this MCP server.

### Nested Blocks

#### `mcp_info`

Optional block containing display and cost information for the MCP server.

- `server_name` - (String, Optional) Display name of the MCP server.
- `description` - (String, Optional) Display description of the MCP server.
- `logo_url` - (String, Optional) URL to the server's logo image.

##### `mcp_server_cost_info`

Optional nested block within `mcp_info` containing cost configuration.

- `default_cost_per_query` - (Float64, Optional) Default cost per query for all tools.
- `tool_name_to_cost_per_query` - (Map of Float64, Optional) Per-tool cost overrides, mapping tool names to their cost per query.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

- `id` - The unique identifier for the MCP server (same as `server_id`).
- `server_id` - The server identifier assigned by LiteLLM.
- `created_at` - Timestamp of when the MCP server was created.
- `created_by` - The user or system that created the MCP server.

## Import

MCP servers can be imported using their server ID:

```shell
terraform import litellm_mcp_server.example <server-id>
```

## Transport Types

### HTTP

Standard HTTP/HTTPS communication. Suitable for REST API-based MCP servers. Supports authentication via `auth_type`.

### SSE (Server-Sent Events)

Real-time streaming communication. Ideal for servers that need to push updates.

### Stdio

Standard input/output communication. Used for local MCP servers or command-line tools. Requires `command` and optionally `args` and `env`.

## Notes

- Server names and aliases must use underscores, not hyphens.
- The `auth_type` field supports `none`, `bearer_token`, `bearer`, `basic`, `api_key`, `authorization`, and `oauth2`.
- When using an `auth_type` other than `"none"`, provide authentication details via the `credentials` map.
- The `credentials` attribute is sensitive and will not appear in CLI output or state file in plain text.
- Use `mcp_access_groups` to control which teams or users can access the MCP server tools.
- Configure cost tracking through the `mcp_info.mcp_server_cost_info` block to monitor spending on MCP tool usage.
