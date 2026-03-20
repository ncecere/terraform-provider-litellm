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
    documentation_url   = "https://docs.example.com/code-reviewer"

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
* `litellm_params` - (Optional) Map of LiteLLM-specific parameters (e.g. `model`, `api_key`).
* `tpm_limit` - (Optional) Tokens per minute limit for the agent.
* `rpm_limit` - (Optional) Requests per minute limit for the agent.
* `session_tpm_limit` - (Optional) Per-session tokens per minute limit.
* `session_rpm_limit` - (Optional) Per-session requests per minute limit.
* `static_headers` - (Optional) Map of static headers to send with agent requests.
* `extra_headers` - (Optional) List of extra header names to forward from incoming requests.

### agent_card Block (Required)

* `name` - (Required) Display name of the agent.
* `url` - (Required) The URL endpoint for the agent.
* `description` - (Optional) Human-readable description of the agent.
* `version` - (Optional) Version of the agent.
* `protocol_version` - (Optional) A2A protocol version (e.g. `0.2.6`).
* `default_input_modes` - (Optional) List of default input MIME types.
* `default_output_modes` - (Optional) List of default output MIME types.
* `preferred_transport` - (Optional) Preferred transport protocol (e.g. `httpsse`, `websocket`).
* `icon_url` - (Optional) URL for the agent's icon.
* `documentation_url` - (Optional) URL for the agent's documentation.

### capabilities Block (Optional, inside agent_card)

* `streaming` - (Optional) Whether the agent supports streaming responses.
* `push_notifications` - (Optional) Whether the agent supports push notifications.
* `state_transition_history` - (Optional) Whether the agent supports state transition history.

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
