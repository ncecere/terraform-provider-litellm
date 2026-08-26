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
* `agent_card_params` - (Sensitive) Historical flat `map(string)` compatibility projection; nested card values use deterministic JSON rendering.
* `agent_card_params_json` - (Sensitive) Canonical lossless JSON for every observable card field, including provider, capabilities, skills, skill security, and ordered signatures.
* `litellm_params` - (Sensitive) Historical `map(string)` compatibility projection. API strings remain literal; non-string API values use deterministic exact JSON rendering while `litellm_params_json` preserves their authoritative wire types.
* `litellm_params_json` - (Sensitive) Canonical lossless JSON for every observable heterogeneous LiteLLM parameter.
* `object_permission_json` - Canonical lossless JSON for every observable permission field, including native MCP tool arrays.
* `tpm_limit` - Tokens per minute limit.
* `rpm_limit` - Requests per minute limit.
* `session_tpm_limit` - Per-session tokens per minute limit.
* `session_rpm_limit` - Per-session requests per minute limit.
* `static_headers` - (Sensitive) Static headers sent with agent requests. Redacted in normal CLI output but still present in state.
* `extra_headers` - Extra header names forwarded from incoming requests.
* `spend` - Total spend for this agent.
* `created_at` - Timestamp when the agent was created.
* `updated_at` - Timestamp when the agent was last updated.
* `created_by` - User who created the agent.
* `updated_by` - User who last updated the agent.

> **Role and sensitivity note:** Agent-card JSON, both LiteLLM parameter projections, and static headers propagate Terraform sensitivity. Root outputs exposing them must set `sensitive = true` or deliberately use `nonsensitive(...)`. Use a LiteLLM `PROXY_ADMIN` credential for secret-bearing configuration. Present fields are strictly type-validated, but endpoint-observable partial cards do not need the resource-only card `name`/`url` identity pair. A present malformed or unrecoverably masked value fails the read. Fields omitted by role sanitization are reported as null for this data-source read; no prior data-source value is reused.
