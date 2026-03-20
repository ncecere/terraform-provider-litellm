# litellm_agent Data Source

Retrieves information about a LiteLLM Agent (A2A).

## Example Usage

```hcl
data "litellm_agent" "example" {
  id = "agent-abc-123"
}

output "agent_name" {
  value = data.litellm_agent.example.agent_name
}
```

## Argument Reference

* `id` - (Required) The agent ID to look up.

## Attribute Reference

* `agent_name` - The name of the agent.
* `agent_card_params` - The agent card parameters as a flat string map.
* `litellm_params` - LiteLLM-specific parameters for the agent.
* `tpm_limit` - Tokens per minute limit.
* `rpm_limit` - Requests per minute limit.
* `session_tpm_limit` - Per-session tokens per minute limit.
* `session_rpm_limit` - Per-session requests per minute limit.
* `static_headers` - Static headers sent with agent requests.
* `extra_headers` - Extra header names forwarded from incoming requests.
* `spend` - Total spend for this agent.
* `created_at` - Timestamp when the agent was created.
* `updated_at` - Timestamp when the agent was last updated.
* `created_by` - User who created the agent.
* `updated_by` - User who last updated the agent.
