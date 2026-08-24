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

  litellm_params = {
    model = "gpt-4o"
  }

  static_headers = {
    "X-Custom-Header" = "value"
  }

  extra_headers = ["Authorization"]

  agent_card {
    name             = "Code Reviewer"
    description      = "An agent that reviews code for quality and best practices"
    url              = "https://agent.example.com/a2a"
    version          = "1.0.0"
    protocol_version = "0.2.6"

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
    mcp_servers = ["mcp-server-1"]
    agents      = ["other-agent-id"]
  }
}
```

## Argument Reference

### Top-level

* `agent_name` - (Required) The name of the agent.
* `litellm_params` - (Optional, Sensitive) Map of LiteLLM-specific parameters (e.g. `model`, `api_key`). Prefer proxy workload identity over static credentials. Sensitive values remain present in Terraform state.
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
* `protocol_version` - (Optional) A2A protocol version (e.g. `0.2.6`).
* `default_input_modes` - (Optional) List of default input MIME types.
* `default_output_modes` - (Optional) List of default output MIME types.
* `preferred_transport` - (Optional) Preferred transport protocol (e.g. `httpsse`, `websocket`).
* `icon_url` - (Optional) URL for the agent's icon.
* `documentation_url` - (Optional) URL for the agent's documentation.
* `supports_authenticated_extended_card` - (Optional) Whether the agent supports an authenticated extended A2A card.

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

### object_permission Block (Optional)

* `mcp_servers` - (Optional) List of MCP server IDs the agent can access.
* `mcp_access_groups` - (Optional) List of MCP access groups the agent belongs to.
* `mcp_tool_permissions` - (Optional) Map of MCP server ID to allowed tools (JSON-encoded).
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

Import and Read should use a LiteLLM `PROXY_ADMIN` credential when agent configuration contains secrets. LiteLLM masks `litellm_params` and omits `static_headers` for lower-privilege readers; the provider rejects an import that would otherwise store an unrecoverable masked value. Before every PUT, a proxy-admin preflight rehydrates unmanaged parameter/header fields so an earlier redacted import cannot erase remote credentials.

## Upgrade Note: Sensitive Agent Maps

`litellm_params` and `static_headers` are now schema-sensitive in both the resource and data source. Existing root-module outputs that expose either map must also set `sensitive = true`, or deliberately call `nonsensitive(...)` after reviewing the contents. This redacts ordinary Terraform CLI/UI rendering but does not remove or encrypt values already held in state.
