# litellm_agents Data Source

Fetches a list of all LiteLLM Agents (A2A).

## Example Usage

```hcl
data "litellm_agents" "all" {}

output "agent_names" {
  value = [for a in data.litellm_agents.all.agents : a.agent_name]
}
```

## Attribute Reference

* `agents` - List of agents. Each agent has the following attributes:
  * `agent_id` - The unique agent ID.
  * `agent_name` - The name of the agent.
  * `tpm_limit` - Tokens per minute limit.
  * `rpm_limit` - Requests per minute limit.
  * `session_tpm_limit` - Per-session tokens per minute limit.
  * `session_rpm_limit` - Per-session requests per minute limit.
  * `spend` - Total spend for this agent.
  * `created_at` - Timestamp when the agent was created.
  * `updated_at` - Timestamp when the agent was last updated.
  * `created_by` - User who created the agent.
  * `updated_by` - User who last updated the agent.
