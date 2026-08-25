# litellm_guardrails Data Source

Retrieves the LiteLLM v1.98 guardrail registry through `GET /v2/guardrails/list`. This includes active database-backed guardrails visible to the caller and initialized config-file guardrails, with database identities taking precedence over duplicates.

## Example Usage

```hcl
data "litellm_guardrails" "all" {}

output "guardrail_count" {
  value = length(data.litellm_guardrails.all.guardrails)
}

output "guardrail_names" {
  value = [for g in data.litellm_guardrails.all.guardrails : g.guardrail_name]
}

# Find pre-call guardrails
locals {
  pre_call_guardrails = [
    for g in data.litellm_guardrails.all.guardrails : g
    if g.mode == "pre_call"
  ]
}

output "input_guardrails" {
  value = [for g in local.pre_call_guardrails : g.guardrail_name]
}
```

## Argument Reference

This data source has no required arguments.

## Attribute Reference

* `id` - Placeholder identifier.
* `guardrails` - List of guardrail objects, each containing:
  * `guardrail_id` - The unique identifier.
  * `guardrail_name` - Human-readable name for the guardrail.
  * `guardrail` - The guardrail integration type.
  * `mode` - When to apply the guardrail: a mode string, canonical JSON string array, or canonical JSON `Mode` object with tag-specific routing.
  * `default_on` - Whether the guardrail is enabled by default.
  * `litellm_params` - Sensitive canonical JSON object string of provider-specific configuration; empty additional parameters are `{}` rather than null. Credential-bearing values are LiteLLM-masked inventory values, not plaintext credentials.
  * `created_at` - Creation timestamp.
  * `updated_at` - Last update timestamp.

## Inventory Scope and Security

The provider intentionally uses the v2 registry endpoint so resources created through `litellm_guardrail` appear in this inventory. LiteLLM's legacy `GET /guardrails/list` endpoint is a config-file-only view and is not used by this data source. LiteLLM v1.98 exposes no pagination or filter parameters for the v2 route; the provider performs one strict snapshot read and deterministically sorts it by ID and name.

Visibility depends on LiteLLM authorization: administrators can see the complete active registry, while other roles can be limited to global and team-visible guardrails. The provider remains HTTP API-only.

Because nested `litellm_params` is sensitive, outputs containing complete guardrail objects may also need `sensitive = true`. LiteLLM masks sensitive parameter keys before returning list results.
