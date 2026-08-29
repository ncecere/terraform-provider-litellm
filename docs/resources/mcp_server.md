# litellm_mcp_server (Resource)

Manages MCP (Model Context Protocol) server configurations in LiteLLM. MCP servers allow LLM models to access external tools and data sources through a standardized protocol.

> **Note:** Server names and canonical aliases use 1–128 ASCII letters, digits, underscores, or periods. They cannot contain LiteLLM v1.98's default tool-prefix separator (`-`). Non-empty aliases may contain ASCII spaces in configuration; the provider sends LiteLLM's space-to-underscore normalization while preserving the configured spelling in Terraform state.
>
> Ordinary refresh can fall back to LiteLLM's MCP server collection endpoint if the individual read returns an unexpected error. Create and Update verification use only the direct singular endpoint as mutation authority. A committed create without confirmed readback retains only the server identity; a failed Update or readback retains the complete prior state.

## Example Usage

### Minimal Configuration

```hcl
resource "litellm_mcp_server" "minimal" {
  server_name = "my_mcp_server"
  url         = "https://example.com/mcp"
  transport   = "sse"
}
```

### Custom Identity and Unnamed Server

```hcl
resource "litellm_mcp_server" "custom_identity" {
  server_id = "inventory.tools"
  alias     = "inventory tools"
  transport = "http"
  url       = "https://example.com/mcp"
}
```

LiteLLM stores `inventory_tools`, while Terraform preserves the configured `inventory tools` spelling and compares it semantically to the normalized API value. Omitting both `server_name` and `alias` is also supported; LiteLLM then uses `server_id` as the effective MCP tool prefix.

### Full Configuration

```hcl
resource "litellm_mcp_server" "full" {
  server_name    = "github_mcp_server"
  alias          = "github_mcp"
  description    = "GitHub MCP server"
  url            = "https://api.github.com/mcp"
  transport      = "sse"
  auth_type      = "bearer_token"
  allow_all_keys = true

  mcp_access_groups = ["dev_team"]
  allowed_tools     = ["tool1", "tool2"]
  args              = []

  env = {
    "ENV_VAR" = "value"
  }

  credentials = {
    "auth_value" = "example-bearer-token"
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

### OpenAPI Specification

For HTTP or SSE, LiteLLM v1.98 accepts `spec_path` instead of `url`. The path is resolved by the LiteLLM runtime, not by Terraform, and may also be an HTTP(S) URL.

```hcl
resource "litellm_mcp_server" "openapi" {
  server_name = "inventory_api"
  transport   = "http"
  spec_path   = "/etc/litellm/openapi/inventory.json"
}
```

### Stdio Transport

```hcl
resource "litellm_mcp_server" "stdio_server" {
  server_name = "local_dev_tools"
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

  extra_headers = ["X-API-Version"]

  static_headers = {
    "Accept" = "application/json"
  }

  oauth_scopes = ["mcp.read", "mcp.write"]
  oauth2_flow  = "authorization_code"

  available_on_public_internet = false
  instructions                 = "Use read-only tools unless write access is explicitly requested."

  tool_name_to_display_name = {
    read_data  = "Read_Data"
    write_data = "Write_Data"
  }

  tool_name_to_description = {
    read_data  = "Read records"
    write_data = "Write records"
  }

  allow_all_keys = false
  allowed_tools  = ["read_data", "write_data", "query"]
}
```

## Argument Reference

The following arguments are supported:

### Required

- `transport` - (String) The transport protocol. Must be one of: `http`, `sse`, `stdio`.

### Optional

- `server_id` - (String, Computed, Forces Replacement) A create-only server identity. It must be a non-empty manageable path segment and cannot be `.`/`..` or LiteLLM's reserved `all-team-mcpservers`/`all-proxy-mcpservers` identities. When omitted, the provider selects a stable generated identity. Adding or changing a configured value replaces the resource; existing generated-ID configurations and imports can continue omitting it without replacement.
- `server_name` - (String) An optional 1–128 character MCP tool-prefix name. When omitted, LiteLLM uses `alias`, then `server_id`, as the effective prefix fallback. Removing a configured name sends an explicit null.
- `alias` - (String, Computed) An optional server alias. Non-empty configured aliases are sent with ASCII spaces normalized to underscores, while state preserves configured spelling when the API value is semantically equal. When alias is omitted on Create and `server_name` is set, LiteLLM defaults alias from the name; when both are omitted, `server_id` is the fallback.
- `description` - (String) A human-readable description of the MCP server.
- `url` - (String, Sensitive) The MCP server URL. HTTP and SSE require at least one of `url` or `spec_path`; stdio does not require a URL.
- `spec_path` - (String, Sensitive) A LiteLLM-local path or HTTP(S) URL for an OpenAPI specification. It can satisfy the HTTP/SSE endpoint requirement without `url`; if both are set, LiteLLM uses `url` as the OpenAPI base URL.
- `spec_version` - (String, Deprecated) Compatibility-only attribute retained for existing HCL and state. LiteLLM v1.98 does not accept or return it, so the provider does not send it. New or changed non-default values are rejected; an unchanged historical value remains plannable so existing configurations can be upgraded or destroyed safely. Remove it from configuration when practical.
- `auth_type` - (String) The authentication type. Defaults to `"none"`. LiteLLM v1.98 accepts exactly `none`, `api_key`, `bearer_token`, `basic`, `authorization`, `oauth2`, `aws_sigv4`, `token`, `oauth2_token_exchange`, `oauth2_id_jag`, `true_passthrough`, or `oauth_delegate`. When using a value other than `"none"`, the selected mode may require credentials and additional endpoint-specific fields.
- `mcp_access_groups` - (List of String) Access groups that are allowed to use this MCP server.
- `command` - (String, Sensitive) Command to execute for `stdio` transport.
- `args` - (List of String, Sensitive) Arguments to pass to the command for `stdio` transport.
- `env` - (Map of String, Sensitive) Environment variables to set when running the MCP server.
- `credentials` - (Map of String, Sensitive) Credentials for authenticating with the MCP server. For static `api_key`, `bearer_token`, `basic`, `authorization`, and `token` modes, LiteLLM v1.98 reads the secret from `auth_value`; keys named `token` or `api_key` are ignored. OAuth2 uses fields such as `client_id` and `client_secret`; optional `upstream_resource` configures the non-secret RFC 8707 resource indicator. The resource keeps the complete map sensitive, while the singular data source may expose only LiteLLM's separately reviewed `upstream_resource` scalar.
- `allowed_tools` - (List of String) List of tool names that are allowed to be used from this server.
- `extra_headers` - (List of String) Extra header names to forward/include in requests. This matches the LiteLLM API schema.
- `static_headers` - (Map of String, Sensitive) Static HTTP headers that are always included in requests.
- `authorization_url` - (String, Sensitive) OAuth2 authorization URL (used with `oauth2` auth type).
- `token_url` - (String, Sensitive) OAuth2 token URL (used with `oauth2` auth type).
- `registration_url` - (String, Sensitive) OAuth2 dynamic client registration URL (used with `oauth2` auth type).
- `allow_all_keys` - (Bool) Whether all API keys are allowed to access this MCP server.
- `oauth_scopes` - (List of String, Sensitive) Write-only OAuth scopes stored by LiteLLM at `credentials.scopes`. LiteLLM v1.98 strips this member from every management response, so imports leave it null and data sources do not expose it. An explicit empty list clears owned scopes. Configure scopes only through this argument; `credentials["scopes"]` is rejected because the legacy credentials map cannot represent a native list.
- `available_on_public_internet` - (Bool, Computed) IP-classification hint used by LiteLLM. Explicit `false` is preserved. Removing owned configuration restores LiteLLM's durable `true` default. This is not a complete security boundary, especially when untrusted reverse-proxy headers affect client-IP classification.
- `oauth2_flow` - (String, Computed) Stored OAuth2 flow: `client_credentials` or `authorization_code`. LiteLLM can stamp a flow when it is omitted; the provider adopts the returned value rather than inferring it locally. Removing owned configuration sends null.
- `instructions` - (String, Computed) Server instructions. An explicitly configured empty string is preserved; removing owned configuration sends null.
- `tool_name_to_display_name` - (Map of String, Computed) Tool display-name overrides. Each value may be empty or contain only ASCII letters, digits, underscores, and hyphens. An empty map is an explicit clear.
- `tool_name_to_description` - (Map of String, Computed) Tool description overrides with arbitrary string values. An empty map is an explicit clear.
- `skip_url_validation` - (Bool, Deprecated) Compatibility-only attribute retained for existing HCL and state. LiteLLM v1.98 does not accept it, so the provider does not send it. New or changed `true` values are rejected; an unchanged historical `true` remains plannable so unrelated updates and destroy continue to work. Remove the argument (`false` remains a safe migration no-op).
- `mcp_info_json` - (String, Optional, Computed, Sensitive) A complete non-null JSON object for whole-document MCP info ownership. The empty object (`{}`) means an explicit whole-document clear. It conflicts with `mcp_info`, `mcp_info_overrides_json`, and `mcp_info_clear_paths`.
- `mcp_info_overrides_json` - (String, Optional, Sensitive) A recursively selective non-null JSON object. Scalars, arrays, nested `null`, and nested empty objects are atomic owned values; a non-empty nested object owns only its recursively selected members.
- `mcp_info_clear_paths` - (List of String, Optional, Sensitive) Canonical RFC 6901 object-member pointers that record explicit tombstones. Root pointers, array traversal, duplicates, equal paths, and ancestor/descendant conflicts are rejected.

> Removing fixed fields or selective overrides relinquishes Terraform ownership; it does not request remote deletion. Only an explicit clear path, or whole-document `{}`, expresses deletion intent. Fixed fields can be combined with disjoint overrides and clears. Ownership paths may not overlap or contain one another.
>
> The provider hydrates the complete remote object before Update and sends a complete `mcp_info` document whenever an Update is required. Unknown access-control flags, nested objects, arrays, nulls, and exact JSON numbers are preserved. A null or omitted API parent is treated as role masking, not an empty object: Update can proceed only from a previously authoritative complete JSON snapshot. Post-write direct readback must confirm owned values, clears, fixed fields, and every preserved unowned path before state or ownership generation is committed. Equal-value ownership takeover commits provenance without a PUT.

### Presence-aware field clears

The existing `alias`, `description`, `command`, OAuth URL, access-group, argument, environment, tool/header, credential, and `allow_all_keys` arguments, plus the six v1.98 parity fields above, have presence-aware ownership without changing any earlier value type. Alias is now Optional+Computed so its canonical LiteLLM normalization/default can be represented without drift. A known configured value, including an empty collection, empty string, or `false`, acquires ownership. An unknown expression retains prior ownership. Removing a previously owned argument sends LiteLLM v1.98's exact clear sentinel; an omitted unowned argument is never projected into an Update.

Update always performs an identity- and type-valid direct singular read first, then sends only the changed managed values and owned removals. Collection null/omission/empty responses may be role-redacted and therefore do not erase a prior owned projection. Credential response values, including OAuth scopes, are never authoritative; configured sensitive values survive redaction. Scope confirmation is limited to an accepted write plus identity/schema-valid direct readback and never pretends the values were observed. Because v1.98 merges credential maps, deleting individual configured keys is rejected. Remove the entire `credentials` argument and apply its top-level `null` clear first, then re-add the replacement map in a second apply.

LiteLLM v1.98 can implicitly clear OAuth endpoints when `url` or the credential authentication class changes, and replaces the complete credentials object on an authentication-class change. The provider rejects the operation before PUT unless every affected existing value is explicitly owned, genuinely changed or cleared, and supplied completely in the same update. A credential-class change must configure both the complete `credentials` map and a known `oauth_scopes` list (use `[]` for no scopes); unknown, omitted, or previously owned scopes are not recoverable from redacted responses. It never attempts a restorative second PUT.

Legacy state has no trustworthy presence history. On the first ownership-aware plan (schema v3, upgraded directly to the current schema v5), ownership is acquired only from known non-null configuration; public state is never used as ownership evidence. Consequently, removing an ambiguously historical value during that first upgrade does not clear it remotely. For a safe migration, first apply with the value still configured to record ownership, then remove it and apply again. This two-step rule prevents accidental first-upgrade clears.

Schema v5 upgrades v0, v1, v2, v3, and v4 directly. It retains the v0 `extra_headers` conversion and existing MCP-info controls, preserves existing ownership generations, and initializes the six additive parity fields plus audit additions to typed null without changing earlier lifecycle values.

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
- `server_id` - The canonical server identifier. It is configurable only at Create and otherwise contains the provider-selected generated identity.
- `created_at` - Timestamp of when the MCP server was created.
- `created_by` - The user or system that created the MCP server.
- `updated_at` - Timestamp of the latest server update. Null/omission under role redaction preserves a known prior value; a first restricted create/import resolves it to null.
- `updated_by` - The user or system that last updated the server, with the same role-redaction retention semantics as `updated_at`.
- `mcp_info_ownership_generation` - A non-sensitive computed generation that changes when MCP info ownership intent changes, including equal-value takeover. It forces Apply without making the resource ID unknown.
- `field_ownership_generation` - A non-sensitive computed generation for presence-aware ownership of MCP fields, including the six v1.98 parity additions. It forces Apply for ownership-only takeover or removal without changing identity.

## Migrating to the v1.98 transport contract

- Remove `spec_version`; it remains in schema/state for compatibility but is no longer sent or read. Existing non-default state/HCL can remain unchanged during an upgrade or unrelated update, but changing to another unsupported value is rejected.
- Remove `skip_url_validation`. Existing state/HCL with `true` can remain unchanged during an upgrade, unrelated update, or destroy, but a new or changed `true` is rejected because v1.98 cannot honor it. Omitted or `false` is a no-op and is not sent.
- Remove synthetic stdio URLs. Stdio requires a non-empty `command` and non-empty `args`; the executable basename must be one of LiteLLM v1.98's built-in commands: `deno`, `docker`, `node`, `npx`, `python`, `python3`, or `uvx`. Absolute paths to those executable names are accepted.
- For HTTP/SSE, configure a non-empty `url`, a non-empty `spec_path`, or both. Empty strings are rejected; omit a field to clear or release it.

Existing state addresses and attribute types are unchanged. Refresh/import preserves URL-less stdio and spec-path objects without synthesizing a URL.

## Migrating from `bearer`

The earlier provider accepted `auth_type = "bearer"`, but LiteLLM v1.98 does not. Existing configurations must replace it with `auth_type = "bearer_token"` for bearer-token authentication, or `auth_type = "oauth2"` when configuring OAuth endpoints and client credentials. Leaving `bearer` in configuration now fails Terraform validation during planning. Imported/read state is not rewritten by configuration validation.

## Import

MCP servers can be imported using their server ID:

```shell
terraform import litellm_mcp_server.example <server-id>
```

On the first authoritative read after import, the provider records the complete visible object in sensitive `mcp_info_json`, adopts representable numeric cost leaves in `mcp_info.mcp_server_cost_info`, and leaves display leaves unowned. Arbitrarily typed known or unknown members remain lossless in JSON even when they cannot be projected into the fixed block. The MCP-info import marker is retained while the parent is null or omitted and is cleared only after an authoritative object read. Presence-aware field provenance starts empty and its independent marker clears after an identity-valid singular read. Visible non-empty collection projections do not establish ownership; Optional-only scalar import behavior is unchanged. Subsequent refreshes preserve semantically equivalent JSON spelling and do not infer ownership from public values.

## Transport Types

### HTTP

Standard HTTP/HTTPS communication. Configure a server `url`, an OpenAPI `spec_path`, or both. Supports authentication via `auth_type`.

### SSE (Server-Sent Events)

Real-time streaming communication. Configure a server `url`, an OpenAPI `spec_path`, or both.

### Stdio

Standard input/output communication executed by the LiteLLM runtime. A URL is not required. Both `command` and at least one `args` item are required, and the command is restricted to LiteLLM's built-in allowlist described above. `env` is optional.

## Notes

- Server names and canonical aliases use ASCII letters, digits, underscores, or periods; aliases normalize ASCII spaces to underscores. Hyphens are reserved as LiteLLM v1.98's tool-prefix separator.
- The `auth_type` field accepts exactly `none`, `api_key`, `bearer_token`, `basic`, `authorization`, `oauth2`, `aws_sigv4`, `token`, `oauth2_token_exchange`, `oauth2_id_jag`, `true_passthrough`, or `oauth_delegate` under the LiteLLM v1.98 request contract. The legacy literal `bearer` is not supported.
- For authenticated modes, provide the credentials and endpoint-specific fields required by the selected authentication type.
- Terraform marks `credentials`, `oauth_scopes`, endpoint, stdio command/argument/environment, static-header, and OAuth URL attributes sensitive, which redacts normal CLI output. Sensitive values are still stored in Terraform state; protect the state backend accordingly.
- Existing outputs derived from `url`, `spec_path`, `command`, `args`, `env`, `static_headers`, `authorization_url`, `token_url`, or `registration_url` must opt into sensitivity with `sensitive = true`.
- Use `mcp_access_groups` to control which teams or users can access the MCP server tools.
- Configure cost tracking through the `mcp_info.mcp_server_cost_info` block to monitor spending on MCP tool usage.
