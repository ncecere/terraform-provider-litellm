# litellm_agent Resource

Manages a LiteLLM Agent (A2A). Agents are AI-powered entities that can be discovered, invoked, and composed using the Agent-to-Agent protocol.

## Example Usage

### Minimal Agent

```hcl
resource "litellm_agent" "simple" {
  agent_name = "my-agent"

  agent_card {
    name = "My Agent"
    url  = "https://agent.example.com/a2a"
  }
}
```

### AWS Bedrock AgentCore

LiteLLM v1.98.0 does not persist an `agent_type` value. Its dashboard's “Bedrock AgentCore” recipe creates an ordinary agent with `custom_llm_provider = "bedrock"` and a `bedrock/agentcore/` model. The current resource supports that payload directly:

```hcl
variable "agent_runtime_arn" {
  type = string
}

resource "litellm_agent" "agentcore" {
  agent_name = "bedrock-agentcore"

  # AgentCore derives the invocation endpoint from the ARN, so an empty
  # agent-card URL is intentional for this provider-backed agent.
  agent_card {
    name             = "Bedrock AgentCore"
    url              = ""
    protocol_version = "1.0"

    capabilities {
      streaming = true
    }
  }

  litellm_params = {
    custom_llm_provider = "bedrock"
    model               = "bedrock/agentcore/${var.agent_runtime_arn}"
    qualifier           = "PROD" # optional
  }
}
```

Agent CRUD itself is not enterprise-gated. Invocation requires AWS permissions and an AgentCore runtime that implements the **A2A protocol**; a generic non-A2A AgentCore handler is not compatible with LiteLLM's `/a2a/{agent_id}` bridge.

By default, LiteLLM uses AWS workload credentials and SigV4. Prefer an IAM role attached to the LiteLLM proxy with `bedrock-agentcore:InvokeAgentRuntime` scoped to the runtime ARN, plus any environment-specific KMS and network permissions. Cognito/JWT authentication can be configured through `litellm_params.api_key`; static AWS keys and session tokens are also accepted by LiteLLM but become Terraform state data. `litellm_params` and `static_headers` are marked sensitive to redact normal CLI output, but sensitivity does not encrypt or remove values from state.

### Full Agent with Capabilities and Skills

```hcl
resource "litellm_agent" "full" {
  agent_name      = "code-reviewer"
  tpm_limit       = 10000
  rpm_limit       = 100
  session_tpm_limit = 5000
  session_rpm_limit = 50

  # Legacy map values remain literal strings. Use the additive JSON bridge for
  # heterogeneous nested values; non-overlapping keys from both surfaces merge.
  litellm_params = {
    model = "gpt-4o"
  }
  litellm_params_json = jsonencode({
    stream = false
    retries = 3
    routing = { regions = ["us-east-1", "us-west-2"] }
  })

  static_headers = {
    "X-Custom-Header" = "value"
  }

  extra_headers = ["Authorization"]

  agent_card {
    name             = "Code Reviewer"
    description      = "An agent that reviews code for quality and best practices"
    url              = "https://agent.example.com/a2a"
    version          = "1.0.0"
    protocol_version = "0.3"

    default_input_modes  = ["application/json"]
    default_output_modes = ["application/json", "text/plain"]

    preferred_transport = "httpsse"
    icon_url            = "https://example.com/icon.png"
    documentation_url                   = "https://docs.example.com/code-reviewer"
    supports_authenticated_extended_card = true

    capabilities {
      streaming                = true
      push_notifications       = false
      state_transition_history = true
    }

    provider {
      organization = "Acme Corp"
      url          = "https://acme.example.com"
    }

    skills {
      id          = "review-code"
      name        = "Code Review"
      description = "Reviews code for quality, bugs, and best practices"
      tags        = ["code", "review", "quality"]
      examples    = ["Review this Go function", "Check this Python script"]
      input_modes = ["application/json"]
      output_modes = ["text/plain"]
      security = [
        { oauth2 = ["code:read", "code:review"] },
        { api_key = [] },
      ]
    }

    signatures {
      protected = "eyJhbGciOiJFUzI1NiJ9"
      signature = "base64url-signature"
      header    = jsonencode({ kid = "reviewer-key", critical = ["kid"] })
    }

    skills {
      id          = "suggest-fixes"
      name        = "Suggest Fixes"
      description = "Suggests fixes for identified issues"
      tags        = ["code", "fix"]
    }
  }

  object_permission {
    models      = ["gpt-4o", "gpt-4o-mini"]
    mcp_servers = ["mcp-server-1", "mcp-server-2"]
    agents      = ["other-agent-id"]

    # This attribute intentionally remains map(string) for state and HCL
    # compatibility. Each value is a JSON array of tool-name strings.
    mcp_tool_permissions = {
      "mcp-server-1" = jsonencode(["list_issues", "get_issue"])
      "mcp-server-2" = "[]"
    }
  }
}
```

## Argument Reference

### Top-level

* `agent_name` - (Required) The name of the agent.
* `litellm_params` - (Optional, Sensitive) Legacy `map(string)` of literal LiteLLM parameters. Values such as `"false"`, `"001"`, and JSON-looking text are sent as strings without guessing or coercion.
* `litellm_params_json` - (Optional, Computed, Sensitive) A lossless JSON object for arbitrary string, boolean, exact number, null, list, and object values. It merges with non-overlapping legacy keys. An overlap is valid only when the JSON value is the identical string; otherwise planning fails. Each explicitly configured JSON key transfers only that key's ownership; legacy-only configurations are not rewritten or silently migrated.
* `tpm_limit` - (Optional) Tokens per minute limit for the agent.
* `rpm_limit` - (Optional) Requests per minute limit for the agent.
* `session_tpm_limit` - (Optional) Per-session tokens per minute limit.
* `session_rpm_limit` - (Optional) Per-session requests per minute limit.
* `static_headers` - (Optional, Sensitive) Map of static headers to send with agent requests. Sensitive values remain present in Terraform state.
* `extra_headers` - (Optional) List of extra header names to forward from incoming requests.

### agent_card Block (Required)

* `name` - (Required) Display name of the agent.
* `url` - (Required) The URL endpoint for the agent. Set it to an empty string for provider-backed agents such as Bedrock AgentCore, where LiteLLM derives the endpoint from `litellm_params`.
* `description` - (Optional) Human-readable description of the agent.
* `version` - (Optional) Version of the agent.
* `protocol_version` - (Optional) A2A protocol version. LiteLLM's supported served families are `0.3` and `1.0`; the registry request itself accepts a string, so the provider does not invent a narrower enum.
* `default_input_modes` - (Optional) List of default input MIME types.
* `default_output_modes` - (Optional) List of default output MIME types.
* `preferred_transport` - (Optional) Preferred transport protocol (e.g. `httpsse`, `websocket`).
* `icon_url` - (Optional) URL for the agent's icon.
* `documentation_url` - (Optional) URL for the agent's documentation.
* `supports_authenticated_extended_card` - (Optional) Whether the agent supports an authenticated extended A2A card.

### signatures Block (Optional, repeatable, inside agent_card)

* `protected` - (Required, Sensitive) JWS protected header.
* `signature` - (Required, Sensitive) JWS signature value.
* `header` - (Optional, Sensitive) Arbitrary non-null header object encoded as JSON. Exact numbers and nested values are preserved. Conflicts with `header_json`.
* `header_json` - (Optional, Sensitive) Strict JSON bridge accepting either an object or explicit JSON `null`. Use `header_json = jsonencode(null)` when wire-level null must differ from omission. Conflicts with `header`. Signature order and duplicates are significant; an explicitly empty signatures list clears the list on full-card replacement.

### capabilities Block (Optional, inside agent_card)

* `streaming` - (Optional) Whether the agent supports streaming responses.
* `push_notifications` - (Optional) Whether the agent supports push notifications.
* `state_transition_history` - (Optional) Whether the agent supports state transition history.

Configured capability values are read authoritatively. If LiteLLM accepts a flag but omits it from read-back, the provider treats the effective value as `false` and reports the rejected update instead of retaining false clean state. Unconfigured fields remain unmanaged.

### provider Block (Optional, inside agent_card)

* `organization` - (Optional) Organization name of the agent provider.
* `url` - (Optional) URL of the agent provider.

### skills Block (Optional, repeatable, inside agent_card)

* `id` - (Required) Unique identifier for the skill.
* `name` - (Required) Display name of the skill.
* `description` - (Optional) Description of what the skill does.
* `tags` - (Optional) List of tags for categorizing the skill.
* `examples` - (Optional) List of example inputs.
* `input_modes` - (Optional) List of supported input MIME types.
* `output_modes` - (Optional) List of supported output MIME types.
* `security` - (Optional) Ordered non-null `list(map(list(string)))` of A2A security requirements. Requirement and scope ordering and duplicates are preserved; an explicit empty list clears the skill security field. Conflicts with `security_json`.
* `security_json` - (Optional, Sensitive) Strict JSON bridge accepting either the same ordered security list or explicit JSON `null`. Use `security_json = jsonencode(null)` when wire-level null must differ from omission. Conflicts with `security`.

### object_permission Block (Optional)

* `mcp_servers` - (Optional) List of MCP server IDs the agent can access.
* `mcp_access_groups` - (Optional) List of MCP access groups the agent belongs to.
* `mcp_tool_permissions` - (Optional) `map(string)` of MCP server ID to allowed tools. Each map value must be a valid JSON array containing only strings; use `jsonencode(["tool_a", "tool_b"])`. Empty arrays (`"[]"`) and an explicit empty map (`{}`) are valid and are sent to LiteLLM to clear the corresponding permissions. Do not pass a JSON object for the whole map or a Terraform list as an individual value.
* `models` - (Optional) List of model IDs the agent can use.
* `agents` - (Optional) List of other agent IDs this agent can invoke.

## Attribute Reference

* `id` - The agent ID assigned by LiteLLM.
* `created_at` - Timestamp when the agent was created.
* `updated_at` - Timestamp when the agent was last updated.
* `created_by` - User who created the agent.
* `updated_by` - User who last updated the agent.

## Import

Agents can be imported using their agent ID:

```shell
terraform import litellm_agent.example <agent-id>
```

Import and Read should use a LiteLLM `PROXY_ADMIN` credential when agent configuration contains secrets. LiteLLM masks `litellm_params` and omits `static_headers` for lower-privilege readers; the provider rejects an import that would otherwise store an unrecoverable masked value. Before a PATCH that must preserve masked or API-owned parameter/header leaves, a proxy-admin preflight rehydrates those leaves so an earlier redacted import cannot erase remote credentials.

Imported `mcp_tool_permissions` values are stored as deterministic compact JSON arrays, for example `["list_issues","get_issue"]`. For configured values, the provider preserves the existing JSON spelling when LiteLLM returns the same ordered string array, avoiding whitespace-only drift. API `null` or omission means the permission is absent: an explicit empty-map clear remains stable, while a prior nonempty map is removed from state so Terraform reports a rejected apply or subsequent drift. Older state containing the provider's former invalid rendering (for example `[tool_a tool_b]`) is repaired from a valid API read; it is rejected before any mutation if it cannot first be repaired. Present malformed API permission values fail the read without changing state.

## Lifecycle Safety and Clear Semantics

The resource has no caller-selectable agent ID. Create first accepts a valid nonblank `agent_id` returned by LiteLLM. If a successful response is malformed, oversized, or omits that ID, the provider takes eight independent-connection list samples with 250ms exponential backoff capped at two seconds and unions valid exact-name IDs. Recovery succeeds only for exactly one ID whose fresh GET response matches the complete configured create fingerprint; zero, multiple, malformed, errored, or unreadable samples fail without guessing or reading an empty identity. Once an ID is known, failed, malformed, stale, unstable, or incomplete confirmation produces an error and stores only that confirmed ID as fully known recovery state. Update uses v1.98's merge-aware `PATCH` endpoint, requires two consecutive fresh-connection authoritative confirmations, retries eight times with the same bounded backoff (including transient read failures), and retains prior Terraform state and ownership provenance on failure. Complete-card preflight also requires bounded stable fresh-connection samples. These checks reduce keepalive-pinned stale-worker results, but bounded sampling cannot prove absolute cross-worker durability after the sampling window. A configured custom HTTP transport that cannot establish isolated fresh connections fails safely before confirmation or card replacement. MCP tool permissions retain their native-array semantic confirmation.

Ordinary Read removes state only when LiteLLM returns HTTP 404 exactly. A 500 response is an error even if its body contains words such as “not found.” Delete treats exact 404 as idempotent success and retains state for every other failure. Lifecycle diagnostics intentionally omit response bodies, request URLs, configured values, underlying causes, and secrets.

Removing previously configured values emits endpoint-specific clears instead of silently omitting them:

* `tpm_limit`, `rpm_limit`, `session_tpm_limit`, and `session_rpm_limit` use JSON `null` through `PATCH`.
* `static_headers` uses `{}` and `extra_headers` uses `[]`.
* `object_permission` sends empty arrays for MCP servers, MCP access groups, models, and agents, plus an empty object for MCP tool permissions. Removing the whole Terraform block clears only fields Terraform previously owned; imported or otherwise unowned siblings remain remote-owned.
* Agent-card optional scalars, capabilities, signatures, individual provider fields, and nested skill fields/collections are cleared through a complete card replacement. Any card-changing update first samples the raw authoritative complete card immediately before PATCH, overlays only exact configured/owned paths, and preserves all other keys, types, exact numbers, nulls, and omission. Unowned signature headers and skill security never pass through typed reconstruction. Omitting an API-owned skill preserves it; after HCL has transferred every wire-present leaf, a later omission safely removes it. If required preservation or unique nonblank skill identity cannot be proved, PATCH is not sent.
* Removing keys from `litellm_params` is supported while at least one key remains. Every legacy or structured parameter PATCH starts from a stable fresh authoritative raw object and overlays/removes only exact Terraform-owned keys; unowned values and presence remain byte-semantically exact JSON values.

LiteLLM v1.98 substitutes defaults instead of persisting several empty values. To prevent a false successful apply, the provider rejects these transitions before mutation: clearing the complete `litellm_params` object; removing the complete `agent_card`; clearing `agent_card.version` or `protocol_version`; emptying `default_input_modes` or `default_output_modes`; and removing the complete provider block. An explicitly empty Terraform-owned skills list is allowed because confirmation proves every removed skill ID is absent even if LiteLLM injects a separate unmanaged default chat skill. Keep other defaulted values configured. Direct database changes are outside this API-only provider's safety boundary.

Import records private leaf-level ownership provenance plus separate structural scope for API-owned collections. Imports populate `litellm_params_json` with the complete canonical API object and retain the historical `map(string)` state projection for every legacy key. The JSON bridge and private ownership marker remain the wire authority, so imported booleans, numbers, nulls, lists, and objects cannot be sent back as their compatibility strings. A legacy-only configured resource keeps `litellm_params_json` null and is not rewritten during upgrade. Configuring the JSON bridge transfers only its explicitly present keys after verified apply; configuring either JSON or a legacy key preserves imported structured siblings. Imported/API-owned map keys, card fields, provider/capability children, object-permission fields, and skill leaves remain adopted and refresh from API; later API-added or removed collection members are reconciled without transferring ownership to configured siblings. Ordinary configured resources never adopt an omitted leaf merely because LiteLLM returns an injected default. Configured JSON-bearing map values preserve their spelling when each value is semantically equal. Explicit HCL transfers only its exact configured leaves after authoritative apply; configuring one provider, capability, or skill child does not claim its siblings or consume structural API scope. Optional-only unconfigured object-permission siblings are not adopted on ordinary configured-resource reads. Parent removal is rejected only when fresh ownership proves it would discard an API-owned child. Secret-bearing omitted leaves are preserved by `PATCH`; masked values still require the proxy-admin preflight before mutation.

## Upgrade Note: MCP Tool Permission JSON Values

The public `mcp_tool_permissions` schema remains `map(string)`; there is no state migration, address change, or import syntax change. Every existing configured value must be a JSON string array. Use `jsonencode(...)` when possible. Omitting the attribute leaves it unmanaged within a configured `object_permission` block, while `mcp_tool_permissions = {}` explicitly clears the complete tool-permission map.

## Upgrade Note: Sensitive Agent Maps

`litellm_params` and `static_headers` are now schema-sensitive in both the resource and data source. Existing root-module outputs that expose either map must also set `sensitive = true`, or deliberately call `nonsensitive(...)` after reviewing the contents. This redacts ordinary Terraform CLI/UI rendering but does not remove or encrypt values already held in state.
