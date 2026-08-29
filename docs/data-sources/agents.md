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
  * `agent_card_params` - (Sensitive) Historical flat `map(string)` compatibility projection; nested card values use deterministic JSON rendering.
  * `agent_card_params_json` - (Sensitive) Canonical lossless JSON for every observable card/provider/skill/security/signature field.
  * `litellm_params` - (Sensitive) Historical `map(string)` compatibility projection; heterogeneous API values use deterministic exact JSON rendering.
  * `litellm_params_json` - (Sensitive) Canonical lossless JSON for heterogeneous LiteLLM parameters.
  * `object_permission_json` - Canonical lossless JSON for every observable permission field.
  * `static_headers` - (Sensitive) Static request headers.
  * `extra_headers` - Forwarded request-header names.
  * `tpm_limit` - Tokens per minute limit.
  * `rpm_limit` - Requests per minute limit.
  * `session_tpm_limit` - Per-session tokens per minute limit.
  * `session_rpm_limit` - Per-session requests per minute limit.
  * `spend` - Total spend for this agent.
  * `created_at` - Timestamp when the agent was created.
  * `updated_at` - Timestamp when the agent was last updated.
  * `created_by` - User who created the agent.
  * `updated_by` - User who last updated the agent.

Single and list data sources use the same strict projection. Present fields are type-validated, while endpoint-observable partial cards may omit the resource-only card `name`/`url` identity pair. Present malformed values fail the complete read. Role-sanitized omissions are null for that item and never reuse prior data-source state.
